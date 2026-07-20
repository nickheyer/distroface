package mirror

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/logger"
	"github.com/nickheyer/distroface/pkg/natsort"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Placeholder host, requests never leave the process
const localRegistryHost = "mirror.invalid"

// Pulls upstream oci tags and pushes them into the embedded registry
type ociSyncer struct {
	registry          http.Handler
	tokens            *auth.TokenService
	upstreamTransport http.RoundTripper
}

func NewOCISyncer(registry http.Handler, tokens *auth.TokenService) *ociSyncer {
	return &ociSyncer{registry: registry, tokens: tokens}
}

// Normalizes an upstream reference, bare names default to docker hub
func upstreamRepo(upstream string) (name.Repository, error) {
	s := strings.TrimSpace(upstream)
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.Trim(s, "/")
	if s == "" {
		return name.Repository{}, fmt.Errorf("%w: upstream is required", ErrInvalid)
	}
	if strings.ContainsAny(path.Base(s), ":@") {
		return name.Repository{}, fmt.Errorf("%w: upstream must be a repository without a tag or digest", ErrInvalid)
	}
	repo, err := name.NewRepository(s)
	if err != nil {
		return name.Repository{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if repo.RegistryStr() == name.DefaultRegistry && !strings.Contains(repo.RepositoryStr(), "/") {
		repo, err = name.NewRepository(name.DefaultRegistry + "/library/" + repo.RepositoryStr())
		if err != nil {
			return name.Repository{}, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
	}
	return repo, nil
}

func upstreamAuth(cfg *v1.MirrorConfig) authn.Authenticator {
	if cfg.GetAuthToken() == "" {
		return authn.Anonymous
	}
	user := cfg.GetUsername()
	if user == "" {
		user = "oauth2"
	}
	return &authn.Basic{Username: user, Password: cfg.GetAuthToken()}
}

func (o *ociSyncer) srcOpts(ctx context.Context, cfg *v1.MirrorConfig) []remote.Option {
	return []remote.Option{
		remote.WithContext(ctx),
		remote.WithTransport(o.upstreamTransport),
		remote.WithAuth(upstreamAuth(cfg)),
	}
}

func (o *ociSyncer) dstOpts(ctx context.Context, namespace, repoName string) []remote.Option {
	full := namespace + "/" + repoName
	rt := &inprocTransport{
		handler: o.registry,
		token: func() (string, error) {
			return o.tokens.SignToken("system:mirror", []*auth.ResourceActions{{
				Type:    "repository",
				Name:    full,
				Actions: []string{"pull", "push"},
			}})
		},
	}
	return []remote.Option{
		remote.WithContext(ctx),
		remote.WithTransport(rt),
	}
}

func (o *ociSyncer) validate(ctx context.Context, cfg *v1.MirrorConfig) error {
	src, err := upstreamRepo(cfg.GetUpstream())
	if err != nil {
		return err
	}
	if _, err := remote.List(src, o.srcOpts(ctx, cfg)...); err != nil {
		return fmt.Errorf("%w: cannot list tags for %s: %v", ErrInvalid, src.String(), err)
	}
	return nil
}

func (o *ociSyncer) syncRepo(ctx context.Context, repo *db.Repository, cfg *v1.MirrorConfig, log *logger.Logger) error {
	src, err := upstreamRepo(cfg.GetUpstream())
	if err != nil {
		return err
	}
	srcOpts := o.srcOpts(ctx, cfg)
	tags, err := remote.List(src, srcOpts...)
	if err != nil {
		return classifyOCIErr(err)
	}

	kept := tags[:0]
	for _, t := range tags {
		if matchesPattern(cfg.GetPattern(), t) {
			kept = append(kept, t)
		}
	}
	natsort.SortDesc(kept)
	if depth := effectiveDepth(cfg); depth > 0 && len(kept) > depth {
		kept = kept[:depth]
	}

	dst, err := name.NewRepository(localRegistryHost + "/" + repo.Namespace + "/" + repo.Name)
	if err != nil {
		return err
	}
	dstOpts := o.dstOpts(ctx, repo.Namespace, repo.Name)

	var errs []error
	synced := 0
	for _, tag := range kept {
		srcDesc, err := remote.Head(src.Tag(tag), srcOpts...)
		if err != nil {
			if errs = append(errs, fmt.Errorf("%s: %w", tag, classifyOCIErr(err))); rateLimited(errs) {
				return errors.Join(errs...)
			}
			continue
		}
		if local, err := remote.Head(dst.Tag(tag), dstOpts...); err == nil && local.Digest == srcDesc.Digest {
			continue
		}
		if err := o.copyTag(src.Tag(tag), dst.Tag(tag), srcOpts, dstOpts); err != nil {
			if errs = append(errs, fmt.Errorf("%s: %w", tag, classifyOCIErr(err))); rateLimited(errs) {
				return errors.Join(errs...)
			}
			continue
		}
		synced++
	}
	if synced > 0 {
		log.Info("mirror synced %d tags into %s/%s from %s", synced, repo.Namespace, repo.Name, src.String())
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// True when the newest collected error is an upstream rate limit
func rateLimited(errs []error) bool {
	if len(errs) == 0 {
		return false
	}
	_, limited := RetryAfter(errs[len(errs)-1])
	return limited
}

// Maps registry 429s onto the shared cooldown error
func classifyOCIErr(err error) error {
	var te *transport.Error
	if errors.As(err, &te) && te.StatusCode == http.StatusTooManyRequests {
		return &rateLimitedError{until: time.Now().Add(currentLimits().RateLimitCooldown)}
	}
	return err
}

// Copies one tag preserving multi arch indexes
func (o *ociSyncer) copyTag(src, dst name.Tag, srcOpts, dstOpts []remote.Option) error {
	desc, err := remote.Get(src, srcOpts...)
	if err != nil {
		return err
	}
	if desc.MediaType.IsIndex() {
		idx, err := desc.ImageIndex()
		if err != nil {
			return err
		}
		return remote.WriteIndex(dst, idx, dstOpts...)
	}
	img, err := desc.Image()
	if err != nil {
		return err
	}
	return remote.Write(dst, img, dstOpts...)
}

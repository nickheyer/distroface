package registry

import (
	"context"
	"net"

	"github.com/distribution/distribution/v3"
	repositorymiddleware "github.com/distribution/distribution/v3/registry/middleware/repository"
	"github.com/distribution/reference"
	"github.com/google/uuid"
	"github.com/opencontainers/go-digest"

	"github.com/nickheyer/distroface/internal/audit"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/webhook"
	"github.com/nickheyer/distroface/pkg/logger"
	"github.com/nickheyer/distroface/pkg/utils"
)

// Deps for the repository middleware, set before handlers.NewApp
var listenerDeps struct {
	store      *stores.Store
	log        *logger.Logger
	dispatcher *webhook.Dispatcher
	recorder   *audit.Recorder
}

// RegisterListenerMiddleware stores the dependencies needed by the
// repository middleware observer. Must be called before handlers.NewApp.
func RegisterListenerMiddleware(store *stores.Store, log *logger.Logger, dispatcher *webhook.Dispatcher, recorder *audit.Recorder) {
	listenerDeps.store = store
	listenerDeps.log = log
	listenerDeps.dispatcher = dispatcher
	listenerDeps.recorder = recorder
}

func init() {
	// Distribution hands middleware the app context, so the repo is
	// wrapped directly and every event uses its per request context
	repositorymiddleware.Register("distroface", func(_ context.Context, repo distribution.Repository, _ map[string]any) (distribution.Repository, error) {
		if listenerDeps.store == nil {
			return repo, nil
		}
		return &observedRepo{Repository: repo, obs: &observer{
			store:      listenerDeps.store,
			log:        listenerDeps.log,
			dispatcher: listenerDeps.dispatcher,
			recorder:   listenerDeps.recorder,
		}}, nil
	})
}

// Emits webhooks, audit rows, and counters for repository events
type observer struct {
	store      *stores.Store
	log        *logger.Logger
	dispatcher *webhook.Dispatcher
	recorder   *audit.Recorder
}

type observedRepo struct {
	distribution.Repository
	obs *observer
}

func (r *observedRepo) Manifests(ctx context.Context, options ...distribution.ManifestServiceOption) (distribution.ManifestService, error) {
	ms, err := r.Repository.Manifests(ctx, options...)
	if err != nil {
		return nil, err
	}
	return &observedManifests{ManifestService: ms, repo: r.Repository.Named(), obs: r.obs}, nil
}

func (r *observedRepo) Tags(ctx context.Context) distribution.TagService {
	return &observedTags{TagService: r.Repository.Tags(ctx), repo: r.Repository.Named(), obs: r.obs}
}

type observedManifests struct {
	distribution.ManifestService
	repo reference.Named
	obs  *observer
}

func (m *observedManifests) Get(ctx context.Context, dgst digest.Digest, options ...distribution.ManifestServiceOption) (distribution.Manifest, error) {
	manifest, err := m.ManifestService.Get(ctx, dgst, options...)
	if err == nil {
		m.obs.manifestPulled(ctx, m.repo, manifest, options...)
	}
	return manifest, err
}

func (m *observedManifests) Put(ctx context.Context, manifest distribution.Manifest, options ...distribution.ManifestServiceOption) (digest.Digest, error) {
	dgst, err := m.ManifestService.Put(ctx, manifest, options...)
	if err == nil {
		m.obs.manifestPushed(ctx, m.repo, manifest, options...)
	}
	return dgst, err
}

func (m *observedManifests) Delete(ctx context.Context, dgst digest.Digest) error {
	err := m.ManifestService.Delete(ctx, dgst)
	if err == nil {
		m.obs.manifestDeleted(ctx, m.repo, dgst)
	}
	return err
}

type observedTags struct {
	distribution.TagService
	repo reference.Named
	obs  *observer
}

func (t *observedTags) Untag(ctx context.Context, tag string) error {
	err := t.TagService.Untag(ctx, tag)
	if err == nil {
		t.obs.tagDeleted(ctx, t.repo, tag)
	}
	return err
}

func (o *observer) manifestPushed(ctx context.Context, repo reference.Named, m distribution.Manifest, options ...distribution.ManifestServiceOption) {
	namespace, name := utils.SplitRepoName(repo.Name())
	if namespace == "" || name == "" {
		return
	}

	r, err := o.store.GetRepository(ctx, namespace, name)
	if err != nil {
		o.log.Error("listener: failed to look up repo %s/%s: %v", namespace, name, err)
		return
	}

	if r == nil {
		ownerID := ""
		isOrgNamespace := false
		user, err := o.store.GetUserByUsername(ctx, namespace)
		if err != nil {
			o.log.Error("listener: failed to look up user %s: %v", namespace, err)
		}
		if user != nil {
			ownerID = user.ID
		} else {
			org, err := o.store.GetOrganization(ctx, namespace)
			if err != nil {
				o.log.Error("listener: failed to look up org %s: %v", namespace, err)
			}
			if org != nil {
				ownerID = org.ID
				isOrgNamespace = true
			}
		}

		r = &storage.Repository{
			ID:             uuid.New().String(),
			Namespace:      namespace,
			Name:           name,
			OwnerID:        ownerID,
			IsOrgNamespace: isOrgNamespace,
		}
		if err := o.store.CreateRepository(ctx, r); err != nil {
			o.log.Error("listener: failed to create repo %s/%s: %v", namespace, name, err)
			return
		}
		o.log.Info("listener: auto-created repository %s/%s", namespace, name)
	}

	if err := o.store.IncrementPushCount(ctx, namespace, name); err != nil {
		o.log.Error("listener: failed to increment push count for %s/%s: %v", namespace, name, err)
	}

	tag := utils.TagFromOptions(options)
	_, dgst := utils.ExtractRef(repo, m)
	if o.dispatcher != nil {
		o.dispatcher.Dispatch(ctx, "push", namespace, name, tag, dgst)
	}
	o.audit(ctx, "push", namespace, name, tag, dgst)
}

func (o *observer) manifestPulled(ctx context.Context, repo reference.Named, m distribution.Manifest, options ...distribution.ManifestServiceOption) {
	namespace, name := utils.SplitRepoName(repo.Name())
	if namespace == "" || name == "" {
		return
	}

	if err := o.store.IncrementPullCount(ctx, namespace, name); err != nil {
		o.log.Error("listener: failed to increment pull count for %s/%s: %v", namespace, name, err)
	}

	tag := utils.TagFromOptions(options)
	_, dgst := utils.ExtractRef(repo, m)
	if o.dispatcher != nil {
		o.dispatcher.Dispatch(ctx, "pull", namespace, name, tag, dgst)
	}
	o.audit(ctx, "pull", namespace, name, tag, dgst)
}

func (o *observer) manifestDeleted(ctx context.Context, repo reference.Named, dgst digest.Digest) {
	namespace, name := utils.SplitRepoName(repo.Name())
	if namespace == "" || name == "" {
		return
	}
	if o.dispatcher != nil {
		o.dispatcher.Dispatch(ctx, "delete", namespace, name, "", dgst.String())
	}
	o.audit(ctx, "delete", namespace, name, "", dgst.String())
}

func (o *observer) tagDeleted(ctx context.Context, repo reference.Named, tag string) {
	namespace, name := utils.SplitRepoName(repo.Name())
	if namespace == "" || name == "" {
		return
	}
	if o.dispatcher != nil {
		o.dispatcher.Dispatch(ctx, "delete", namespace, name, tag, "")
	}
	o.audit(ctx, "delete", namespace, name, tag, "")
}

// Actor and source come from the request scoped auth context
func (o *observer) audit(ctx context.Context, action, namespace, name, tag, dgst string) {
	if o.recorder == nil {
		return
	}
	ref := namespace + "/" + name
	if tag != "" {
		ref += ":" + tag
	}
	if dgst != "" {
		ref += "@" + dgst
	}
	actor, _ := ctx.Value("auth.user.name").(string)
	addr, _ := ctx.Value("http.request.remoteaddr").(string)
	if host, _, err := net.SplitHostPort(addr); err == nil {
		addr = host
	}
	o.recorder.Record(ctx, audit.Event{
		Action:   "Registry/" + action,
		Resource: "registry",
		Outcome:  audit.OutcomeSuccess,
		Detail:   ref,
		SourceIP: addr,
		Actor:    actor,
	})
}

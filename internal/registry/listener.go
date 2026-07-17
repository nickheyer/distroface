package registry

import (
	"context"

	"github.com/distribution/distribution/v3"
	"github.com/distribution/distribution/v3/notifications"
	repositorymiddleware "github.com/distribution/distribution/v3/registry/middleware/repository"
	"github.com/distribution/reference"
	"github.com/google/uuid"
	"github.com/opencontainers/go-digest"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"

	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/webhook"
	"github.com/nickheyer/distroface/pkg/logger"
	"github.com/nickheyer/distroface/pkg/utils"
)

// listenerDeps holds the dependencies needed by the repository middleware listener.
// Set via RegisterListenerMiddleware before handlers.NewApp is called.
var listenerDeps struct {
	store      *stores.Store
	log        *logger.Logger
	dispatcher *webhook.Dispatcher
}

// RegisterListenerMiddleware stores the dependencies needed by the
// repository middleware listener. Must be called before handlers.NewApp.
func RegisterListenerMiddleware(store *stores.Store, log *logger.Logger, dispatcher *webhook.Dispatcher) {
	listenerDeps.store = store
	listenerDeps.log = log
	listenerDeps.dispatcher = dispatcher
}

func init() {
	repositorymiddleware.Register("distroface", func(ctx context.Context, repo distribution.Repository, _ map[string]any) (distribution.Repository, error) {
		if listenerDeps.store == nil {
			return repo, nil
		}
		listener := &registryListener{
			store:      listenerDeps.store,
			log:        listenerDeps.log,
			dispatcher: listenerDeps.dispatcher,
			ctx:        ctx,
		}
		wrapped, _ := notifications.Listen(repo, nil, listener)
		return wrapped, nil
	})
}

// registryListener implements notifications.Listener to handle distribution v3
// repository events directly via the repository middleware system.
type registryListener struct {
	store      *stores.Store
	log        *logger.Logger
	dispatcher *webhook.Dispatcher
	ctx        context.Context
}

var _ notifications.Listener = (*registryListener)(nil)

func (l *registryListener) ManifestPushed(repo reference.Named, m distribution.Manifest, options ...distribution.ManifestServiceOption) error {
	namespace, name := utils.SplitRepoName(repo.Name())
	if namespace == "" || name == "" {
		return nil
	}

	r, err := l.store.GetRepository(l.ctx, namespace, name)
	if err != nil {
		l.log.Error("listener: failed to look up repo %s/%s: %v", namespace, name, err)
		return nil
	}

	if r == nil {
		ownerID := ""
		isOrgNamespace := false
		user, err := l.store.GetUserByUsername(l.ctx, namespace)
		if err != nil {
			l.log.Error("listener: failed to look up user %s: %v", namespace, err)
		}
		if user != nil {
			ownerID = user.ID
		} else {
			org, err := l.store.GetOrganization(l.ctx, namespace)
			if err != nil {
				l.log.Error("listener: failed to look up org %s: %v", namespace, err)
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
		if err := l.store.CreateRepository(l.ctx, r); err != nil {
			l.log.Error("listener: failed to create repo %s/%s: %v", namespace, name, err)
			return nil
		}
		l.log.Info("listener: auto-created repository %s/%s", namespace, name)
	}

	if err := l.store.IncrementPushCount(l.ctx, namespace, name); err != nil {
		l.log.Error("listener: failed to increment push count for %s/%s: %v", namespace, name, err)
	}

	if l.dispatcher != nil {
		tag := utils.TagFromOptions(options)
		_, dgst := utils.ExtractRef(repo, m)
		l.dispatcher.Dispatch(l.ctx, "push", namespace, name, tag, dgst)
	}
	return nil
}

func (l *registryListener) ManifestPulled(repo reference.Named, m distribution.Manifest, options ...distribution.ManifestServiceOption) error {
	namespace, name := utils.SplitRepoName(repo.Name())
	if namespace == "" || name == "" {
		return nil
	}

	if err := l.store.IncrementPullCount(l.ctx, namespace, name); err != nil {
		l.log.Error("listener: failed to increment pull count for %s/%s: %v", namespace, name, err)
	}

	if l.dispatcher != nil {
		tag := utils.TagFromOptions(options)
		_, dgst := utils.ExtractRef(repo, m)
		l.dispatcher.Dispatch(l.ctx, "pull", namespace, name, tag, dgst)
	}
	return nil
}

func (l *registryListener) ManifestDeleted(repo reference.Named, dgst digest.Digest) error {
	if l.dispatcher != nil {
		namespace, name := utils.SplitRepoName(repo.Name())
		if namespace != "" && name != "" {
			l.dispatcher.Dispatch(l.ctx, "delete", namespace, name, "", dgst.String())
		}
	}
	return nil
}

func (l *registryListener) BlobPushed(_ reference.Named, _ v1.Descriptor) error {
	return nil
}

func (l *registryListener) BlobPulled(_ reference.Named, _ v1.Descriptor) error {
	return nil
}

func (l *registryListener) BlobMounted(_ reference.Named, _ v1.Descriptor, _ reference.Named) error {
	return nil
}

func (l *registryListener) BlobDeleted(_ reference.Named, _ digest.Digest) error {
	return nil
}

func (l *registryListener) TagDeleted(repo reference.Named, tag string) error {
	if l.dispatcher != nil {
		namespace, name := utils.SplitRepoName(repo.Name())
		if namespace != "" && name != "" {
			l.dispatcher.Dispatch(l.ctx, "delete", namespace, name, tag, "")
		}
	}
	return nil
}

func (l *registryListener) RepoDeleted(_ reference.Named) error {
	return nil
}

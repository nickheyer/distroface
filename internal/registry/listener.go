package registry

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync/atomic"
	"time"

	"github.com/distribution/distribution/v3"
	"github.com/distribution/distribution/v3/manifest/ocischema"
	"github.com/distribution/distribution/v3/manifest/schema2"
	repositorymiddleware "github.com/distribution/distribution/v3/registry/middleware/repository"
	"github.com/distribution/reference"
	"github.com/google/uuid"
	"github.com/opencontainers/go-digest"

	"github.com/nickheyer/distroface/internal/audit"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/webhook"
	"github.com/nickheyer/distroface/pkg/logger"
	"github.com/nickheyer/distroface/pkg/utils"
)

// Deps for the repository middleware, wired during container setup
type listenerDeps struct {
	store      *stores.Store
	log        *logger.Logger
	dispatcher *webhook.Dispatcher
	recorder   *audit.Recorder
	counters   *repoCounters
	linker     SourceLinker
	images     ImageRecorder
	activity   ActivityRecorder
}

// Swapped in copy-on-write so per request readers never see a torn
// struct, the middleware snapshots the pointer once per repo wrap
var listenerDepsPtr atomic.Pointer[listenerDeps]

// Flips once the middleware first wraps a repo, so dependency sets
// after requests started warn loudly instead of rewiring mid serve
var listenerDepsRead atomic.Bool

// swapListenerDeps applies one field change through a CAS loop
func swapListenerDeps(mutate func(*listenerDeps)) {
	for {
		old := listenerDepsPtr.Load()
		var next *listenerDeps
		if old != nil {
			c := *old
			next = &c
		} else {
			next = &listenerDeps{}
		}
		mutate(next)
		if listenerDepsPtr.CompareAndSwap(old, next) {
			if old != nil && listenerDepsRead.Load() {
				if log := next.log; log != nil {
					log.Warn("listener: dependency wired after requests started wrapping repositories")
				}
			}
			return
		}
	}
}

// Links an image repo to the git repo its source label names
type SourceLinker interface {
	LinkImageBySource(ctx context.Context, img *storage.Repository, source string)
}

// Records a tag pushed with a job token in a workflow
type ImageRecorder interface {
	RecordJobImage(ctx context.Context, jobID string, img *storage.Repository, tag, digest string)
}

// Run attribution of job token pushes
func SetImageRecorder(r ImageRecorder) {
	swapListenerDeps(func(d *listenerDeps) { d.images = r })
}

// Feeds tag pushes into the activity stream
type ActivityRecorder interface {
	ImagePushed(ctx context.Context, repo *storage.Repository, actor, tag, digest string)
}

// SetActivityRecorder installs the activity feed hook
func SetActivityRecorder(r ActivityRecorder) {
	swapListenerDeps(func(d *listenerDeps) { d.activity = r })
}

// SetSourceLinker enables linking by the opencontainers source label
func SetSourceLinker(l SourceLinker) {
	swapListenerDeps(func(d *listenerDeps) { d.linker = l })
}

// RegisterListenerMiddleware stores the dependencies needed by the
// repository middleware observer. Must be called before handlers.NewApp.
func RegisterListenerMiddleware(store *stores.Store, log *logger.Logger, dispatcher *webhook.Dispatcher, recorder *audit.Recorder) {
	counters := newRepoCounters(store, log, counterFlushInterval)
	var previous *repoCounters
	swapListenerDeps(func(d *listenerDeps) {
		previous = d.counters
		d.store = store
		d.log = log
		d.dispatcher = dispatcher
		d.recorder = recorder
		d.counters = counters
	})
	previous.Close()
}

func init() {
	// Distribution hands middleware the app context, so the repo is
	// wrapped directly and every event uses its per request context
	err := repositorymiddleware.Register("distroface", func(_ context.Context, repo distribution.Repository, _ map[string]any) (distribution.Repository, error) {
		deps := listenerDepsPtr.Load()
		if deps == nil || deps.store == nil {
			return repo, nil
		}
		listenerDepsRead.Store(true)
		return &observedRepo{Repository: repo, obs: &observer{
			store:      deps.store,
			log:        deps.log,
			dispatcher: deps.dispatcher,
			recorder:   deps.recorder,
			counters:   deps.counters,
			linker:     deps.linker,
			images:     deps.images,
			activity:   deps.activity,
		}}, nil
	})
	if err != nil {
		panic(err)
	}
}

// Bounded worker pool for side effects that must not stall a registry
// response: webhook dispatch, audit rows, activity, job image records
type eventQueue struct {
	tasks chan func()
	drops atomic.Int64
	last  atomic.Int64
}

const (
	queueWorkers = 32
	queueDepth   = 512
)

// Package level like the middleware itself, alive for the process
var events = newEventQueue(queueWorkers, queueDepth)

func newEventQueue(workers, depth int) *eventQueue {
	q := &eventQueue{tasks: make(chan func(), depth)}
	for range workers {
		go func() {
			for task := range q.tasks {
				task()
			}
		}()
	}
	return q
}

// Never blocks a registry response; overflow is dropped and counted,
// sampled into the log so a saturated queue is visible in metrics
func (q *eventQueue) enqueue(task func()) {
	select {
	case q.tasks <- task:
	default:
		n := q.drops.Add(1)
		now := time.Now().Unix()
		if prev := q.last.Load(); now-prev > 30 && q.last.CompareAndSwap(prev, now) {
			if deps := listenerDepsPtr.Load(); deps != nil && deps.log != nil {
				deps.log.Warn("listener: event queue full, %d events dropped so far", n)
			}
		}
	}
}

// Emits webhooks, audit rows, and counters for repository events
type observer struct {
	store      *stores.Store
	log        *logger.Logger
	dispatcher *webhook.Dispatcher
	recorder   *audit.Recorder
	counters   *repoCounters
	linker     SourceLinker
	images     ImageRecorder
	activity   ActivityRecorder
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
	return &observedManifests{ManifestService: ms, dist: r.Repository, repo: r.Named(), obs: r.obs}, nil
}

func (r *observedRepo) Tags(ctx context.Context) distribution.TagService {
	return &observedTags{TagService: r.Repository.Tags(ctx), repo: r.Named(), obs: r.obs}
}

type observedManifests struct {
	distribution.ManifestService
	dist distribution.Repository
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
		m.obs.manifestPushed(ctx, m.dist, m.repo, manifest, options...)
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

func (o *observer) manifestPushed(ctx context.Context, dist distribution.Repository, repo reference.Named, m distribution.Manifest, options ...distribution.ManifestServiceOption) {
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
		// Packages named after a git repository link to it and follow its visibility
		first := name
		if i := strings.IndexByte(first, '/'); i > 0 {
			first = first[:i]
		}
		if src, _ := o.store.GetGitRepository(ctx, namespace, first); src != nil {
			r.GitRepoID = &src.ID
			r.IsPrivate = src.IsPrivate
		}
		if err := o.store.CreateRepository(ctx, r); err != nil {
			o.log.Error("listener: failed to create repo %s/%s: %v", namespace, name, err)
			return
		}
		o.log.Info("listener: auto-created repository %s/%s", namespace, name)
		// Only a package this push creates follows its label, existing ones link by hand
		if r.GitRepoID == nil && o.linker != nil && dist != nil {
			if source := imageSourceLabel(ctx, dist, m); source != "" {
				o.linker.LinkImageBySource(ctx, r, source)
			}
		}
	}

	o.counters.pushed(namespace, name)

	tag := utils.TagFromOptions(options)
	_, dgst := utils.ExtractRef(repo, m)
	// Side effects run off the response path; the request context is
	// detached first so shutdown cannot cancel in flight work
	actor := subjectOf(ctx)
	background := context.WithoutCancel(ctx)
	events.enqueue(func() {
		if o.dispatcher != nil {
			o.dispatcher.Dispatch(background, "push", namespace, name, tag, dgst)
		}
		o.audit(background, "push", namespace, name, tag, dgst)
		if o.activity != nil {
			o.activity.ImagePushed(background, r, actor, tag, dgst)
		}
		if tag != "" && o.images != nil {
			if jobID, _, ok := auth.ParseJobSubject(actor); ok {
				o.images.RecordJobImage(background, jobID, r, tag, dgst)
			}
		}
	})
}

// Get subject of request, from its token
func subjectOf(ctx context.Context) string {
	actor, _ := ctx.Value("auth.user.name").(string)
	return actor
}

// Source label from the image config, empty when absent or oversized
func imageSourceLabel(ctx context.Context, dist distribution.Repository, m distribution.Manifest) string {
	var cfg distribution.Descriptor
	switch mm := m.(type) {
	case *ocischema.DeserializedManifest:
		cfg = mm.Config
	case *schema2.DeserializedManifest:
		cfg = mm.Config
	default:
		return ""
	}
	if cfg.Digest == "" || cfg.Size > 1<<20 {
		return ""
	}
	raw, err := dist.Blobs(ctx).Get(ctx, cfg.Digest)
	if err != nil {
		return ""
	}
	var parsed struct {
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"config"`
	}
	if json.Unmarshal(raw, &parsed) != nil {
		return ""
	}
	return strings.TrimSpace(parsed.Config.Labels["org.opencontainers.image.source"])
}

func (o *observer) manifestPulled(ctx context.Context, repo reference.Named, m distribution.Manifest, options ...distribution.ManifestServiceOption) {
	namespace, name := utils.SplitRepoName(repo.Name())
	if namespace == "" || name == "" {
		return
	}

	o.counters.pulled(namespace, name)

	tag := utils.TagFromOptions(options)
	_, dgst := utils.ExtractRef(repo, m)
	background := context.WithoutCancel(ctx)
	events.enqueue(func() {
		if o.dispatcher != nil {
			o.dispatcher.Dispatch(background, "pull", namespace, name, tag, dgst)
		}
		o.audit(background, "pull", namespace, name, tag, dgst)
	})
}

func (o *observer) manifestDeleted(ctx context.Context, repo reference.Named, dgst digest.Digest) {
	namespace, name := utils.SplitRepoName(repo.Name())
	if namespace == "" || name == "" {
		return
	}
	background := context.WithoutCancel(ctx)
	events.enqueue(func() {
		if o.dispatcher != nil {
			o.dispatcher.Dispatch(background, "delete", namespace, name, "", dgst.String())
		}
		o.audit(background, "delete", namespace, name, "", dgst.String())
	})
}

func (o *observer) tagDeleted(ctx context.Context, repo reference.Named, tag string) {
	namespace, name := utils.SplitRepoName(repo.Name())
	if namespace == "" || name == "" {
		return
	}
	background := context.WithoutCancel(ctx)
	events.enqueue(func() {
		if o.dispatcher != nil {
			o.dispatcher.Dispatch(background, "delete", namespace, name, tag, "")
		}
		o.audit(background, "delete", namespace, name, tag, "")
	})
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
	_, actor, _ := auth.ParseJobSubject(subjectOf(ctx))
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

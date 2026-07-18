package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	mrand "math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"github.com/nickheyer/distroface/internal/rbac"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
)

const (
	phaseRoles         = "roles"
	phaseUsers         = "users"
	phaseOrgs          = "orgs"
	phaseInvites       = "invites"
	phaseImages        = "images"
	phasePulls         = "pulls"
	phaseStars         = "stars"
	phaseTokens        = "tokens"
	phaseWebhooks      = "webhooks"
	phaseArtifactRepos = "artifact-repos"
	phaseArtifacts     = "artifacts"
	phasePortals       = "portals"

	mtOCIManifest    = "application/vnd.oci.image.manifest.v1+json"
	mtOCIConfig      = "application/vnd.oci.image.config.v1+json"
	mtOCILayer       = "application/vnd.oci.image.layer.v1.tar+gzip"
	mtOCIIndex       = "application/vnd.oci.image.index.v1+json"
	mtDockerManifest = "application/vnd.docker.distribution.manifest.v2+json"
	mtDockerConfig   = "application/vnd.docker.container.image.v1+json"
	mtDockerLayer    = "application/vnd.docker.image.rootfs.diff.tar.gzip"
	mtDockerList     = "application/vnd.docker.distribution.manifest.list.v2+json"

	manifestAccept = mtOCIManifest + ", " + mtOCIIndex + ", " + mtDockerManifest + ", " + mtDockerList
)

type Config struct {
	BaseURL     string
	Username    string
	Password    string
	Concurrency int
	Timeout     time.Duration
	Retries     int

	NamePrefix       string
	ProgressInterval time.Duration
	FailFast         bool
	Phases           string
	HealthTimeout    time.Duration
	MaxErrorSamples  int

	Roles      int
	Users      int
	Orgs       int
	OrgMembers int
	Invites    int

	ImageRepos  int
	TagsPerRepo int
	Pulls       int
	Stars       int
	Tokens      int
	Webhooks    int

	ArtifactRepos int
	Artifacts     int
	Portals       int

	LayerSize       int
	ArtifactSize    int
	WebhookURL      string
	WebhooksActive  bool
	RegistryService string
	PortalPorts     string

	portalPorts []int
}

func envKey(name string) string {
	return "SEED_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
}

func envStr(name, def string) string {
	if v := os.Getenv(envKey(name)); v != "" {
		return v
	}
	return def
}

func envInt(name string, def int) int {
	if v := os.Getenv(envKey(name)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envBool(name string, def bool) bool {
	if v := os.Getenv(envKey(name)); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

func envDur(name string, def time.Duration) time.Duration {
	if v := os.Getenv(envKey(name)); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func loadConfig() *Config {
	cfg := &Config{}
	flag.StringVar(&cfg.BaseURL, "base-url", envStr("base-url", "http://localhost:8080"), "target server base URL")
	flag.StringVar(&cfg.Username, "username", envStr("username", "admin"), "admin username")
	flag.StringVar(&cfg.Password, "password", envStr("password", ""), "admin password (required)")
	flag.IntVar(&cfg.Concurrency, "concurrency", envInt("concurrency", min(max(runtime.NumCPU()*2, 8), 32)), "worker pool size per phase")
	flag.DurationVar(&cfg.Timeout, "timeout", envDur("timeout", 60*time.Second), "HTTP request timeout")
	flag.IntVar(&cfg.Retries, "retries", envInt("retries", 3), "retries on transient failures")

	flag.StringVar(&cfg.NamePrefix, "name-prefix", envStr("name-prefix", "seed"), "created entity name prefix (lowercase)")
	flag.DurationVar(&cfg.ProgressInterval, "progress-interval", envDur("progress-interval", 3*time.Second), "progress update interval")
	flag.BoolVar(&cfg.FailFast, "fail-fast", envBool("fail-fast", false), "abort phase on first error")
	flag.StringVar(&cfg.Phases, "phases", envStr("phases", ""), "comma separated phase allowlist, empty runs all")
	flag.DurationVar(&cfg.HealthTimeout, "health-timeout", envDur("health-timeout", 60*time.Second), "max health wait before aborting")
	flag.IntVar(&cfg.MaxErrorSamples, "max-error-samples", envInt("max-error-samples", 5), "errors printed per phase before suppression")

	flag.IntVar(&cfg.Roles, "roles", envInt("roles", 25), "role count")
	flag.IntVar(&cfg.Users, "users", envInt("users", 300), "user count")
	flag.IntVar(&cfg.Orgs, "orgs", envInt("orgs", 40), "organization count")
	flag.IntVar(&cfg.OrgMembers, "org-members", envInt("org-members", 5), "members added per organization")
	flag.IntVar(&cfg.Invites, "invites", envInt("invites", 150), "registration invite count")

	flag.IntVar(&cfg.ImageRepos, "image-repos", envInt("image-repos", 120), "docker repository count")
	flag.IntVar(&cfg.TagsPerRepo, "tags-per-repo", envInt("tags-per-repo", 5), "image tags pushed per repository")
	flag.IntVar(&cfg.Pulls, "pulls", envInt("pulls", 800), "manifest pull count")
	flag.IntVar(&cfg.Stars, "stars", envInt("stars", 800), "repository star count")
	flag.IntVar(&cfg.Tokens, "tokens", envInt("tokens", 400), "API token count")
	flag.IntVar(&cfg.Webhooks, "webhooks", envInt("webhooks", 150), "webhook count")

	flag.IntVar(&cfg.ArtifactRepos, "artifact-repos", envInt("artifact-repos", 60), "artifact repository count")
	flag.IntVar(&cfg.Artifacts, "artifacts", envInt("artifacts", 1500), "uploaded artifact count")
	flag.IntVar(&cfg.Portals, "portals", envInt("portals", 20), "registry portal count")

	flag.IntVar(&cfg.LayerSize, "layer-size", envInt("layer-size", 2048), "image layer size in bytes")
	flag.IntVar(&cfg.ArtifactSize, "artifact-size", envInt("artifact-size", 2048), "artifact payload size in bytes")
	flag.StringVar(&cfg.WebhookURL, "webhook-url", envStr("webhook-url", "http://127.0.0.1:9/seed-hook"), "webhook target URL")
	flag.BoolVar(&cfg.WebhooksActive, "webhooks-active", envBool("webhooks-active", false), "create webhooks in active state")
	flag.StringVar(&cfg.RegistryService, "registry-service", envStr("registry-service", "distroface-registry"), "registry token service name")
	flag.StringVar(&cfg.PortalPorts, "portal-ports", envStr("portal-ports", "15181,15182,15183,15184,15185"), "portal listener port pool, empty keeps portals on the app port")
	flag.Parse()

	for p := range strings.SplitSeq(cfg.PortalPorts, ",") {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			fmt.Fprintf(os.Stderr, "error: bad portal port %q\n", p)
			os.Exit(2)
		}
		cfg.portalPorts = append(cfg.portalPorts, n)
	}
	return cfg
}

func (cfg *Config) phaseEnabled(name string) bool {
	if strings.TrimSpace(cfg.Phases) == "" {
		return true
	}
	for p := range strings.SplitSeq(cfg.Phases, ",") {
		if strings.TrimSpace(p) == name {
			return true
		}
	}
	return false
}

func (cfg *Config) printPlan() {
	fmt.Printf(`Plan:
  target          %s (user=%s)
  concurrency     %d   retries=%d   timeout=%s
  name prefix     %q

  config:
    roles                 %d
    users                 %d
    organizations         %d  (members-per-org=%d)
    invites               %d

  data:
    image repos           %d  x %d tags (layer=%dB)
    manifest pulls        %d
    stars                 %d
    api tokens            %d
    webhooks              %d  (active=%v)
    artifact repos        %d
    artifacts             %d  (size=%dB)
    portals               %d
`,
		cfg.BaseURL, cfg.Username,
		cfg.Concurrency, cfg.Retries, cfg.Timeout,
		cfg.NamePrefix,
		cfg.Roles, cfg.Users, cfg.Orgs, cfg.OrgMembers, cfg.Invites,
		cfg.ImageRepos, cfg.TagsPerRepo, cfg.LayerSize,
		cfg.Pulls, cfg.Stars, cfg.Tokens, cfg.Webhooks, cfg.WebhooksActive,
		cfg.ArtifactRepos, cfg.Artifacts, cfg.ArtifactSize, cfg.Portals,
	)
}

func main() {
	cfg := loadConfig()
	if cfg.Password == "" {
		fmt.Fprintln(os.Stderr, "error: --password (or SEED_PASSWORD) is required")
		os.Exit(2)
	}
	cfg.printPlan()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	c, err := newClient(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}

	fmt.Printf("waiting up to %s for %s …\n", cfg.HealthTimeout, cfg.BaseURL)
	if err := c.waitHealthy(ctx, cfg.HealthTimeout); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Println("server healthy")

	adminTok, err := c.login(ctx, cfg.Username, cfg.Password)
	if err != nil {
		fmt.Fprintln(os.Stderr, "login error:", err)
		os.Exit(1)
	}
	c.adminToken.Store(adminTok)
	fmt.Println("seeding")

	runID := make([]byte, 4)
	_, _ = rand.Read(runID)
	s := &seeder{
		cfg:      cfg,
		client:   c,
		runID:    hex.EncodeToString(runID),
		sessions: map[string]*userSession{},
	}
	start := time.Now()
	if err := s.run(ctx); err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "\ninterrupted, stopping")
			os.Exit(130)
		}
		fmt.Fprintln(os.Stderr, "\nseed aborted:", err)
		os.Exit(1)
	}
	fmt.Printf("\nall phases complete in %s\n", time.Since(start).Round(time.Second))
}

type roleRef struct{ id, name string }
type userRef struct{ id, username, password string }
type orgRef struct{ id, name string }
type repoRef struct{ id, namespace, name string }
type artifactRepoRef struct{ namespace, name string }

// One seeding target, index 0 is the base app
type regEntry struct {
	base string
	host string
	org  string
}

type imageRef struct {
	entry     int
	repoPath  string
	namespace string
	name      string
	tag       string
}

type userSession struct {
	once  sync.Once
	token string
	err   error
}

type seeder struct {
	cfg    *Config
	client *client
	runID  string

	roles      []roleRef
	memberRole roleRef
	users      []userRef
	orgs       []orgRef

	imagesMu      sync.Mutex
	images        []imageRef
	dockerRepos   []repoRef
	artifactRepos []artifactRepoRef

	regEntries       []regEntry
	orgArtifactRepos map[string][]artifactRepoRef

	sessMu   sync.Mutex
	sessions map[string]*userSession
}

func (s *seeder) run(ctx context.Context) error {
	cfg := s.cfg
	var err error

	fmt.Println("\nconfig...")
	if cfg.phaseEnabled(phaseRoles) && cfg.Roles > 0 {
		if s.roles, err = collect(ctx, s, phaseRoles, cfg.Roles, s.createRole); err != nil {
			return err
		}
	}
	if cfg.phaseEnabled(phaseUsers) && cfg.Users > 0 {
		if err = s.ensureMemberRole(ctx); err != nil {
			return fmt.Errorf("create member role: %w", err)
		}
		if s.users, err = collect(ctx, s, phaseUsers, cfg.Users, s.createUser); err != nil {
			return err
		}
	}
	if cfg.phaseEnabled(phaseOrgs) && cfg.Orgs > 0 {
		if s.orgs, err = collect(ctx, s, phaseOrgs, cfg.Orgs, s.createOrg); err != nil {
			return err
		}
	}
	if cfg.phaseEnabled(phaseInvites) && cfg.Invites > 0 {
		if err = runPhase(ctx, cfg, phaseInvites, cfg.Invites, s.createInvite); err != nil {
			return err
		}
	}

	fmt.Println("\ndata...")
	if cfg.phaseEnabled(phaseImages) && cfg.ImageRepos > 0 && cfg.TagsPerRepo > 0 {
		if err = s.ensurePushPortals(ctx); err != nil {
			return fmt.Errorf("create push portals: %w", err)
		}
		if err = runPhase(ctx, cfg, phaseImages, cfg.ImageRepos*cfg.TagsPerRepo, s.pushImageTag); err != nil {
			return err
		}
		if err = s.loadDockerRepos(ctx); err != nil {
			return fmt.Errorf("list repositories: %w", err)
		}
	}
	if cfg.phaseEnabled(phasePulls) && cfg.Pulls > 0 {
		if err = requirePools(phasePulls, map[string]int{"pushed images": len(s.images)}); err != nil {
			return err
		}
		if err = runPhase(ctx, cfg, phasePulls, cfg.Pulls, s.pullImage); err != nil {
			return err
		}
	}
	if cfg.phaseEnabled(phaseStars) && cfg.Stars > 0 {
		if err = requirePools(phaseStars, map[string]int{"users": len(s.users), "pushed images": len(s.images)}); err != nil {
			return err
		}
		if err = runPhase(ctx, cfg, phaseStars, cfg.Stars, s.starRepo); err != nil {
			return err
		}
	}
	if cfg.phaseEnabled(phaseTokens) && cfg.Tokens > 0 {
		if err = requirePools(phaseTokens, map[string]int{"users": len(s.users)}); err != nil {
			return err
		}
		if err = runPhase(ctx, cfg, phaseTokens, cfg.Tokens, s.createToken); err != nil {
			return err
		}
	}
	if cfg.phaseEnabled(phaseWebhooks) && cfg.Webhooks > 0 {
		if err = requirePools(phaseWebhooks, map[string]int{"repos or orgs": len(s.dockerRepos) + len(s.orgs)}); err != nil {
			return err
		}
		if err = runPhase(ctx, cfg, phaseWebhooks, cfg.Webhooks, s.createWebhook); err != nil {
			return err
		}
	}
	if cfg.phaseEnabled(phaseArtifactRepos) && cfg.ArtifactRepos > 0 {
		if s.artifactRepos, err = collect(ctx, s, phaseArtifactRepos, cfg.ArtifactRepos, s.createArtifactRepo); err != nil {
			return err
		}
	}
	if cfg.phaseEnabled(phaseArtifacts) && cfg.Artifacts > 0 {
		if err = requirePools(phaseArtifacts, map[string]int{"artifact repos": len(s.artifactRepos)}); err != nil {
			return err
		}
		if err = s.ensurePushPortals(ctx); err != nil {
			return fmt.Errorf("create push portals: %w", err)
		}
		s.orgArtifactRepos = map[string][]artifactRepoRef{}
		for _, r := range s.artifactRepos {
			s.orgArtifactRepos[r.namespace] = append(s.orgArtifactRepos[r.namespace], r)
		}
		if err = runPhase(ctx, cfg, phaseArtifacts, cfg.Artifacts, s.uploadArtifact); err != nil {
			return err
		}
	}
	if cfg.phaseEnabled(phasePortals) && cfg.Portals > 0 {
		if err = requirePools(phasePortals, map[string]int{"orgs": len(s.orgs)}); err != nil {
			return err
		}
		if err = runPhase(ctx, cfg, phasePortals, cfg.Portals, s.createPortal); err != nil {
			return err
		}
	}
	return nil
}

func (s *seeder) name(kind string, i int) string {
	return fmt.Sprintf("%s-%s-%s-%d", s.cfg.NamePrefix, kind, s.runID, i)
}

func sample[T any](xs []T) T {
	return xs[mrand.IntN(len(xs))]
}

func ptr[T any](v T) *T { return &v }

// ── config tier ──────────────────────────────────────────────────────────

func randomPerms() []*v1.Permission {
	var out []*v1.Permission
	for _, e := range rbac.ResourceActions {
		if mrand.IntN(2) == 0 {
			continue
		}
		for _, a := range e.Actions {
			if mrand.IntN(2) == 0 {
				continue
			}
			out = append(out, &v1.Permission{Resource: e.Resource, Action: a})
		}
	}
	if len(out) == 0 {
		out = append(out, &v1.Permission{Resource: rbac.ResourceRepositories, Action: rbac.ActionRead})
	}
	return out
}

func (s *seeder) createRole(ctx context.Context, i int) (roleRef, error) {
	resp, err := retry(ctx, s.cfg, func(ctx context.Context) (*connect.Response[v1.CreateRoleResponse], error) {
		return s.client.roles.CreateRole(ctx, connect.NewRequest(&v1.CreateRoleRequest{
			Name:        s.name("role", i),
			Description: "seeder generated role",
			Permissions: randomPerms(),
		}))
	})
	if err != nil {
		return roleRef{}, err
	}
	return roleRef{id: resp.Msg.Role.GetId(), name: resp.Msg.Role.GetName()}, nil
}

// Grants seeded users enough access for later phases
func (s *seeder) ensureMemberRole(ctx context.Context) error {
	resp, err := retry(ctx, s.cfg, func(ctx context.Context) (*connect.Response[v1.CreateRoleResponse], error) {
		return s.client.roles.CreateRole(ctx, connect.NewRequest(&v1.CreateRoleRequest{
			Name:        fmt.Sprintf("%s-member-%s", s.cfg.NamePrefix, s.runID),
			Description: "seeder member baseline role",
			Permissions: []*v1.Permission{
				{Resource: rbac.ResourceRepositories, Action: rbac.ActionRead},
				{Resource: rbac.ResourceRepositories, Action: rbac.ActionPull},
				{Resource: rbac.ResourceArtifacts, Action: rbac.ActionRead},
				{Resource: rbac.ResourceArtifacts, Action: rbac.ActionPull},
				{Resource: rbac.ResourceTokens, Action: rbac.ActionRead},
				{Resource: rbac.ResourceTokens, Action: rbac.ActionCreate},
				{Resource: rbac.ResourceTokens, Action: rbac.ActionDelete},
				{Resource: rbac.ResourceOrganizations, Action: rbac.ActionRead},
				{Resource: rbac.ResourceWebhooks, Action: rbac.ActionRead},
			},
		}))
	})
	if err != nil {
		return err
	}
	s.memberRole = roleRef{id: resp.Msg.Role.GetId(), name: resp.Msg.Role.GetName()}
	return nil
}

func (s *seeder) createUser(ctx context.Context, i int) (userRef, error) {
	username := s.name("user", i)
	password := "seedpass-" + s.runID
	roleIDs := []string{s.memberRole.id}
	if len(s.roles) > 0 && mrand.IntN(2) == 0 {
		roleIDs = append(roleIDs, sample(s.roles).id)
	}
	resp, err := retry(ctx, s.cfg, func(ctx context.Context) (*connect.Response[v1.AdminCreateUserResponse], error) {
		return s.client.users.AdminCreateUser(ctx, connect.NewRequest(&v1.AdminCreateUserRequest{
			Username:    username,
			Password:    password,
			Email:       username + "@seed.invalid",
			DisplayName: fmt.Sprintf("Seed User %d", i),
			RoleIds:     roleIDs,
		}))
	})
	if err != nil {
		return userRef{}, err
	}
	return userRef{id: resp.Msg.User.GetId(), username: username, password: password}, nil
}

func (s *seeder) createOrg(ctx context.Context, i int) (orgRef, error) {
	resp, err := retry(ctx, s.cfg, func(ctx context.Context) (*connect.Response[v1.CreateOrganizationResponse], error) {
		return s.client.orgs.CreateOrganization(ctx, connect.NewRequest(&v1.CreateOrganizationRequest{
			Name:        s.name("org", i),
			DisplayName: fmt.Sprintf("Seed Org %d", i),
			Description: "seeder generated organization",
		}))
	})
	if err != nil {
		return orgRef{}, err
	}
	org := orgRef{id: resp.Msg.Organization.GetId(), name: resp.Msg.Organization.GetName()}

	n := min(s.cfg.OrgMembers, len(s.users))
	for j, idx := range mrand.Perm(len(s.users))[:n] {
		role := v1.OrgRole_ORG_ROLE_MEMBER
		if j == 0 {
			role = v1.OrgRole_ORG_ROLE_ADMIN
		}
		u := s.users[idx]
		_, err := retry(ctx, s.cfg, func(ctx context.Context) (*connect.Response[v1.AddOrgMemberResponse], error) {
			return s.client.orgs.AddOrgMember(ctx, connect.NewRequest(&v1.AddOrgMemberRequest{
				OrgId:  org.id,
				UserId: u.id,
				Role:   role,
			}))
		})
		if err != nil && connect.CodeOf(err) != connect.CodeAlreadyExists {
			return orgRef{}, fmt.Errorf("add member %s: %w", u.username, err)
		}
	}
	return org, nil
}

func (s *seeder) createInvite(ctx context.Context, i int) error {
	req := &v1.CreateInviteRequest{
		Description:    fmt.Sprintf("seeder invite %s-%d", s.runID, i),
		MaxUses:        ptr(int32(1 + mrand.IntN(5))),
		ExpiresInHours: ptr(int32(1 + mrand.IntN(720))),
	}
	if len(s.roles) > 0 {
		req.RoleIds = []string{sample(s.roles).id}
	}
	if i%3 == 0 {
		req.Pin = ptr("1234")
	}
	_, err := retry(ctx, s.cfg, func(ctx context.Context) (*connect.Response[v1.CreateInviteResponse], error) {
		return s.client.auth.CreateInvite(ctx, connect.NewRequest(req))
	})
	return err
}

// ── data tier ────────────────────────────────────────────────────────────

// One hostname portal per pool port, all pushable seeding targets
func (s *seeder) ensurePushPortals(ctx context.Context) error {
	if s.regEntries != nil {
		return nil
	}
	s.regEntries = []regEntry{{base: s.client.baseURL}}
	if len(s.cfg.portalPorts) == 0 || len(s.orgs) == 0 {
		return nil
	}
	for k, port := range s.cfg.portalPorts {
		org := s.orgs[k%len(s.orgs)]
		hostname := fmt.Sprintf("%s-push-%s-%d.portal.invalid", s.cfg.NamePrefix, s.runID, k)
		req := &v1.CreatePortalRequest{
			OrgId:       org.id,
			Name:        fmt.Sprintf("%s-push-%s-%d", s.cfg.NamePrefix, s.runID, k),
			Hostname:    hostname,
			Port:        int32(port),
			AllowPush:   true,
			RequireAuth: k%2 == 1,
		}
		// Alternate builtin unqualified mapping and custom rules
		if k%2 == 0 {
			req.MapUnqualified = true
		} else {
			req.Rules = []*v1.PortalRule{{Pattern: "([^/]+)", Replace: org.name + "/$1"}}
		}
		if _, err := retry(ctx, s.cfg, func(ctx context.Context) (*connect.Response[v1.CreatePortalResponse], error) {
			return s.client.portals.CreatePortal(ctx, connect.NewRequest(req))
		}); err != nil {
			return fmt.Errorf("portal on port %d: %w", port, err)
		}
		s.regEntries = append(s.regEntries, regEntry{
			base: fmt.Sprintf("%s://%s:%d", s.client.base.Scheme, s.client.base.Hostname(), port),
			host: hostname,
			org:  org.name,
		})
	}
	fmt.Printf("    seeding targets: base app + %d push portals\n", len(s.regEntries)-1)
	return nil
}

func (s *seeder) pushImageTag(ctx context.Context, i int) error {
	repoIdx := i / s.cfg.TagsPerRepo
	name := s.name("img", repoIdx)
	tag := fmt.Sprintf("v1.0.%d", i%s.cfg.TagsPerRepo)

	// Stable per repo so every tag lands in one namespace
	entryIdx := int(mrand.New(mrand.NewPCG(uint64(repoIdx), 42)).IntN(len(s.regEntries)))
	entry := s.regEntries[entryIdx]
	ns, repoPath := entry.org, name
	if entryIdx == 0 {
		ns = s.cfg.Username
		if len(s.orgs) > 0 && repoIdx%2 == 1 {
			ns = s.orgs[repoIdx%len(s.orgs)].name
		}
		repoPath = ns + "/" + name
	}
	if err := s.client.pushImage(ctx, entry, repoPath, tag, s.cfg.LayerSize, i); err != nil {
		return fmt.Errorf("via %s: %w", entryLabel(entry), err)
	}
	s.imagesMu.Lock()
	s.images = append(s.images, imageRef{entry: entryIdx, repoPath: repoPath, namespace: ns, name: name, tag: tag})
	s.imagesMu.Unlock()
	return nil
}

func entryLabel(e regEntry) string {
	if e.host == "" {
		return e.base
	}
	return e.host
}

func (s *seeder) pullImage(ctx context.Context, _ int) error {
	img := sample(s.images)
	return s.client.pullManifest(ctx, s.regEntries[img.entry], img.repoPath, img.tag)
}

func (s *seeder) starRepo(ctx context.Context, i int) error {
	u := s.users[i%len(s.users)]
	tok, err := s.sessionFor(ctx, u)
	if err != nil {
		return fmt.Errorf("login %s: %w", u.username, err)
	}
	img := sample(s.images)
	_, err = retry(withToken(ctx, tok), s.cfg, func(ctx context.Context) (*connect.Response[v1.StarRepositoryResponse], error) {
		return s.client.repos.StarRepository(ctx, connect.NewRequest(&v1.StarRepositoryRequest{
			Namespace: img.namespace,
			Name:      img.name,
		}))
	})
	if connect.CodeOf(err) == connect.CodeAlreadyExists {
		return nil
	}
	return err
}

func (s *seeder) createToken(ctx context.Context, i int) error {
	u := s.users[i%len(s.users)]
	tok, err := s.sessionFor(ctx, u)
	if err != nil {
		return fmt.Errorf("login %s: %w", u.username, err)
	}
	_, err = retry(withToken(ctx, tok), s.cfg, func(ctx context.Context) (*connect.Response[v1.CreateAPITokenResponse], error) {
		return s.client.tokens.CreateAPIToken(ctx, connect.NewRequest(&v1.CreateAPITokenRequest{
			Name:          s.name("token", i),
			ExpiresInDays: ptr(int32(1 + mrand.IntN(90))),
		}))
	})
	return err
}

func randomEvents() []v1.WebhookEvent {
	all := []v1.WebhookEvent{v1.WebhookEvent_WEBHOOK_EVENT_PUSH, v1.WebhookEvent_WEBHOOK_EVENT_PULL, v1.WebhookEvent_WEBHOOK_EVENT_DELETE}
	var out []v1.WebhookEvent
	for _, e := range all {
		if mrand.IntN(2) == 0 {
			out = append(out, e)
		}
	}
	if len(out) == 0 {
		out = append(out, v1.WebhookEvent_WEBHOOK_EVENT_PUSH)
	}
	return out
}

func (s *seeder) createWebhook(ctx context.Context, i int) error {
	req := &v1.CreateWebhookRequest{
		Url:         s.cfg.WebhookURL,
		Secret:      "seed-secret-" + s.runID,
		Events:      randomEvents(),
		Active:      s.cfg.WebhooksActive,
		ContentType: "application/json",
	}
	if len(s.dockerRepos) > 0 && (i%2 == 0 || len(s.orgs) == 0) {
		req.Scope = v1.WebhookScope_WEBHOOK_SCOPE_REPOSITORY
		req.RepoId = sample(s.dockerRepos).id
	} else {
		req.Scope = v1.WebhookScope_WEBHOOK_SCOPE_ORGANIZATION
		req.OrgId = sample(s.orgs).id
	}
	_, err := retry(ctx, s.cfg, func(ctx context.Context) (*connect.Response[v1.CreateWebhookResponse], error) {
		return s.client.webhooks.CreateWebhook(ctx, connect.NewRequest(req))
	})
	return err
}

func (s *seeder) createArtifactRepo(ctx context.Context, i int) (artifactRepoRef, error) {
	ns := ""
	if len(s.orgs) > 0 && i%2 == 1 {
		ns = s.orgs[(i/2)%len(s.orgs)].name
	}
	resp, err := retry(ctx, s.cfg, func(ctx context.Context) (*connect.Response[v1.CreateArtifactRepositoryResponse], error) {
		return s.client.artifacts.CreateArtifactRepository(ctx, connect.NewRequest(&v1.CreateArtifactRepositoryRequest{
			Name:        s.name("art", i),
			Description: "seeder artifact repository",
			IsPrivate:   i%4 == 0,
			Namespace:   ns,
		}))
	})
	if err != nil {
		return artifactRepoRef{}, err
	}
	repo := resp.Msg.Repository
	return artifactRepoRef{namespace: repo.GetNamespace(), name: repo.GetName()}, nil
}

func (s *seeder) uploadArtifact(ctx context.Context, i int) error {
	payload := make([]byte, s.cfg.ArtifactSize)
	_, _ = rand.Read(payload)
	version := fmt.Sprintf("0.%d.%d", i/1000, i%1000)
	props := map[string]string{
		"seeder":  s.runID,
		"channel": sample([]string{"stable", "beta", "nightly"}),
		"arch":    sample([]string{"amd64", "arm64"}),
	}

	if entryIdx := i % len(s.regEntries); entryIdx > 0 {
		entry := s.regEntries[entryIdx]
		if repos := s.orgArtifactRepos[entry.org]; len(repos) > 0 {
			repo := repos[i%len(repos)]
			path := fmt.Sprintf("pkg/%s-%d.bin", repo.name, i)
			if err := s.client.uploadArtifactHTTP(ctx, entry, repo.name, version, path, props, payload); err != nil {
				return fmt.Errorf("via %s: %w", entry.host, err)
			}
			return nil
		}
	}

	repo := s.artifactRepos[i%len(s.artifactRepos)]
	initResp, err := retry(ctx, s.cfg, func(ctx context.Context) (*connect.Response[v1.InitiateArtifactUploadResponse], error) {
		return s.client.artifacts.InitiateArtifactUpload(ctx, connect.NewRequest(&v1.InitiateArtifactUploadRequest{
			RepoName:  repo.name,
			Namespace: repo.namespace,
		}))
	})
	if err != nil {
		return fmt.Errorf("initiate: %w", err)
	}

	if err := s.client.patchChunk(ctx, s.regEntries[0], initResp.Msg.UploadUrl, payload); err != nil {
		return fmt.Errorf("chunk: %w", err)
	}

	_, err = retry(ctx, s.cfg, func(ctx context.Context) (*connect.Response[v1.CompleteArtifactUploadResponse], error) {
		return s.client.artifacts.CompleteArtifactUpload(ctx, connect.NewRequest(&v1.CompleteArtifactUploadRequest{
			RepoName:   repo.name,
			Namespace:  repo.namespace,
			UploadId:   initResp.Msg.UploadId,
			Version:    version,
			Path:       fmt.Sprintf("pkg/%s-%d.bin", repo.name, i),
			Properties: props,
			Metadata:   fmt.Sprintf(`{"seeder":%q}`, s.runID),
		}))
	})
	if err != nil {
		return fmt.Errorf("complete: %w", err)
	}
	return nil
}

// Cycles the app port and pool, first pass claims catch-alls
func (s *seeder) createPortal(ctx context.Context, i int) error {
	org := s.orgs[i%len(s.orgs)]
	req := &v1.CreatePortalRequest{
		OrgId:       org.id,
		Name:        s.name("portal", i),
		AllowPush:   i%2 == 0,
		RequireAuth: i%2 == 1,
	}
	ports := s.cfg.portalPorts
	slot := i % (len(ports) + 1)
	hostname := fmt.Sprintf("%s-%s-%d.portal.invalid", s.cfg.NamePrefix, s.runID, i)
	switch {
	case slot == 0:
		req.Hostname = hostname
	case i <= len(ports):
		req.Port = int32(ports[slot-1])
	default:
		req.Hostname = hostname
		req.Port = int32(ports[slot-1])
	}
	_, err := retry(ctx, s.cfg, func(ctx context.Context) (*connect.Response[v1.CreatePortalResponse], error) {
		return s.client.portals.CreatePortal(ctx, connect.NewRequest(req))
	})
	return err
}

func (s *seeder) loadDockerRepos(ctx context.Context) error {
	pageToken := ""
	for {
		resp, err := retry(ctx, s.cfg, func(ctx context.Context) (*connect.Response[v1.ListRepositoriesResponse], error) {
			return s.client.repos.ListRepositories(ctx, connect.NewRequest(&v1.ListRepositoriesRequest{
				Page: &v1.PageRequest{PageSize: 500, PageToken: pageToken},
			}))
		})
		if err != nil {
			return err
		}
		for _, r := range resp.Msg.Repositories {
			s.dockerRepos = append(s.dockerRepos, repoRef{id: r.GetId(), namespace: r.GetNamespace(), name: r.GetName()})
		}
		pageToken = resp.Msg.Page.GetNextPageToken()
		if pageToken == "" {
			return nil
		}
	}
}

func (s *seeder) sessionFor(ctx context.Context, u userRef) (string, error) {
	s.sessMu.Lock()
	us, ok := s.sessions[u.username]
	if !ok {
		us = &userSession{}
		s.sessions[u.username] = us
	}
	s.sessMu.Unlock()
	us.once.Do(func() {
		us.token, us.err = s.client.login(ctx, u.username, u.password)
	})
	return us.token, us.err
}

// ── client ───────────────────────────────────────────────────────────────

type ctxTokenKey struct{}

func withToken(ctx context.Context, tok string) context.Context {
	return context.WithValue(ctx, ctxTokenKey{}, tok)
}

type client struct {
	cfg        *Config
	httpc      *http.Client
	baseURL    string
	base       *url.URL
	adminToken atomic.Value

	auth      distrofacev1connect.AuthServiceClient
	users     distrofacev1connect.UserServiceClient
	roles     distrofacev1connect.RoleServiceClient
	orgs      distrofacev1connect.OrganizationServiceClient
	tokens    distrofacev1connect.TokenServiceClient
	webhooks  distrofacev1connect.WebhookServiceClient
	artifacts distrofacev1connect.ArtifactServiceClient
	portals   distrofacev1connect.PortalServiceClient
	repos     distrofacev1connect.RepositoryServiceClient
	health    distrofacev1connect.HealthServiceClient

	regTokMu sync.Mutex
	regToks  map[string]regToken
}

type regToken struct {
	token   string
	fetched time.Time
}

type bearerTransport struct {
	c    *client
	base http.RoundTripper
}

func (t *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("Authorization") == "" {
		tok, _ := req.Context().Value(ctxTokenKey{}).(string)
		if tok == "" {
			tok, _ = t.c.adminToken.Load().(string)
		}
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	return t.base.RoundTrip(req)
}

func newClient(cfg *Config) (*client, error) {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base url: %w", err)
	}
	c := &client{cfg: cfg, baseURL: baseURL, base: base, regToks: map[string]regToken{}}
	conns := max(cfg.Concurrency*2, 16)
	c.httpc = &http.Client{
		Timeout: cfg.Timeout,
		Transport: &bearerTransport{c: c, base: &http.Transport{
			MaxIdleConns:        conns,
			MaxIdleConnsPerHost: conns,
			MaxConnsPerHost:     conns,
			IdleConnTimeout:     90 * time.Second,
		}},
	}
	c.auth = distrofacev1connect.NewAuthServiceClient(c.httpc, baseURL)
	c.users = distrofacev1connect.NewUserServiceClient(c.httpc, baseURL)
	c.roles = distrofacev1connect.NewRoleServiceClient(c.httpc, baseURL)
	c.orgs = distrofacev1connect.NewOrganizationServiceClient(c.httpc, baseURL)
	c.tokens = distrofacev1connect.NewTokenServiceClient(c.httpc, baseURL)
	c.webhooks = distrofacev1connect.NewWebhookServiceClient(c.httpc, baseURL)
	c.artifacts = distrofacev1connect.NewArtifactServiceClient(c.httpc, baseURL)
	c.portals = distrofacev1connect.NewPortalServiceClient(c.httpc, baseURL)
	c.repos = distrofacev1connect.NewRepositoryServiceClient(c.httpc, baseURL)
	c.health = distrofacev1connect.NewHealthServiceClient(c.httpc, baseURL)
	return c, nil
}

func (c *client) login(ctx context.Context, identifier, password string) (string, error) {
	resp, err := retry(ctx, c.cfg, func(ctx context.Context) (*connect.Response[v1.LoginResponse], error) {
		return c.auth.Login(ctx, connect.NewRequest(&v1.LoginRequest{Identifier: identifier, Password: password}))
	})
	if err != nil {
		return "", err
	}
	return resp.Msg.SessionToken, nil
}

func (c *client) waitHealthy(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		_, err := c.health.HealthCheck(ctx, connect.NewRequest(&v1.HealthCheckRequest{}))
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("server not healthy at %s after %s: %w", c.baseURL, timeout, err)
		}
		if !sleepCtx(ctx, 2*time.Second) {
			return ctx.Err()
		}
	}
}

// ── registry (OCI /v2/) ──────────────────────────────────────────────────

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (c *client) registryToken(ctx context.Context, e regEntry, repo string) (string, error) {
	key := e.host + "|" + repo
	c.regTokMu.Lock()
	cached, ok := c.regToks[key]
	c.regTokMu.Unlock()
	if ok && time.Since(cached.fetched) < 5*time.Minute {
		return cached.token, nil
	}

	u := fmt.Sprintf("%s/auth/token?service=%s&scope=%s", e.base,
		url.QueryEscape(c.cfg.RegistryService),
		url.QueryEscape("repository:"+repo+":pull,push"))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	if e.host != "" {
		req.Host = e.host
	}
	req.SetBasicAuth(c.cfg.Username, c.cfg.Password)

	body, status, _, err := c.doRaw(req)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("token endpoint returned %d: %s", status, truncate(string(body), 200))
	}
	var tr struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &tr); err != nil || tr.Token == "" {
		return "", fmt.Errorf("bad token response: %s", truncate(string(body), 200))
	}
	c.regTokMu.Lock()
	c.regToks[key] = regToken{token: tr.Token, fetched: time.Now()}
	c.regTokMu.Unlock()
	return tr.Token, nil
}

// Rotates across all four supported manifest media types
func (c *client) pushImage(ctx context.Context, e regEntry, repo, tag string, layerSize, variant int) error {
	tok, err := c.registryToken(ctx, e, repo)
	if err != nil {
		return fmt.Errorf("registry token: %w", err)
	}

	switch variant % 4 {
	case 0:
		_, err = c.pushArchManifest(ctx, e, repo, tok, tag, "amd64", mtOCIManifest, mtOCIConfig, mtOCILayer, layerSize)
	case 1:
		_, err = c.pushArchManifest(ctx, e, repo, tok, tag, "amd64", mtDockerManifest, mtDockerConfig, mtDockerLayer, layerSize)
	case 2:
		err = c.pushMultiArch(ctx, e, repo, tok, tag, mtOCIIndex, mtOCIManifest, mtOCIConfig, mtOCILayer, layerSize)
	default:
		err = c.pushMultiArch(ctx, e, repo, tok, tag, mtDockerList, mtDockerManifest, mtDockerConfig, mtDockerLayer, layerSize)
	}
	return err
}

type childManifest struct {
	digest string
	size   int
}

// Pushes config plus layer blobs then the manifest at ref
func (c *client) pushArchManifest(ctx context.Context, e regEntry, repo, tok, ref, arch, manifestMT, configMT, layerMT string, layerSize int) (childManifest, error) {
	layer := make([]byte, layerSize)
	_, _ = rand.Read(layer)
	layerDgst := digestOf(layer)

	cfgBlob, _ := json.Marshal(map[string]any{
		"architecture": arch,
		"os":           "linux",
		"config":       map[string]any{},
		"rootfs":       map[string]any{"type": "layers", "diff_ids": []string{layerDgst}},
	})
	cfgDgst := digestOf(cfgBlob)

	if err := c.uploadBlob(ctx, e, repo, tok, layerDgst, layer); err != nil {
		return childManifest{}, fmt.Errorf("layer blob: %w", err)
	}
	if err := c.uploadBlob(ctx, e, repo, tok, cfgDgst, cfgBlob); err != nil {
		return childManifest{}, fmt.Errorf("config blob: %w", err)
	}

	manifest, _ := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"mediaType":     manifestMT,
		"config": map[string]any{
			"mediaType": configMT,
			"digest":    cfgDgst,
			"size":      len(cfgBlob),
		},
		"layers": []any{map[string]any{
			"mediaType": layerMT,
			"digest":    layerDgst,
			"size":      len(layer),
		}},
	})
	if ref == "" {
		ref = digestOf(manifest)
	}
	if _, _, err := c.regRequest(ctx, http.MethodPut, e.base+"/v2/"+repo+"/manifests/"+ref, e.host, tok, manifestMT, "", manifest); err != nil {
		return childManifest{}, fmt.Errorf("manifest %s: %w", ref, err)
	}
	return childManifest{digest: digestOf(manifest), size: len(manifest)}, nil
}

// Pushes per arch children by digest then a tagged index
func (c *client) pushMultiArch(ctx context.Context, e regEntry, repo, tok, tag, indexMT, manifestMT, configMT, layerMT string, layerSize int) error {
	archs := []string{"amd64", "arm64"}
	entries := make([]any, 0, len(archs))
	for _, arch := range archs {
		child, err := c.pushArchManifest(ctx, e, repo, tok, "", arch, manifestMT, configMT, layerMT, layerSize)
		if err != nil {
			return fmt.Errorf("%s child: %w", arch, err)
		}
		entries = append(entries, map[string]any{
			"mediaType": manifestMT,
			"digest":    child.digest,
			"size":      child.size,
			"platform":  map[string]any{"architecture": arch, "os": "linux"},
		})
	}
	index, _ := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"mediaType":     indexMT,
		"manifests":     entries,
	})
	if _, _, err := c.regRequest(ctx, http.MethodPut, e.base+"/v2/"+repo+"/manifests/"+tag, e.host, tok, indexMT, "", index); err != nil {
		return fmt.Errorf("index: %w", err)
	}
	return nil
}

// Rebases a possibly absolute location onto the entry's dial address
func rebase(e regEntry, loc string) (string, error) {
	u, err := url.Parse(loc)
	if err != nil {
		return "", fmt.Errorf("bad location %q: %w", loc, err)
	}
	target := e.base + u.EscapedPath()
	if u.RawQuery != "" {
		target += "?" + u.RawQuery
	}
	return target, nil
}

func (c *client) uploadBlob(ctx context.Context, e regEntry, repo, tok, dgst string, blob []byte) error {
	_, hdr, err := c.regRequest(ctx, http.MethodPost, e.base+"/v2/"+repo+"/blobs/uploads/", e.host, tok, "", "", nil)
	if err != nil {
		return err
	}
	loc := hdr.Get("Location")
	if loc == "" {
		return fmt.Errorf("upload start returned no location")
	}
	putURL, err := rebase(e, loc)
	if err != nil {
		return err
	}
	sep := "?"
	if strings.Contains(putURL, "?") {
		sep = "&"
	}
	_, _, err = c.regRequest(ctx, http.MethodPut, putURL+sep+"digest="+url.QueryEscape(dgst), e.host, tok, "application/octet-stream", "", blob)
	return err
}

func (c *client) pullManifest(ctx context.Context, e regEntry, repo, tag string) error {
	tok, err := c.registryToken(ctx, e, repo)
	if err != nil {
		return fmt.Errorf("registry token: %w", err)
	}
	_, _, err = c.regRequest(ctx, http.MethodGet, e.base+"/v2/"+repo+"/manifests/"+tag, e.host, tok, "", manifestAccept, nil)
	return err
}

// PATCHes artifact bytes to the returned upload endpoint
func (c *client) patchChunk(ctx context.Context, e regEntry, uploadURL string, payload []byte) error {
	target, err := rebase(e, uploadURL)
	if err != nil {
		return err
	}
	_, _, err = c.regRequest(ctx, http.MethodPatch, target, e.host, "", "application/octet-stream", "", payload)
	return err
}

// Uploads through a portal's namespace mapped v1 facade
func (c *client) uploadArtifactHTTP(ctx context.Context, e regEntry, repoName, version, path string, props map[string]string, payload []byte) error {
	_, hdr, err := c.regRequest(ctx, http.MethodPost, e.base+"/api/v1/artifacts/"+repoName+"/upload", e.host, "", "", "", nil)
	if err != nil {
		return fmt.Errorf("initiate: %w", err)
	}
	loc := hdr.Get("Location")
	if loc == "" {
		if id := hdr.Get("Upload-ID"); id != "" {
			loc = "/api/v1/artifacts/" + repoName + "/upload/" + id
		} else {
			return fmt.Errorf("initiate returned no location")
		}
	}
	target, err := rebase(e, loc)
	if err != nil {
		return err
	}
	if _, _, err := c.regRequest(ctx, http.MethodPatch, target, e.host, "", "application/octet-stream", "", payload); err != nil {
		return fmt.Errorf("chunk: %w", err)
	}
	q := url.Values{}
	q.Set("version", version)
	q.Set("path", path)
	propsJSON, _ := json.Marshal(props)
	if _, _, err := c.regRequest(ctx, http.MethodPut, target+"?"+q.Encode(), e.host, "", "application/json", "", propsJSON); err != nil {
		return fmt.Errorf("complete: %w", err)
	}
	return nil
}

// Retrying HTTP request, 2xx expected, registry token optional
func (c *client) regRequest(ctx context.Context, method, u, host, tok, contentType, accept string, body []byte) ([]byte, http.Header, error) {
	var lastErr error
	for attempt := 0; attempt <= c.cfg.Retries; attempt++ {
		if attempt > 0 && !sleepCtx(ctx, backoff(attempt)) {
			return nil, nil, ctx.Err()
		}
		var r io.Reader
		if body != nil {
			r = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, u, r)
		if err != nil {
			return nil, nil, err
		}
		if host != "" {
			req.Host = host
		}
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		respBody, status, hdr, err := c.doRaw(req)
		if err != nil {
			lastErr = fmt.Errorf("%s %s: %w", method, u, err)
			continue
		}
		if status >= 200 && status < 300 {
			return respBody, hdr, nil
		}
		lastErr = fmt.Errorf("%s %s -> HTTP %d: %s", method, u, status, truncate(string(respBody), 300))
		if status < 500 && status != http.StatusTooManyRequests {
			return nil, nil, lastErr
		}
	}
	return nil, nil, lastErr
}

func (c *client) doRaw(req *http.Request) ([]byte, int, http.Header, error) {
	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, resp.Header, err
	}
	return body, resp.StatusCode, resp.Header, nil
}

// ── phase machinery ──────────────────────────────────────────────────────

func requirePools(phase string, pools map[string]int) error {
	for label, n := range pools {
		if n == 0 {
			return fmt.Errorf("phase %q needs %s but none exist", phase, label)
		}
	}
	return nil
}

// Runs a pool-building phase and returns created refs
func collect[T any](ctx context.Context, s *seeder, name string, n int, create func(context.Context, int) (T, error)) ([]T, error) {
	if n <= 0 {
		return nil, nil
	}
	var mu sync.Mutex
	out := make([]T, 0, n)
	err := runPhase(ctx, s.cfg, name, n, func(ctx context.Context, i int) error {
		v, err := create(ctx, i)
		if err != nil {
			return err
		}
		mu.Lock()
		out = append(out, v)
		mu.Unlock()
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("phase %q created 0/%d entities, cannot continue", name, n)
	}
	return out, nil
}

// Runs fn across [0,total) on a bounded worker pool
func runPhase(ctx context.Context, cfg *Config, name string, total int, fn func(ctx context.Context, i int) error) error {
	if total <= 0 {
		return nil
	}
	workers := min(cfg.Concurrency, total)

	var ok, fail, samples atomic.Int64
	start := time.Now()
	var next atomic.Int64
	var (
		mu       sync.Mutex
		firstErr error
	)

	cctx, cancel := context.WithCancel(ctx)
	defer cancel()

	stop := make(chan struct{})
	var printer sync.WaitGroup
	printer.Add(1)
	go func() {
		defer printer.Done()
		t := time.NewTicker(cfg.ProgressInterval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				o, f := ok.Load(), fail.Load()
				done := o + f
				elapsed := time.Since(start)
				eta := "-"
				if rate := float64(o) / elapsed.Seconds(); rate > 0 && done < int64(total) {
					eta = (time.Duration(float64(int64(total)-done)/rate) * time.Second).Round(time.Second).String()
				}
				fmt.Printf("    %-16s %d/%d (%5.1f%%)  %.0f/s  ok=%d fail=%d  eta=%s\n",
					name, done, total, 100*float64(done)/float64(total),
					float64(o)/elapsed.Seconds(), o, f, eta)
			}
		}
	}()

	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if cctx.Err() != nil {
					return
				}
				i := int(next.Add(1)) - 1
				if i >= total {
					return
				}
				if err := fn(cctx, i); err != nil {
					fail.Add(1)
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					if cnt := samples.Add(1); cnt <= int64(cfg.MaxErrorSamples) {
						fmt.Printf("    ! %s error: %v\n", name, err)
						if cnt == int64(cfg.MaxErrorSamples) {
							fmt.Printf("    ! %s: further errors suppressed\n", name)
						}
					}
					if cfg.FailFast {
						cancel()
						return
					}
					continue
				}
				ok.Add(1)
			}
		}()
	}
	wg.Wait()
	close(stop)
	printer.Wait()

	if err := ctx.Err(); err != nil {
		return err
	}
	dur := time.Since(start)
	fmt.Printf("  ✓ %-16s ok=%d fail=%d in %s (%.0f/s)\n",
		name, ok.Load(), fail.Load(), dur.Round(time.Millisecond), float64(ok.Load())/dur.Seconds())

	if cfg.FailFast {
		return firstErr
	}
	return nil
}

// ── helpers ──────────────────────────────────────────────────────────────

func retry[T any](ctx context.Context, cfg *Config, fn func(context.Context) (T, error)) (T, error) {
	var zero T
	var lastErr error
	for attempt := 0; attempt <= cfg.Retries; attempt++ {
		if attempt > 0 && !sleepCtx(ctx, backoff(attempt)) {
			return zero, ctx.Err()
		}
		v, err := fn(ctx)
		if err == nil {
			return v, nil
		}
		lastErr = err
		if !retryable(err) {
			break
		}
	}
	return zero, lastErr
}

func retryable(err error) bool {
	switch connect.CodeOf(err) {
	case connect.CodeUnavailable, connect.CodeDeadlineExceeded, connect.CodeResourceExhausted, connect.CodeAborted, connect.CodeUnknown:
		return true
	case connect.CodeInternal:
		msg := err.Error()
		return strings.Contains(msg, "locked") || strings.Contains(msg, "busy")
	}
	return false
}

func backoff(attempt int) time.Duration {
	return min(time.Duration(attempt*attempt)*150*time.Millisecond, 5*time.Second)
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func truncate(s string, n int) string {
	if s = strings.TrimSpace(s); len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

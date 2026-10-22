package mirror

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/gitrepo"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

const (
	importPhaseFetch      = "fetch"
	importPhaseWiki       = "wiki"
	importPhaseLFS        = "lfs"
	importPhaseLabels     = "labels"
	importPhaseMilestones = "milestones"
	importPhaseIssues     = "issues"
	importPhasePulls      = "pulls"
	importPhaseReleases   = "releases"
	importDone            = "done"
)

var importPhases = []string{
	importPhaseFetch, importPhaseWiki, importPhaseLFS, importPhaseLabels,
	importPhaseMilestones, importPhaseIssues, importPhasePulls, importPhaseReleases,
}

// Pages one list may span before the importer gives up
const importMaxPages = 1000

// Objects requested per lfs batch call
const lfsBatchSize = 100

// Timeline comment kinds the importer writes
const (
	commentKindComment  = "comment"
	commentKindImported = "imported"
	commentKindClosed   = "closed"
	commentKindMerged   = "merged"
)

// A dismissed import must not return through a late save
var errImportDismissed = errors.New("import was dismissed")

// Resumable cursor persisted in git_imports.state
type importState struct {
	SyncState
	Options    ImportOptions     `json:"options"`
	Forge      string            `json:"forge,omitempty"`
	Cursor     map[string]int    `json:"cursor,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
	Milestones map[string]string `json:"milestones,omitempty"`
	Pulls      map[string]int64  `json:"pulls,omitempty"`
	MaxNumber  int64             `json:"max_number,omitempty"`
	Counts     map[string]int    `json:"counts,omitempty"`
	Notes      []string          `json:"notes,omitempty"`
}

func parseImportState(raw string) importState {
	var st importState
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &st)
	}
	if st.Cursor == nil {
		st.Cursor = map[string]int{}
	}
	if st.Labels == nil {
		st.Labels = map[string]string{}
	}
	if st.Milestones == nil {
		st.Milestones = map[string]string{}
	}
	if st.Pulls == nil {
		st.Pulls = map[string]int64{}
	}
	if st.Counts == nil {
		st.Counts = map[string]int{}
	}
	return st
}

func (s *importState) encode() string {
	b, err := json.Marshal(s)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func (s *importState) note(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	for _, n := range s.Notes {
		if n == msg {
			return
		}
	}
	s.Notes = append(s.Notes, msg)
}

func nextImportPhase(phase string) string {
	for i, p := range importPhases {
		if p == phase && i+1 < len(importPhases) {
			return importPhases[i+1]
		}
	}
	return importDone
}

// Progress line the UI shows for a phase
func importProgress(phase string) string {
	switch phase {
	case importPhaseFetch:
		return "fetching branches and tags"
	case importPhaseWiki:
		return "fetching the wiki"
	case importPhaseLFS:
		return "downloading lfs objects"
	case importPhaseLabels:
		return "importing labels"
	case importPhaseMilestones:
		return "importing milestones"
	case importPhaseIssues:
		return "importing issues"
	case importPhasePulls:
		return "importing pull requests"
	case importPhaseReleases:
		return "importing releases"
	}
	return "importing " + phase
}

// Saves the cursor unless the import was dismissed meanwhile
func (m *Monitor) saveImport(ctx context.Context, imp *db.GitImport) error {
	cur, err := m.store.GetGitImport(ctx, imp.RepoID)
	if err != nil {
		return err
	}
	if cur == nil {
		return errImportDismissed
	}
	return m.store.SaveGitImport(ctx, imp)
}

// Metadata shapes shared by the forge importers
type forgeUser struct {
	Login string
	Email string
}

type forgeLabel struct {
	Name        string
	Color       string
	Description string
}

type forgeMilestone struct {
	ID          string
	Title       string
	Description string
	State       string
	DueDate     *time.Time
	ClosedAt    *time.Time
}

type forgeIssue struct {
	Number      int64
	Title       string
	Body        string
	State       string
	Author      forgeUser
	Labels      []string
	MilestoneID string
	Locked      bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ClosedAt    *time.Time
	URL         string
}

type forgeComment struct {
	Author    forgeUser
	Body      string
	CreatedAt time.Time
}

type forgePull struct {
	forgeIssue
	HeadBranch string
	HeadSHA    string
	BaseBranch string
	Draft      bool
	Merged     bool
	MergedAt   *time.Time
	MergeSHA   string
}

type forgeReview struct {
	Author      forgeUser
	State       string
	Body        string
	CommitSHA   string
	SubmittedAt *time.Time
	Dismissed   bool
}

type forgeRelease struct {
	Tag         string
	Name        string
	Body        string
	Draft       bool
	Prerelease  bool
	TargetSHA   string
	Author      forgeUser
	PublishedAt *time.Time
	CreatedAt   time.Time
}

// Request context shared by every importer call
type forgeCtx struct {
	client *http.Client
	up     gitUpstream
	token  string
}

// Forge metadata reader, lists are paged from one
type forgeDriver interface {
	labels(ctx context.Context, f forgeCtx, page int) ([]forgeLabel, bool, error)
	milestones(ctx context.Context, f forgeCtx, page int) ([]forgeMilestone, bool, error)
	issues(ctx context.Context, f forgeCtx, page int) ([]forgeIssue, bool, error)
	comments(ctx context.Context, f forgeCtx, number int64, page int) ([]forgeComment, bool, error)
	pulls(ctx context.Context, f forgeCtx, page int) ([]forgePull, bool, error)
	pullComments(ctx context.Context, f forgeCtx, number int64, page int) ([]forgeComment, bool, error)
	reviews(ctx context.Context, f forgeCtx, number int64) ([]forgeReview, error)
	releases(ctx context.Context, f forgeCtx, page int) ([]forgeRelease, bool, error)
	pullHeadRef(number int64) string
}

func forgeDriverFor(name string) forgeDriver {
	switch name {
	case forgeGitHub:
		return githubForge{}
	case forgeGitLab:
		return gitlabForge{}
	case forgeGitea:
		return giteaForge{}
	}
	return nil
}

// One list page, more when the page came back full
func pagedJSON[T any](ctx context.Context, f forgeCtx, u string, headers map[string]string, pageSize int) ([]T, bool, error) {
	var batch []T
	if err := fetchJSON(ctx, f.client, u, headers, &batch); err != nil {
		return nil, false, err
	}
	return batch, len(batch) >= pageSize, nil
}

// Escapes each path segment on its own
func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

func normalizeColor(c string) string {
	c = strings.TrimSpace(strings.ToLower(c))
	if c == "" {
		return "#ededed"
	}
	if !strings.HasPrefix(c, "#") {
		c = "#" + c
	}
	return c
}

func normalizeIssueState(s string) string {
	switch strings.ToLower(s) {
	case "closed", "merged":
		return db.IssueStateClosed
	}
	return db.IssueStateOpen
}

func isHexHash(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// Payload marking rows that came from another forge
func importMarker(source string, u forgeUser, link string) string {
	p := map[string]any{"imported": true, "source": source}
	if u.Login != "" {
		p["author"] = u.Login
	}
	if u.Email != "" {
		p["email"] = u.Email
	}
	if link != "" {
		p["url"] = link
	}
	b, _ := json.Marshal(p)
	return string(b)
}

func (m *Monitor) execImport(ctx context.Context, repo *db.GitRepository, imp *db.GitImport) {
	if imp.Phase == importDone {
		return
	}
	key := importKey(repo.ID)
	ev := Event{
		Kind:      "import",
		RepoID:    repo.ID,
		Namespace: repo.Namespace,
		Name:      repo.Name,
		Private:   repo.IsPrivate,
		OwnerID:   repo.OwnerID,
	}
	state := parseImportState(imp.State)
	// Empty error marks the row busy, pushes wait on it
	imp.LastError = ""
	if err := m.saveImport(ctx, imp); err != nil {
		if !errors.Is(err, errImportDismissed) {
			m.log.Error("import state update for %s: %v", repo.ID, err)
		}
		return
	}
	runCtx, cancel := context.WithTimeout(ctx, currentLimits().SyncTimeout)
	m.armCancel(key, cancel)
	m.beginSync(key, ev)

	report := func(msg string) { m.progress(key, ev, msg) }
	syncErr := m.runImport(runCtx, repo, imp, &state, report)
	cancel()
	if m.disarmCancel(key) && syncErr != nil {
		syncErr = ErrSyncStopped
	}

	var msg string
	state.SyncState, msg = nextState(state.SyncState, syncErr)
	switch {
	case errors.Is(syncErr, errImportDismissed):
		m.log.Info("import for %s/%s dismissed", repo.Namespace, repo.Name)
		m.endSync(key, ev, nil)
		return
	case errors.Is(syncErr, ErrSyncStopped):
		m.log.Info("import for %s/%s stopped by user", repo.Namespace, repo.Name)
	case syncErr != nil:
		m.log.Error("import for %s/%s: %v", repo.Namespace, repo.Name, syncErr)
	default:
		m.log.Info("import for %s/%s finished: %v", repo.Namespace, repo.Name, state.Counts)
	}
	imp.State = state.encode()
	imp.LastError = msg
	if err := m.saveImport(statusCtx(ctx), imp); err != nil && !errors.Is(err, errImportDismissed) {
		m.log.Error("import state update for %s: %v", repo.ID, err)
	}
	m.endSync(key, ev, syncErr)
}

// Walks phases from the saved cursor, saving after each
func (m *Monitor) runImport(ctx context.Context, repo *db.GitRepository, imp *db.GitImport, st *importState, progress func(string)) error {
	cfg, err := m.Config(repo.MirrorConfig)
	if err != nil {
		return err
	}
	up, err := parseGitUpstream(cfg.GetUpstream())
	if err != nil {
		return err
	}
	if st.Forge == "" {
		st.Forge = imp.Source
	}
	if st.Forge == "" {
		st.Forge = up.Forge
	}
	// Any git host imports, metadata needs a forge api
	var drv forgeDriver
	if st.Options.Metadata {
		if st.Forge == "" {
			if forge, err := m.detectForge(ctx, up); err == nil {
				st.Forge = forge
			} else {
				st.note("metadata not imported, %s is not a GitHub, GitLab, or Gitea host", up.Host)
			}
		}
		if drv = forgeDriverFor(st.Forge); drv == nil && st.Forge != "" {
			st.note("metadata not imported, %s is not a supported forge", st.Forge)
		}
	}
	imp.Source = st.Forge
	up.Forge = st.Forge
	f := forgeCtx{client: m.client, up: up, token: cfg.GetAuthToken()}
	if imp.Phase == "" {
		imp.Phase = importPhaseFetch
	}
	for imp.Phase != importDone {
		progress(importProgress(imp.Phase))
		var err error
		switch imp.Phase {
		case importPhaseFetch:
			err = m.importFetch(ctx, repo, cfg)
		case importPhaseWiki:
			if st.Options.Wiki {
				err = m.importWiki(ctx, repo, cfg, up, st)
			}
		case importPhaseLFS:
			if st.Options.LFS {
				err = m.importLFS(ctx, repo, cfg, up, st, progress)
			}
		case importPhaseLabels:
			if drv != nil {
				err = m.importLabels(ctx, repo, drv, f, st)
			}
		case importPhaseMilestones:
			if drv != nil {
				err = m.importMilestones(ctx, repo, drv, f, st)
			}
		case importPhaseIssues:
			if drv != nil {
				err = m.importIssues(ctx, repo, drv, f, st, progress)
			}
		case importPhasePulls:
			if drv != nil {
				err = m.importPulls(ctx, repo, cfg, up, drv, f, st, progress)
			}
		case importPhaseReleases:
			if drv != nil {
				err = m.importReleases(ctx, repo, drv, f, st)
			}
		default:
			err = fmt.Errorf("unknown import phase %q", imp.Phase)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", imp.Phase, err)
		}
		imp.Phase = nextImportPhase(imp.Phase)
		imp.State = st.encode()
		if err := m.saveImport(ctx, imp); err != nil {
			return err
		}
	}
	return m.finishImport(ctx, repo, st)
}

// Probes vendor api roots when the host name says nothing
func (m *Monitor) detectForge(ctx context.Context, up gitUpstream) (string, error) {
	probe := func(u string) int {
		pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(pctx, http.MethodGet, u, nil)
		if err != nil {
			return 0
		}
		resp, err := m.client.Do(req)
		if err != nil {
			return 0
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	base := "https://" + up.Host
	if probe(base+"/api/v1/version") == http.StatusOK {
		return forgeGitea, nil
	}
	if code := probe(base + "/api/v4/version"); code == http.StatusOK || code == http.StatusUnauthorized {
		return forgeGitLab, nil
	}
	if probe(base+"/api/v3/meta") == http.StatusOK {
		return forgeGitHub, nil
	}
	return "", fmt.Errorf("%w: cannot tell which forge hosts %s, use a github, gitlab, or gitea url", ErrInvalid, up.Host)
}

func (m *Monitor) importFetch(ctx context.Context, repo *db.GitRepository, cfg *v1.MirrorConfig) error {
	state := parseGitMirrorState(repo.MirrorState)
	err := m.syncGitRepo(ctx, repo, cfg, &state)
	var nothing *nothingMirroredError
	if errors.As(err, &nothing) {
		err = nil
	}
	if err != nil {
		return err
	}
	m.writeGitMirrorStatus(ctx, repo, state, "")
	if fresh, ferr := m.store.GetGitRepositoryByID(ctx, repo.ID); ferr == nil && fresh != nil {
		repo.DefaultBranch = fresh.DefaultBranch
	}
	return nil
}

func (m *Monitor) importWiki(ctx context.Context, repo *db.GitRepository, cfg *v1.MirrorConfig, up gitUpstream, st *importState) error {
	state := parseGitMirrorState(repo.MirrorState)
	auth, err := m.gitAuth(up, cfg, &state)
	if err != nil {
		return err
	}
	wikiURL := up.wikiURL()
	refs, err := m.git.ListRemote(ctx, wikiURL, auth, gitProbeTimeout)
	if err != nil {
		err = classifyGitErr(err)
		if _, limited := RetryAfter(err); limited {
			return err
		}
		st.note("wiki not available upstream")
		return nil
	}
	hasBranch := false
	for _, ref := range refs {
		if ref != nil && ref.Type() == plumbing.HashReference && ref.Name().IsBranch() {
			hasBranch = true
			break
		}
	}
	if !hasBranch {
		return nil
	}
	eng := m.git.Engine()
	if !eng.Exists(repo.Namespace, repo.Name, true) {
		if err := eng.Init(repo.Namespace, repo.Name, true, repo.DefaultBranch, repo.ObjectFormat); err != nil {
			return err
		}
	}
	if err := m.git.FetchRemote(ctx, repo, true, wikiURL, mirrorRefspecs, auth); err != nil {
		return classifyGitErr(err)
	}
	// Upstream wikis often use master, point HEAD at what arrived
	if r, err := eng.OpenRepo(repo.Namespace, repo.Name, true); err == nil {
		defer r.Close()
		if r.BranchHash(r.HeadBranch()).IsZero() {
			if branches, _ := r.Branches(); len(branches) > 0 {
				pick := ""
				for _, candidate := range []string{"master", "main", repo.DefaultBranch} {
					if _, ok := branches[candidate]; ok {
						pick = candidate
						break
					}
				}
				if pick == "" {
					for name := range branches {
						if pick == "" || name < pick {
							pick = name
						}
					}
				}
				_ = r.SetHead(pick)
			}
		}
	}
	st.Counts["wiki"] = 1
	return nil
}

// Walks every branch tip for pointer files
func (m *Monitor) collectLFSPointers(ctx context.Context, repo *db.GitRepository) (map[string]int64, error) {
	r, err := m.git.Engine().OpenRepo(repo.Namespace, repo.Name, false)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	branches, err := r.Branches()
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	seen := map[plumbing.Hash]bool{}
	for _, h := range branches {
		c, err := r.Commit(h)
		if err != nil {
			continue
		}
		tree, err := c.Tree()
		if err != nil || seen[tree.Hash] {
			continue
		}
		seen[tree.Hash] = true
		err = tree.Files().ForEach(func(f *object.File) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if f.Size > 1024 {
				return nil
			}
			content, err := f.Contents()
			if err != nil {
				return nil
			}
			if ptr := gitrepo.ParseLFSPointer([]byte(content)); ptr != nil {
				out[ptr.OID] = ptr.Size
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

type lfsAction struct {
	Href   string            `json:"href"`
	Header map[string]string `json:"header"`
}

// Asks the upstream batch api where to download each object
func (m *Monitor) lfsBatch(ctx context.Context, batchURL, user, pass string, objs []gitrepo.LFSPointer) (map[string]lfsAction, error) {
	type obj struct {
		OID  string `json:"oid"`
		Size int64  `json:"size"`
	}
	req := struct {
		Operation string   `json:"operation"`
		Transfers []string `json:"transfers"`
		Objects   []obj    `json:"objects"`
	}{Operation: "download", Transfers: []string{"basic"}}
	for _, p := range objs {
		req.Objects = append(req.Objects, obj{OID: p.OID, Size: p.Size})
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, batchURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("Accept", "application/vnd.git-lfs+json")
	hreq.Header.Set("Content-Type", "application/vnd.git-lfs+json")
	if pass != "" {
		hreq.SetBasicAuth(user, pass)
	}
	resp, err := m.client.Do(hreq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := classifyResponse(resp, batchURL); err != nil {
		return nil, err
	}
	var out struct {
		Objects []struct {
			OID     string               `json:"oid"`
			Actions map[string]lfsAction `json:"actions"`
			Error   *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		} `json:"objects"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	res := map[string]lfsAction{}
	for _, o := range out.Objects {
		if a, ok := o.Actions["download"]; ok && o.Error == nil {
			res[o.OID] = a
		}
	}
	return res, nil
}

// Streams one upstream object into the blob store
func (m *Monitor) downloadLFSObject(ctx context.Context, repo *db.GitRepository, p gitrepo.LFSPointer, act lfsAction) error {
	maxBytes := m.git.GitSettings(ctx, repo).GetMaxLfsObjectMb() * 1024 * 1024
	if maxBytes > 0 && p.Size > maxBytes {
		return fmt.Errorf("object %s exceeds the %d MB limit", p.OID, maxBytes/(1024*1024))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, act.Href, nil)
	if err != nil {
		return err
	}
	for k, v := range act.Header {
		req.Header.Set(k, v)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := classifyResponse(resp, act.Href); err != nil {
		return err
	}
	blobs := m.git.Blobs()
	uploadID, err := blobs.InitiateUpload()
	if err != nil {
		return err
	}
	var body io.Reader = resp.Body
	if maxBytes > 0 {
		body = io.LimitReader(resp.Body, maxBytes+1)
	}
	n, err := blobs.AppendChunk(uploadID, body)
	if err != nil {
		_ = blobs.CancelUpload(uploadID)
		return err
	}
	if maxBytes > 0 && n > maxBytes {
		_ = blobs.CancelUpload(uploadID)
		return fmt.Errorf("object %s exceeds the %d MB limit", p.OID, maxBytes/(1024*1024))
	}
	digest, size, _, err := blobs.CompleteUpload(uploadID)
	if err != nil {
		return err
	}
	if digest != "sha256:"+p.OID || (p.Size > 0 && size != p.Size) {
		return fmt.Errorf("upstream object did not match its pointer")
	}
	return m.store.UpsertLFSObject(ctx, repo.ID, p.OID, size)
}

func (m *Monitor) importLFS(ctx context.Context, repo *db.GitRepository, cfg *v1.MirrorConfig, up gitUpstream, st *importState, progress func(string)) error {
	if !repo.LFSEnabled {
		st.note("lfs disabled on this repository, pointers left as is")
		return nil
	}
	pointers, err := m.collectLFSPointers(ctx, repo)
	if err != nil {
		return err
	}
	if len(pointers) == 0 {
		return nil
	}
	maxBytes := m.git.GitSettings(ctx, repo).GetMaxLfsObjectMb() * 1024 * 1024
	var pending []gitrepo.LFSPointer
	for oid, size := range pointers {
		if obj, _ := m.store.GetLFSObject(ctx, repo.ID, oid); obj != nil {
			continue
		}
		if f, _, err := m.git.Blobs().OpenBlob("sha256:" + oid); err == nil {
			f.Close()
			if err := m.store.UpsertLFSObject(ctx, repo.ID, oid, size); err != nil {
				return err
			}
			continue
		}
		if maxBytes > 0 && size > maxBytes {
			st.note("lfs object %s exceeds the size limit", oid[:12])
			continue
		}
		pending = append(pending, gitrepo.LFSPointer{OID: oid, Size: size})
	}
	user, pass := gitBasicAuth(up.Forge, cfg.GetUsername(), cfg.GetAuthToken())
	done, failed := 0, 0
	for start := 0; start < len(pending); start += lfsBatchSize {
		end := min(start+lfsBatchSize, len(pending))
		actions, err := m.lfsBatch(ctx, up.lfsBatchURL(), user, pass, pending[start:end])
		if err != nil {
			if _, limited := RetryAfter(err); limited || ctx.Err() != nil {
				return err
			}
			// No lfs api upstream, pointers stay and the import continues
			st.note("lfs objects not imported, the upstream batch api answered: %v", err)
			return nil
		}
		for _, p := range pending[start:end] {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			act, ok := actions[p.OID]
			if !ok {
				st.note("upstream has no lfs object %s", p.OID[:12])
				continue
			}
			if err := m.downloadLFSObject(ctx, repo, p, act); err != nil {
				if _, limited := RetryAfter(err); limited {
					return err
				}
				failed++
				if failed <= 5 {
					st.note("lfs object %s not imported: %v", p.OID[:12], err)
				}
				continue
			}
			done++
			st.Counts["lfs"]++
			if done%5 == 0 {
				progress(fmt.Sprintf("downloading lfs objects (%d of %d)", done, len(pending)))
			}
		}
	}
	if failed > 5 {
		st.note("%d lfs objects were not imported", failed)
	}
	return nil
}

func (m *Monitor) importLabels(ctx context.Context, repo *db.GitRepository, drv forgeDriver, f forgeCtx, st *importState) error {
	// Labels the repo already has keep their ids
	repoID := repo.ID
	var existing []db.GitLabel
	if err := m.store.DB().WithContext(ctx).Where("repo_id = ?", repoID).Find(&existing).Error; err != nil {
		return err
	}
	for _, l := range existing {
		st.Labels[strings.ToLower(l.Name)] = l.ID
	}
	for page := max(st.Cursor["labels"], 1); page <= importMaxPages; page++ {
		items, more, err := drv.labels(ctx, f, page)
		if err != nil {
			return err
		}
		for _, l := range items {
			key := strings.ToLower(l.Name)
			if l.Name == "" || st.Labels[key] != "" {
				continue
			}
			row := &db.GitLabel{RepoID: &repoID, Name: l.Name, Color: normalizeColor(l.Color), Description: l.Description}
			if err := m.store.CreateGitLabel(ctx, row); err != nil {
				return err
			}
			st.Labels[key] = row.ID
			st.Counts["labels"]++
		}
		st.Cursor["labels"] = page + 1
		if !more {
			break
		}
	}
	return nil
}

func (m *Monitor) importMilestones(ctx context.Context, repo *db.GitRepository, drv forgeDriver, f forgeCtx, st *importState) error {
	for page := max(st.Cursor["milestones"], 1); page <= importMaxPages; page++ {
		items, more, err := drv.milestones(ctx, f, page)
		if err != nil {
			return err
		}
		for _, ms := range items {
			if ms.ID == "" || st.Milestones[ms.ID] != "" {
				continue
			}
			state := "open"
			if s := strings.ToLower(ms.State); s == "closed" {
				state = "closed"
			}
			row := &db.GitMilestone{RepoID: repo.ID, Title: ms.Title, Description: ms.Description, DueDate: ms.DueDate, State: state, ClosedAt: ms.ClosedAt}
			if err := m.store.CreateGitMilestone(ctx, row); err != nil {
				return err
			}
			st.Milestones[ms.ID] = row.ID
			st.Counts["milestones"]++
		}
		st.Cursor["milestones"] = page + 1
		if !more {
			break
		}
	}
	return nil
}

// Writes the issue row, labels, and imported marker event, no local author
func (m *Monitor) importIssueRow(ctx context.Context, repo *db.GitRepository, st *importState, is forgeIssue, number int64, isPull bool) (*db.GitIssue, error) {
	row := &db.GitIssue{
		ID:        uuid.New().String(),
		RepoID:    repo.ID,
		Number:    number,
		IsPull:    isPull,
		Title:     is.Title,
		Body:      is.Body,
		State:     normalizeIssueState(is.State),
		Locked:    is.Locked,
		ClosedAt:  is.ClosedAt,
		CreatedAt: is.CreatedAt,
		UpdatedAt: is.UpdatedAt,
	}
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now().UTC()
	}
	if row.UpdatedAt.IsZero() {
		row.UpdatedAt = row.CreatedAt
	}
	if is.MilestoneID != "" {
		if ms := st.Milestones[is.MilestoneID]; ms != "" {
			row.MilestoneID = &ms
		}
	}
	if err := m.store.CreateGitIssue(ctx, row); err != nil {
		return nil, err
	}
	var labelIDs []string
	for _, name := range is.Labels {
		if id := st.Labels[strings.ToLower(name)]; id != "" {
			labelIDs = append(labelIDs, id)
		}
	}
	if len(labelIDs) > 0 {
		if err := m.store.SetGitIssueLabels(ctx, row.ID, labelIDs); err != nil {
			return nil, err
		}
	}
	if err := m.addImportEvent(ctx, row, commentKindImported, importMarker(st.Forge, is.Author, is.URL), row.CreatedAt); err != nil {
		return nil, err
	}
	return row, nil
}

func (m *Monitor) addImportEvent(ctx context.Context, issue *db.GitIssue, kind, payload string, at time.Time) error {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	return m.store.CreateGitComment(ctx, &db.GitComment{IssueID: issue.ID, Type: kind, Payload: payload, CreatedAt: at, UpdatedAt: at})
}

// Every comment page comes down before any row is written, so a cut off resumes cleanly
func (m *Monitor) fetchComments(ctx context.Context, drv forgeDriver, f forgeCtx, number int64, pull bool) ([]forgeComment, error) {
	var all []forgeComment
	for page := 1; page <= importMaxPages; page++ {
		var (
			items []forgeComment
			more  bool
			err   error
		)
		if pull {
			items, more, err = drv.pullComments(ctx, f, number, page)
		} else {
			items, more, err = drv.comments(ctx, f, number, page)
		}
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if !more {
			break
		}
	}
	return all, nil
}

// Comments carry the upstream author in their marker and no local author
func (m *Monitor) writeComments(ctx context.Context, st *importState, issue *db.GitIssue, items []forgeComment) error {
	for _, c := range items {
		at := c.CreatedAt
		if at.IsZero() {
			at = time.Now().UTC()
		}
		row := &db.GitComment{
			IssueID:   issue.ID,
			Type:      commentKindComment,
			Body:      c.Body,
			Payload:   importMarker(st.Forge, c.Author, ""),
			CreatedAt: at,
			UpdatedAt: at,
		}
		if err := m.store.CreateGitComment(ctx, row); err != nil {
			return err
		}
		st.Counts["comments"]++
	}
	return nil
}

// Comment inserts bump updated_at, put the upstream value back
func (m *Monitor) restoreIssueTime(ctx context.Context, issue *db.GitIssue) {
	_ = m.store.UpdateGitIssueColumns(ctx, issue.ID, map[string]any{"updated_at": issue.UpdatedAt})
}

func (m *Monitor) importIssues(ctx context.Context, repo *db.GitRepository, drv forgeDriver, f forgeCtx, st *importState, progress func(string)) error {
	for page := max(st.Cursor["issues"], 1); page <= importMaxPages; page++ {
		items, more, err := drv.issues(ctx, f, page)
		if err != nil {
			return err
		}
		for i, is := range items {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			st.MaxNumber = max(st.MaxNumber, is.Number)
			existing, err := m.store.GetGitIssue(ctx, repo.ID, is.Number)
			if err != nil {
				return err
			}
			if existing != nil {
				continue
			}
			comments, err := m.fetchComments(ctx, drv, f, is.Number, false)
			if err != nil {
				return err
			}
			row, err := m.importIssueRow(ctx, repo, st, is, is.Number, false)
			if err != nil {
				return err
			}
			if err := m.writeComments(ctx, st, row, comments); err != nil {
				return err
			}
			if row.State == db.IssueStateClosed {
				at := row.UpdatedAt
				if is.ClosedAt != nil {
					at = *is.ClosedAt
				}
				_ = m.addImportEvent(ctx, row, commentKindClosed, importMarker(st.Forge, forgeUser{}, ""), at)
			}
			m.restoreIssueTime(ctx, row)
			st.Counts["issues"]++
			if i%10 == 0 {
				progress(fmt.Sprintf("importing issues (#%d)", is.Number))
			}
		}
		st.Cursor["issues"] = page + 1
		if !more {
			break
		}
	}
	return nil
}

// Raises the issue counter past imported numbers
func (m *Monitor) ensureIssueCounter(ctx context.Context, repo *db.GitRepository, n int64) error {
	if n <= 0 {
		return nil
	}
	fresh, err := m.store.GetGitRepositoryByID(ctx, repo.ID)
	if err != nil || fresh == nil {
		return err
	}
	if fresh.IssueCounter >= n {
		return nil
	}
	return m.store.UpdateGitRepositoryColumns(ctx, repo.ID, map[string]any{"issue_counter": n})
}

// Local pull number, gitlab merge requests get fresh ones
func (m *Monitor) pullNumber(ctx context.Context, repo *db.GitRepository, st *importState, upstream int64) (int64, error) {
	if st.Forge != forgeGitLab {
		return upstream, nil
	}
	key := strconv.FormatInt(upstream, 10)
	if n, ok := st.Pulls[key]; ok {
		return n, nil
	}
	n, err := m.store.NextGitCounter(ctx, repo.ID, "issue_counter")
	if err != nil {
		return 0, err
	}
	st.Pulls[key] = n
	return n, nil
}

func (m *Monitor) localRefHash(repo *db.GitRepository, ref string) string {
	r, err := m.git.Engine().OpenRepo(repo.Namespace, repo.Name, false)
	if err != nil {
		return ""
	}
	defer r.Close()
	h, err := r.Resolve(ref)
	if err != nil || h.IsZero() {
		return ""
	}
	return h.String()
}

func normalizeReviewState(s string) (string, bool) {
	switch strings.ToLower(s) {
	case "approved":
		return "approved", true
	case "changes_requested", "request_changes":
		return "changes_requested", true
	case "commented", "comment", "dismissed":
		return "commented", true
	}
	return "", false
}

func (m *Monitor) importPulls(ctx context.Context, repo *db.GitRepository, cfg *v1.MirrorConfig, up gitUpstream, drv forgeDriver, f forgeCtx, st *importState, progress func(string)) error {
	keys := parseGitMirrorState(repo.MirrorState)
	auth, err := m.gitAuth(up, cfg, &keys)
	if err != nil {
		return err
	}
	if st.Forge == forgeGitLab {
		if err := m.ensureIssueCounter(ctx, repo, st.MaxNumber); err != nil {
			return err
		}
	}
	for page := max(st.Cursor["pulls"], 1); page <= importMaxPages; page++ {
		items, more, err := drv.pulls(ctx, f, page)
		if err != nil {
			return err
		}
		for i, pr := range items {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			number, err := m.pullNumber(ctx, repo, st, pr.Number)
			if err != nil {
				return err
			}
			st.MaxNumber = max(st.MaxNumber, number)
			existing, err := m.store.GetGitIssue(ctx, repo.ID, number)
			if err != nil {
				return err
			}
			if existing != nil {
				continue
			}
			localRef := fmt.Sprintf("refs/pull/%d/head", number)
			if err := m.git.FetchRemote(ctx, repo, false, up.URL, []string{"+" + drv.pullHeadRef(pr.Number) + ":" + localRef}, auth); err != nil {
				if _, limited := RetryAfter(classifyGitErr(err)); limited {
					return err
				}
				st.note("head of pull request %d could not be fetched", pr.Number)
			}
			headSHA := pr.HeadSHA
			if headSHA == "" {
				headSHA = m.localRefHash(repo, localRef)
			}
			issue := pr.forgeIssue
			if pr.Merged {
				issue.State = db.IssueStateClosed
			}
			comments, err := m.fetchComments(ctx, drv, f, pr.Number, true)
			if err != nil {
				return err
			}
			reviews, err := drv.reviews(ctx, f, pr.Number)
			if err != nil {
				return err
			}
			row, err := m.importIssueRow(ctx, repo, st, issue, number, true)
			if err != nil {
				return err
			}
			prRow := &db.GitPullRequest{
				IssueID:        row.ID,
				HeadRepoID:     repo.ID,
				HeadBranch:     pr.HeadBranch,
				HeadSHA:        headSHA,
				BaseBranch:     pr.BaseBranch,
				Draft:          pr.Draft,
				MergeableState: "unknown",
				MergedAt:       pr.MergedAt,
				MergeCommitSHA: pr.MergeSHA,
				HeadDeleted:    pr.State != db.IssueStateOpen && !pr.Merged && headSHA == "",
			}
			if err := m.store.CreateGitPullRequest(ctx, prRow); err != nil {
				return err
			}
			if err := m.writeComments(ctx, st, row, comments); err != nil {
				return err
			}
			for _, rv := range reviews {
				state, ok := normalizeReviewState(rv.State)
				if !ok {
					continue
				}
				at := time.Now().UTC()
				if rv.SubmittedAt != nil {
					at = *rv.SubmittedAt
				}
				// Upstream reviewers are not local accounts, so the review never counts
				review := &db.GitReview{
					IssueID:     row.ID,
					State:       state,
					Body:        rv.Body,
					CommitSHA:   rv.CommitSHA,
					Dismissed:   rv.Dismissed,
					Imported:    true,
					SubmittedAt: &at,
					CreatedAt:   at,
				}
				if err := m.store.CreateGitReview(ctx, review); err != nil {
					return err
				}
				st.Counts["reviews"]++
			}
			switch {
			case pr.Merged:
				at := row.UpdatedAt
				if pr.MergedAt != nil {
					at = *pr.MergedAt
				}
				_ = m.addImportEvent(ctx, row, commentKindMerged, importMarker(st.Forge, forgeUser{}, ""), at)
			case row.State == db.IssueStateClosed:
				at := row.UpdatedAt
				if pr.ClosedAt != nil {
					at = *pr.ClosedAt
				}
				_ = m.addImportEvent(ctx, row, commentKindClosed, importMarker(st.Forge, forgeUser{}, ""), at)
			}
			m.restoreIssueTime(ctx, row)
			st.Counts["pulls"]++
			if i%10 == 0 {
				progress(fmt.Sprintf("importing pull requests (#%d)", pr.Number))
			}
		}
		st.Cursor["pulls"] = page + 1
		if !more {
			break
		}
	}
	return nil
}

// Release target commit, resolved locally for ref name hints
func (m *Monitor) tagCommit(repo *db.GitRepository, tag, hint string) string {
	if isHexHash(hint) {
		return hint
	}
	r, err := m.git.Engine().OpenRepo(repo.Namespace, repo.Name, false)
	if err != nil {
		return ""
	}
	defer r.Close()
	h, err := r.Resolve("refs/tags/" + tag)
	if err != nil || h.IsZero() {
		return ""
	}
	if t := r.TagObject(h); t != nil {
		return t.Target.String()
	}
	return h.String()
}

func releaseRow(repoID string, rel forgeRelease, authorID, targetSHA string) *db.GitRelease {
	row := &db.GitRelease{
		RepoID:      repoID,
		Tag:         rel.Tag,
		Name:        rel.Name,
		Body:        rel.Body,
		Draft:       rel.Draft,
		Prerelease:  rel.Prerelease,
		AuthorID:    authorID,
		TargetSHA:   targetSHA,
		PublishedAt: rel.PublishedAt,
	}
	if row.Name == "" {
		row.Name = rel.Tag
	}
	if !rel.CreatedAt.IsZero() {
		row.CreatedAt, row.UpdatedAt = rel.CreatedAt, rel.CreatedAt
	}
	return row
}

func (m *Monitor) importReleases(ctx context.Context, repo *db.GitRepository, drv forgeDriver, f forgeCtx, st *importState) error {
	for page := max(st.Cursor["releases"], 1); page <= importMaxPages; page++ {
		items, more, err := drv.releases(ctx, f, page)
		if err != nil {
			return err
		}
		for _, rel := range items {
			if rel.Tag == "" {
				continue
			}
			existing, err := m.store.GetGitRelease(ctx, repo.ID, rel.Tag)
			if err != nil {
				return err
			}
			if existing != nil {
				continue
			}
			row := releaseRow(repo.ID, rel, "", m.tagCommit(repo, rel.Tag, rel.TargetSHA))
			if err := m.store.CreateGitRelease(ctx, row); err != nil {
				return err
			}
			st.Counts["releases"]++
		}
		st.Cursor["releases"] = page + 1
		if !more {
			break
		}
	}
	return nil
}

// Settles counters and drops the upstream, a plain repository remains
func (m *Monitor) finishImport(ctx context.Context, repo *db.GitRepository, st *importState) error {
	if err := m.ensureIssueCounter(ctx, repo, st.MaxNumber); err != nil {
		return err
	}
	return m.clearImportSource(ctx, repo)
}

// Mirrors upstream release notes onto the linked git repo
func (m *Monitor) syncLinkedReleases(ctx context.Context, art *db.ArtifactRepository, cfg *v1.MirrorConfig) {
	if art.GitRepoID == nil || *art.GitRepoID == "" {
		return
	}
	repo, err := m.store.GetGitRepositoryByID(ctx, *art.GitRepoID)
	if err != nil || repo == nil {
		return
	}
	forge := forgeForType(art.Type)
	drv := forgeDriverFor(forge)
	if drv == nil {
		return
	}
	up, err := upstreamForForge(forge, cfg.GetUpstream())
	if err != nil {
		return
	}
	f := forgeCtx{client: m.client, up: up, token: cfg.GetAuthToken()}
	for page := 1; page <= maxReleasePages; page++ {
		items, more, err := drv.releases(ctx, f, page)
		if err != nil {
			m.log.Warn("release notes sync for %s/%s: %v", repo.Namespace, repo.Name, err)
			return
		}
		for _, rel := range items {
			if rel.Tag == "" {
				continue
			}
			existing, err := m.store.GetGitRelease(ctx, repo.ID, rel.Tag)
			if err != nil {
				return
			}
			if existing == nil {
				row := releaseRow(repo.ID, rel, "", m.tagCommit(repo, rel.Tag, rel.TargetSHA))
				if err := m.store.CreateGitRelease(ctx, row); err != nil {
					m.log.Warn("release row %s for %s/%s: %v", rel.Tag, repo.Namespace, repo.Name, err)
				}
				continue
			}
			name := rel.Name
			if name == "" {
				name = rel.Tag
			}
			if existing.Name == name && existing.Body == rel.Body && existing.Draft == rel.Draft && existing.Prerelease == rel.Prerelease {
				continue
			}
			existing.Name, existing.Body, existing.Draft, existing.Prerelease = name, rel.Body, rel.Draft, rel.Prerelease
			if err := m.store.UpdateGitRelease(ctx, existing); err != nil {
				m.log.Warn("release row %s for %s/%s: %v", rel.Tag, repo.Namespace, repo.Name, err)
			}
		}
		if !more {
			break
		}
	}
}

// Parses a release package upstream, bare slugs take vendor host
func upstreamForForge(forge, raw string) (gitUpstream, error) {
	s := strings.TrimSpace(raw)
	if !strings.Contains(s, "://") && !isSCPLike(s) {
		parts := strings.Split(strings.Trim(s, "/"), "/")
		if len(parts) == 2 || (len(parts) > 2 && !strings.Contains(parts[0], ".")) {
			host := map[string]string{forgeGitHub: "github.com", forgeGitLab: "gitlab.com", forgeGitea: "codeberg.org"}[forge]
			s = host + "/" + strings.Join(parts, "/")
		}
	}
	up, err := parseGitUpstream(s)
	if err != nil {
		return up, err
	}
	up.Forge = forge
	return up, nil
}

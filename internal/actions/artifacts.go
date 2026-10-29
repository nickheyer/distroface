package actions

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
)

// Artifact row states
const (
	ArtifactPending   = "pending"
	ArtifactConfirmed = "confirmed"
)

const (
	// Where signed download links live
	ArtifactDownloadPrefix = "/api/actions/artifacts"
	artifactURLPurpose     = "actions-artifact-url"
	artifactURLTTL         = time.Hour
	// Uploads that never finalized are dropped after this
	pendingArtifactGrace = 24 * time.Hour
	// Expired rows deleted per sweep pass
	expireBatch = 500
	// The toolkit's rules for artifact names
	invalidArtifactNameChars = "\\/\":<>|*?\r\n"
	artifactZipType          = "application/zip"
)

// ValidateArtifactName applies the rules the upload action checks locally
func ValidateArtifactName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%w: artifact name is required", ErrInvalid)
	}
	if strings.ContainsAny(name, invalidArtifactNameChars) {
		return fmt.Errorf("%w: artifact name %q contains an invalid character", ErrInvalid, name)
	}
	return nil
}

// ArtifactExpired reports whether retention already ran out
func ArtifactExpired(a *db.ActionsArtifact) bool {
	return a.ExpiresAt != nil && a.ExpiresAt.Before(time.Now())
}

// Confirmed and still inside retention
func artifactLive(a *db.ActionsArtifact) bool {
	return a != nil && a.Status == ArtifactConfirmed && !ArtifactExpired(a)
}

// Blocks of an upload wait here until finalize merges them
func (m *Manager) artifactBlockDir(id string) string {
	return filepath.Join(m.artifacts.Blobs().Root(), "actions-blocks", id)
}

// Expiry the caller asks for, capped by the repository setting
func (m *Manager) artifactExpiry(ctx context.Context, repo *db.GitRepository, requested *time.Time) *time.Time {
	var expires *time.Time
	if days := m.Settings(ctx, repo).GetArtifactRetentionDays(); days > 0 {
		t := time.Now().UTC().Add(time.Duration(days) * 24 * time.Hour)
		expires = &t
	}
	if requested != nil {
		t := requested.UTC()
		if expires == nil || t.Before(*expires) {
			expires = &t
		}
	}
	return expires
}

// Pending row for an upload, reused across retries
func (m *Manager) claimArtifact(ctx context.Context, scope *resultsScope, name, contentType string, expires *time.Time, restart bool) (*db.ActionsArtifact, error) {
	a := &db.ActionsArtifact{
		RunID: scope.run.ID, JobID: scope.job.ID, Name: name, ContentType: contentType,
		Status: ArtifactPending, ExpiresAt: expires,
	}
	row, created, err := m.store.CreateActionsArtifactOrReuse(ctx, scope.repo.ID, a)
	if err != nil {
		return nil, err
	}
	if created || row.Status == ArtifactConfirmed {
		return row, nil
	}
	// Retries restart the blocks and the abandon clock
	if restart {
		_ = os.RemoveAll(m.artifactBlockDir(row.ID))
	}
	now := time.Now().UTC()
	cols := map[string]any{"job_id": scope.job.ID, "content_type": contentType, "expires_at": expires, "created_at": now}
	if err := m.store.UpdateActionsArtifactColumns(ctx, row.ID, cols); err != nil {
		return nil, err
	}
	row.JobID, row.ContentType, row.ExpiresAt, row.CreatedAt = scope.job.ID, contentType, expires, now
	return row, nil
}

// RunArtifacts lists confirmed, unexpired uploads of a run, oldest first
func (m *Manager) RunArtifacts(ctx context.Context, run *db.ActionsRun) ([]*db.ActionsArtifact, error) {
	rows, err := m.store.ListActionsArtifacts(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	out := rows[:0]
	for _, a := range rows {
		if artifactLive(a) {
			out = append(out, a)
		}
	}
	return out, nil
}

func (m *Manager) signArtifactURL(id string, exp int64) string {
	h := hmac.New(sha256.New, m.auth.DerivedKey(artifactURLPurpose))
	h.Write([]byte("artifact:" + id + ":" + strconv.FormatInt(exp, 10)))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// ArtifactDownloadURL is a signed link valid for an hour
func (m *Manager) ArtifactDownloadURL(ctx context.Context, a *db.ActionsArtifact) string {
	exp := time.Now().Add(artifactURLTTL).Unix()
	base := strings.TrimRight(m.git.BaseURL(ctx), "/")
	return fmt.Sprintf("%s%s/%s/%s?exp=%d&sig=%s", base, ArtifactDownloadPrefix, a.ID, url.PathEscape(artifactFileName(a)), exp, m.signArtifactURL(a.ID, exp))
}

// ArtifactPageURL is the session authenticated link the run page uses
func (m *Manager) ArtifactPageURL(ctx context.Context, repo *db.GitRepository, a *db.ActionsArtifact) string {
	return m.git.HTMLURL(ctx, repo) + "/-/actions/artifacts/" + a.ID + "/" + url.PathEscape(artifactFileName(a))
}

// ArtifactDownloadHandler serves signed links to confirmed artifacts
func (m *Manager) ArtifactDownloadHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, ArtifactDownloadPrefix+"/")
		id, _, _ := strings.Cut(rest, "/")
		exp, err := strconv.ParseInt(r.URL.Query().Get("exp"), 10, 64)
		if err != nil || id == "" {
			http.Error(w, "bad link", http.StatusBadRequest)
			return
		}
		if time.Now().Unix() > exp {
			http.Error(w, "link expired", http.StatusUnauthorized)
			return
		}
		if !hmac.Equal([]byte(r.URL.Query().Get("sig")), []byte(m.signArtifactURL(id, exp))) {
			http.Error(w, "bad signature", http.StatusUnauthorized)
			return
		}
		a, err := m.store.GetActionsArtifact(r.Context(), id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		m.serveConfirmedArtifact(w, r, a)
	})
}

// ServeRepoArtifact streams an artifact of the repository to a session authenticated caller
func (m *Manager) ServeRepoArtifact(w http.ResponseWriter, r *http.Request, repo *db.GitRepository, id string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx := r.Context()
	a, err := m.store.GetActionsArtifact(ctx, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if a != nil {
		run, err := m.store.GetActionsRunByID(ctx, a.RunID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if run == nil || repo == nil || run.RepoID != repo.ID {
			a = nil
		}
	}
	m.serveConfirmedArtifact(w, r, a)
}

// Streams a confirmed artifact, 404 when missing, 410 once retention ran out
func (m *Manager) serveConfirmedArtifact(w http.ResponseWriter, r *http.Request, a *db.ActionsArtifact) {
	if a == nil || a.Status != ArtifactConfirmed {
		http.Error(w, "artifact not found", http.StatusNotFound)
		return
	}
	if ArtifactExpired(a) {
		http.Error(w, "artifact expired", http.StatusGone)
		return
	}
	m.streamArtifact(w, r, a)
}

// Zips carry the suffix, legacy single file rows keep their name
func artifactFileName(a *db.ActionsArtifact) string {
	if a.ContentType == "" || a.ContentType == artifactZipType {
		return a.Name + ".zip"
	}
	return a.Name
}

// Writes the blob with the headers browsers and the toolkit expect
func (m *Manager) streamArtifact(w http.ResponseWriter, r *http.Request, a *db.ActionsArtifact) {
	f, info, err := m.artifacts.Blobs().OpenBlob(a.Digest)
	if err != nil {
		http.Error(w, "artifact data is missing", http.StatusNotFound)
		return
	}
	defer f.Close()
	ct := a.ContentType
	if ct == "" {
		ct = artifactZipType
	}
	name := artifactFileName(a)
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, name, info.ModTime(), f)
}

// Deletes a row, staged blocks, and unshared blob
func (m *Manager) deleteArtifact(ctx context.Context, a *db.ActionsArtifact) error {
	if err := m.store.DeleteActionsArtifact(ctx, a.ID); err != nil {
		return err
	}
	_ = os.RemoveAll(m.artifactBlockDir(a.ID))
	if a.Digest != "" {
		m.gcArtifactBlob(ctx, a.Digest)
	}
	return nil
}

// Removes a blob once no table references it
func (m *Manager) gcArtifactBlob(ctx context.Context, digest string) {
	referenced, err := m.store.BlobReferenced(ctx, digest)
	if err != nil {
		m.log.Warn("actions: count references of blob %s: %v", digest, err)
		return
	}
	if referenced {
		return
	}
	if err := m.artifacts.Blobs().DeleteBlob(digest); err != nil {
		m.log.Warn("actions: delete artifact blob %s: %v", digest, err)
	}
}

// DeleteRunArtifacts removes every upload of a run with its blocks and blobs
func (m *Manager) DeleteRunArtifacts(ctx context.Context, run *db.ActionsRun) error {
	rows, err := m.store.ListActionsArtifacts(ctx, run.ID)
	if err != nil {
		return err
	}
	for _, a := range rows {
		if err := m.deleteArtifact(ctx, a); err != nil {
			return fmt.Errorf("delete artifact %s of run %s: %w", a.Name, run.ID, err)
		}
	}
	return nil
}

// DeleteRepoArtifacts reclaims artifacts, logs, and caches before a repository's rows go
func (m *Manager) DeleteRepoArtifacts(ctx context.Context, repo *db.GitRepository) error {
	runs, _, err := m.store.ListActionsRuns(ctx, stores.ActionsRunListOptions{RepoID: repo.ID})
	if err != nil {
		return err
	}
	for _, run := range runs {
		if err := m.DeleteRunArtifacts(ctx, run); err != nil {
			return err
		}
		jobs, err := m.store.ListActionsJobs(ctx, run.ID)
		if err != nil {
			return err
		}
		for _, j := range jobs {
			m.dropJobLogs(ctx, j)
		}
	}
	caches, err := m.store.ListActionsCachesForRepo(ctx, repo.ID)
	if err != nil {
		return err
	}
	for _, c := range caches {
		m.dropCache(ctx, c)
	}
	return nil
}

// Deletes expired uploads and abandoned ones, draining in batches
func (m *Manager) expireArtifacts(ctx context.Context) {
	for ctx.Err() == nil {
		expired, err := m.store.ListExpiredActionsArtifacts(ctx, expireBatch)
		if err != nil || len(expired) == 0 {
			break
		}
		for _, a := range expired {
			if err := m.deleteArtifact(ctx, a); err != nil {
				m.log.Warn("actions: expire artifact %s: %v", a.ID, err)
			}
		}
		if len(expired) < expireBatch {
			break
		}
	}
	stale, err := m.store.ListStalePendingActionsArtifacts(ctx, time.Now().UTC().Add(-pendingArtifactGrace), expireBatch)
	if err != nil {
		return
	}
	for _, a := range stale {
		if err := m.deleteArtifact(ctx, a); err != nil {
			m.log.Warn("actions: drop abandoned artifact %s: %v", a.ID, err)
		}
	}
}

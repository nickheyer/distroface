package actions

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nickheyer/distroface/internal/db"
	resultsv1 "github.com/nickheyer/distroface/pkg/proto/github/actions/results/api/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ResultsAPIPrefix is where the actions toolkit expects the artifact service
const ResultsAPIPrefix = "/twirp/github.actions.results.api.v1.ArtifactService"

// Signed upload and download links live this long
const resultsURLTTL = time.Hour

// Job, run, and repository behind a results api call
type resultsScope struct {
	job  *db.ActionsJob
	run  *db.ActionsRun
	repo *db.GitRepository
}

// Twirp error body with its http status
func twirpError(w http.ResponseWriter, code, msg string) {
	status := http.StatusInternalServerError
	switch code {
	case "invalid_argument", "malformed":
		status = http.StatusBadRequest
	case "not_found":
		status = http.StatusNotFound
	case "already_exists":
		status = http.StatusConflict
	case "unauthenticated":
		status = http.StatusUnauthorized
	case "permission_denied":
		status = http.StatusForbidden
	case "failed_precondition":
		status = http.StatusPreconditionFailed
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "msg": msg})
}

// ResultsHandler serves the artifact service the toolkit calls with ACTIONS_RUNTIME_TOKEN
func (m *Manager) ResultsHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+ResultsAPIPrefix+"/CreateArtifact", m.resultsCreate)
	mux.HandleFunc("POST "+ResultsAPIPrefix+"/FinalizeArtifact", m.resultsFinalize)
	mux.HandleFunc("POST "+ResultsAPIPrefix+"/ListArtifacts", m.resultsList)
	mux.HandleFunc("POST "+ResultsAPIPrefix+"/GetSignedArtifactURL", m.resultsSignedURL)
	mux.HandleFunc("POST "+ResultsAPIPrefix+"/DeleteArtifact", m.resultsDelete)
	mux.HandleFunc("PUT "+ResultsAPIPrefix+"/UploadArtifact", m.resultsUpload)
	mux.HandleFunc("GET "+ResultsAPIPrefix+"/DownloadArtifact", m.resultsDownload)
	return mux
}

// Job behind a runtime bearer, nil when missing
func (m *Manager) runtimeBearer(r *http.Request) (string, error) {
	header := r.Header.Get("Authorization")
	token := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	if token == "" || token == header {
		return "", errors.New("ACTIONS_RUNTIME_TOKEN required")
	}
	jobID, _, err := m.ParseRuntimeToken(token)
	if err != nil {
		return "", errors.New("invalid runtime token")
	}
	return jobID, nil
}

// Resolves the running job behind the bearer token
func (m *Manager) resultsAuth(w http.ResponseWriter, r *http.Request) (*resultsScope, bool) {
	jobID, err := m.runtimeBearer(r)
	if err != nil {
		twirpError(w, "unauthenticated", err.Error())
		return nil, false
	}
	scope, err := m.resultsScopeOf(r, jobID)
	if err != nil {
		twirpError(w, "unauthenticated", err.Error())
		return nil, false
	}
	return scope, true
}

// Job, run, and repository of a running job
func (m *Manager) resultsScopeOf(r *http.Request, jobID string) (*resultsScope, error) {
	ctx := r.Context()
	job, err := m.store.GetActionsJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if job == nil || job.Status != StatusRunning {
		return nil, errors.New("job is not running")
	}
	run, err := m.store.GetActionsRunByID(ctx, job.RunID)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, errors.New("run is gone")
	}
	repo, err := m.store.GetGitRepositoryByID(ctx, run.RepoID)
	if err != nil {
		return nil, err
	}
	if repo == nil {
		return nil, errors.New("repository is gone")
	}
	return &resultsScope{job: job, run: run, repo: repo}, nil
}

func readProto(w http.ResponseWriter, r *http.Request, msg proto.Message) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		twirpError(w, "malformed", "read request body: "+err.Error())
		return false
	}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(body, msg); err != nil {
		twirpError(w, "malformed", "decode request body: "+err.Error())
		return false
	}
	return true
}

func writeProto(w http.ResponseWriter, msg proto.Message) {
	body, err := protojson.Marshal(msg)
	if err != nil {
		twirpError(w, "internal", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// The run id the request names must be the caller's own run
func (s *resultsScope) checkRun(w http.ResponseWriter, runID string) bool {
	if runID != s.run.ID {
		twirpError(w, "permission_denied", "workflow_run_backend_id does not match the job's run")
		return false
	}
	return true
}

func (m *Manager) resultsSignature(endpoint, expires, name, jobID, artifactID string) string {
	h := hmac.New(sha256.New, m.auth.DerivedKey(artifactURLPurpose))
	h.Write([]byte(strings.Join([]string{"results", endpoint, expires, name, jobID, artifactID}, "\x00")))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// Signed link to one endpoint, bound to the job and artifact
func (m *Manager) resultsURL(r *http.Request, endpoint, name, jobID, artifactID string) string {
	expires := time.Now().Add(resultsURLTTL).UTC().Format(time.RFC3339)
	q := url.Values{}
	q.Set("sig", m.resultsSignature(endpoint, expires, name, jobID, artifactID))
	q.Set("expires", expires)
	q.Set("artifactName", name)
	q.Set("taskID", jobID)
	q.Set("artifactID", artifactID)
	return strings.TrimRight(m.git.BaseURL(r.Context()), "/") + ResultsAPIPrefix + "/" + endpoint + "?" + q.Encode()
}

// Verifies a signed link, answers the artifact it names
func (m *Manager) resultsVerify(w http.ResponseWriter, r *http.Request, endpoint string) (*db.ActionsArtifact, bool) {
	q := r.URL.Query()
	expires, name, jobID, artifactID := q.Get("expires"), q.Get("artifactName"), q.Get("taskID"), q.Get("artifactID")
	if !hmac.Equal([]byte(q.Get("sig")), []byte(m.resultsSignature(endpoint, expires, name, jobID, artifactID))) {
		twirpError(w, "unauthenticated", "bad signature")
		return nil, false
	}
	t, err := time.Parse(time.RFC3339, expires)
	if err != nil || t.Before(time.Now()) {
		twirpError(w, "unauthenticated", "link expired")
		return nil, false
	}
	scope, err := m.resultsScopeOf(r, jobID)
	if err != nil {
		twirpError(w, "unauthenticated", err.Error())
		return nil, false
	}
	a, err := m.store.GetActionsArtifact(r.Context(), artifactID)
	if err != nil {
		twirpError(w, "internal", err.Error())
		return nil, false
	}
	if a == nil || a.RunID != scope.run.ID || a.Name != name {
		twirpError(w, "not_found", "artifact not found")
		return nil, false
	}
	return a, true
}

// Expiry a results request asks for, unset when the timestamp is missing
func requestedExpiry(ts *timestamppb.Timestamp) *time.Time {
	if ts == nil || !ts.IsValid() {
		return nil
	}
	t := ts.AsTime().UTC()
	return &t
}

func (m *Manager) resultsCreate(w http.ResponseWriter, r *http.Request) {
	scope, ok := m.resultsAuth(w, r)
	if !ok {
		return
	}
	var req resultsv1.CreateArtifactRequest
	if !readProto(w, r, &req) || !scope.checkRun(w, req.GetWorkflowRunBackendId()) {
		return
	}
	if err := ValidateArtifactName(req.GetName()); err != nil {
		twirpError(w, "invalid_argument", err.Error())
		return
	}
	ctx := r.Context()
	contentType := artifactZipType
	if mt := req.GetMimeType().GetValue(); mt != "" {
		if parsed, _, err := mime.ParseMediaType(mt); err == nil && strings.Contains(parsed, "/") {
			contentType = parsed
		}
	}
	a, err := m.claimArtifact(ctx, scope, req.GetName(), contentType, m.artifactExpiry(ctx, scope.repo, requestedExpiry(req.GetExpiresAt())), true)
	if err != nil {
		twirpError(w, "internal", err.Error())
		return
	}
	if a.Status == ArtifactConfirmed {
		twirpError(w, "already_exists", fmt.Sprintf("an artifact with this name already exists on the workflow run: %s", req.GetName()))
		return
	}
	writeProto(w, &resultsv1.CreateArtifactResponse{Ok: true, SignedUploadUrl: m.resultsURL(r, "UploadArtifact", a.Name, scope.job.ID, a.ID)})
}

// Block files sort by the order the block list names them
type blockList struct {
	Latest []string `xml:"Latest"`
}

func blockFileName(blockID string) string {
	return "block-" + base64.RawURLEncoding.EncodeToString([]byte(blockID))
}

func (m *Manager) resultsUpload(w http.ResponseWriter, r *http.Request) {
	a, ok := m.resultsVerify(w, r, "UploadArtifact")
	if !ok {
		return
	}
	if a.Status != ArtifactPending {
		twirpError(w, "failed_precondition", "artifact already finalized")
		return
	}
	dir := m.artifactBlockDir(a.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		twirpError(w, "internal", err.Error())
		return
	}
	save := func(name string, body io.Reader, flags int) error {
		f, err := os.OpenFile(filepath.Join(dir, name), flags, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(f, body)
		return err
	}
	switch comp := r.URL.Query().Get("comp"); comp {
	case "block", "appendBlock":
		// Without a block id the toolkit streams one growing block
		blockID := r.URL.Query().Get("blockid")
		if comp == "appendBlock" || blockID == "" {
			if err := save("append", r.Body, os.O_CREATE|os.O_WRONLY|os.O_APPEND); err != nil {
				twirpError(w, "internal", err.Error())
				return
			}
			break
		}
		if err := save(blockFileName(blockID), r.Body, os.O_CREATE|os.O_WRONLY|os.O_TRUNC); err != nil {
			twirpError(w, "internal", err.Error())
			return
		}
	case "blocklist":
		if err := save("blocklist.xml", r.Body, os.O_CREATE|os.O_WRONLY|os.O_TRUNC); err != nil {
			twirpError(w, "internal", err.Error())
			return
		}
	default:
		twirpError(w, "invalid_argument", "unknown comp "+comp)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write([]byte(`"ok"`))
}

// Block files in upload order, block list wins
func (m *Manager) orderedBlocks(id string) ([]string, error) {
	dir := m.artifactBlockDir(id)
	if raw, err := os.ReadFile(filepath.Join(dir, "blocklist.xml")); err == nil {
		var list blockList
		if err := xml.Unmarshal(raw, &list); err != nil {
			return nil, fmt.Errorf("block list: %w", err)
		}
		out := make([]string, 0, len(list.Latest))
		for _, blockID := range list.Latest {
			p := filepath.Join(dir, blockFileName(blockID))
			if _, err := os.Stat(p); err != nil {
				return nil, fmt.Errorf("block %q was never uploaded", blockID)
			}
			out = append(out, p)
		}
		return out, nil
	}
	if _, err := os.Stat(filepath.Join(dir, "append")); err == nil {
		return []string{filepath.Join(dir, "append")}, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, errors.New("no data was uploaded")
	}
	var blocks []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "block-") {
			blocks = append(blocks, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(blocks)
	switch len(blocks) {
	case 0:
		return nil, errors.New("no data was uploaded")
	case 1:
		return blocks, nil
	}
	return nil, errors.New("a block list is required for more than one block")
}

func (m *Manager) resultsFinalize(w http.ResponseWriter, r *http.Request) {
	scope, ok := m.resultsAuth(w, r)
	if !ok {
		return
	}
	var req resultsv1.FinalizeArtifactRequest
	if !readProto(w, r, &req) || !scope.checkRun(w, req.GetWorkflowRunBackendId()) {
		return
	}
	ctx := r.Context()
	a, err := m.store.GetActionsArtifactByName(ctx, scope.run.ID, req.GetName())
	if err != nil {
		twirpError(w, "internal", err.Error())
		return
	}
	if a == nil {
		twirpError(w, "not_found", "artifact not found")
		return
	}
	if a.Status == ArtifactConfirmed {
		writeProto(w, &resultsv1.FinalizeArtifactResponse{Ok: true, ArtifactId: a.Number})
		return
	}
	blocks, err := m.orderedBlocks(a.ID)
	if err != nil {
		twirpError(w, "failed_precondition", err.Error())
		return
	}
	blobs := m.artifacts.Blobs()
	uploadID, err := blobs.InitiateUpload()
	if err != nil {
		twirpError(w, "internal", err.Error())
		return
	}
	var total int64
	for _, p := range blocks {
		f, err := os.Open(p)
		if err != nil {
			_ = blobs.CancelUpload(uploadID)
			twirpError(w, "internal", err.Error())
			return
		}
		n, err := blobs.AppendChunk(uploadID, f)
		f.Close()
		if err != nil {
			_ = blobs.CancelUpload(uploadID)
			twirpError(w, "internal", err.Error())
			return
		}
		total += n
	}
	if req.GetSize() != total {
		_ = blobs.CancelUpload(uploadID)
		twirpError(w, "failed_precondition", fmt.Sprintf("uploaded %d bytes, the action announced %d", total, req.GetSize()))
		return
	}
	digest, size, _, err := blobs.CompleteUpload(uploadID)
	if err != nil {
		twirpError(w, "internal", err.Error())
		return
	}
	if want := req.GetHash().GetValue(); want != "" && !strings.EqualFold(want, digest) {
		m.gcArtifactBlob(ctx, digest)
		twirpError(w, "failed_precondition", fmt.Sprintf("checksum %s does not match the uploaded data %s", want, digest))
		return
	}
	cols := map[string]any{"digest": digest, "size": size, "status": ArtifactConfirmed}
	if err := m.store.UpdateActionsArtifactColumns(ctx, a.ID, cols); err != nil {
		twirpError(w, "internal", err.Error())
		return
	}
	_ = os.RemoveAll(m.artifactBlockDir(a.ID))
	writeProto(w, &resultsv1.FinalizeArtifactResponse{Ok: true, ArtifactId: a.Number})
}

func (m *Manager) resultsList(w http.ResponseWriter, r *http.Request) {
	scope, ok := m.resultsAuth(w, r)
	if !ok {
		return
	}
	var req resultsv1.ListArtifactsRequest
	if !readProto(w, r, &req) || !scope.checkRun(w, req.GetWorkflowRunBackendId()) {
		return
	}
	rows, err := m.RunArtifacts(r.Context(), scope.run)
	if err != nil {
		twirpError(w, "internal", err.Error())
		return
	}
	resp := &resultsv1.ListArtifactsResponse{}
	for _, a := range rows {
		if req.NameFilter != nil && a.Name != req.GetNameFilter().GetValue() {
			continue
		}
		if req.IdFilter != nil && a.Number != req.GetIdFilter().GetValue() {
			continue
		}
		// GitHub echoes the caller's job, not the uploader's
		resp.Artifacts = append(resp.Artifacts, &resultsv1.ListArtifactsResponse_MonolithArtifact{
			WorkflowRunBackendId: scope.run.ID, WorkflowJobRunBackendId: req.GetWorkflowJobRunBackendId(), DatabaseId: a.Number,
			Name: a.Name, Size: a.Size, CreatedAt: timestamppb.New(a.CreatedAt),
		})
	}
	writeProto(w, resp)
}

func (m *Manager) resultsSignedURL(w http.ResponseWriter, r *http.Request) {
	scope, ok := m.resultsAuth(w, r)
	if !ok {
		return
	}
	var req resultsv1.GetSignedArtifactURLRequest
	if !readProto(w, r, &req) || !scope.checkRun(w, req.GetWorkflowRunBackendId()) {
		return
	}
	a, err := m.store.GetActionsArtifactByName(r.Context(), scope.run.ID, req.GetName())
	if err != nil {
		twirpError(w, "internal", err.Error())
		return
	}
	if !artifactLive(a) {
		twirpError(w, "not_found", "artifact not found")
		return
	}
	writeProto(w, &resultsv1.GetSignedArtifactURLResponse{SignedUrl: m.resultsURL(r, "DownloadArtifact", a.Name, scope.job.ID, a.ID)})
}

func (m *Manager) resultsDownload(w http.ResponseWriter, r *http.Request) {
	a, ok := m.resultsVerify(w, r, "DownloadArtifact")
	if !ok {
		return
	}
	m.serveConfirmedArtifact(w, r, a)
}

func (m *Manager) resultsDelete(w http.ResponseWriter, r *http.Request) {
	scope, ok := m.resultsAuth(w, r)
	if !ok {
		return
	}
	var req resultsv1.DeleteArtifactRequest
	if !readProto(w, r, &req) || !scope.checkRun(w, req.GetWorkflowRunBackendId()) {
		return
	}
	a, err := m.store.GetActionsArtifactByName(r.Context(), scope.run.ID, req.GetName())
	if err != nil {
		twirpError(w, "internal", err.Error())
		return
	}
	if a == nil {
		twirpError(w, "not_found", "artifact not found")
		return
	}
	if err := m.deleteArtifact(r.Context(), a); err != nil {
		twirpError(w, "internal", err.Error())
		return
	}
	writeProto(w, &resultsv1.DeleteArtifactResponse{Ok: true, ArtifactId: a.Number})
}

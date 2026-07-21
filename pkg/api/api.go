package api

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Server clamps page sizes here
const maxPageSize = 500

// Keeps the v1 cli artifact JSON shape
type Artifact struct {
	ID         string            `json:"id"`
	RepoID     int64             `json:"repo_id"`
	Name       string            `json:"name"`
	Path       string            `json:"path"`
	UploadID   string            `json:"upload_id"`
	Version    string            `json:"version"`
	Size       int64             `json:"size"`
	MimeType   string            `json:"mime_type"`
	Metadata   string            `json:"metadata"`
	Properties map[string]string `json:"properties"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

// Keeps the v1 cli repo JSON shape
type ArtifactRepository struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Namespace   string    `json:"namespace"`
	FullName    string    `json:"full_name"`
	Description string    `json:"description"`
	Owner       string    `json:"owner"`
	Private     bool      `json:"private"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Keeps the v1 cli search JSON shape
type SearchResponse struct {
	Results []Artifact `json:"results"`
	Total   int        `json:"total"`
	Limit   int        `json:"limit"`
	Offset  int        `json:"offset"`
	Sort    string     `json:"sort"`
	Order   string     `json:"order"`
}

func protoTime(ts *timestamppb.Timestamp) time.Time {
	if ts == nil {
		return time.Time{}
	}
	return ts.AsTime()
}

func artifactFromProto(a *v1.Artifact) Artifact {
	props := a.GetProperties()
	if props == nil {
		props = map[string]string{}
	}
	return Artifact{
		ID:         a.GetId(),
		RepoID:     a.GetRepoId(),
		Name:       a.GetName(),
		Path:       a.GetPath(),
		UploadID:   a.GetUploadId(),
		Version:    a.GetVersion(),
		Size:       a.GetSize(),
		MimeType:   a.GetMimeType(),
		Metadata:   a.GetMetadata(),
		Properties: props,
		CreatedAt:  protoTime(a.GetCreatedAt()),
		UpdatedAt:  protoTime(a.GetUpdatedAt()),
	}
}

func repoFromProto(r *v1.ArtifactRepository) ArtifactRepository {
	return ArtifactRepository{
		ID:          r.GetId(),
		Name:        r.GetName(),
		Namespace:   r.GetNamespace(),
		FullName:    r.GetFullName(),
		Description: r.GetDescription(),
		Owner:       r.GetOwner(),
		Private:     r.GetIsPrivate(),
		CreatedAt:   protoTime(r.GetCreatedAt()),
		UpdatedAt:   protoTime(r.GetUpdatedAt()),
	}
}

// ── Repo references ──────────────────────────────────────────────────────

// Repo name with optional namespace qualifier
type RepoRef struct {
	Namespace string
	Name      string
}

func parseRepoRef(s string) RepoRef {
	if ns, name, ok := strings.Cut(s, "/"); ok && ns != "" && name != "" {
		return RepoRef{Namespace: ns, Name: name}
	}
	return RepoRef{Name: strings.Trim(s, "/")}
}

func (r RepoRef) String() string {
	if r.Namespace != "" {
		return r.Namespace + "/" + r.Name
	}
	return r.Name
}

// Data plane path, qualified refs route through the namespace marker
func (r RepoRef) basePath() string {
	if r.Namespace != "" {
		return "/api/v1/artifacts/_ns/" + url.PathEscape(r.Namespace) + "/" + url.PathEscape(r.Name)
	}
	return "/api/v1/artifacts/" + url.PathEscape(r.Name)
}

// ── Repositories ─────────────────────────────────────────────────────────

func (c *Client) createArtifactRepo(ctx context.Context, ref RepoRef, description string, private bool) (ArtifactRepository, error) {
	resp, err := c.Artifacts().CreateArtifactRepository(ctx, connect.NewRequest(&v1.CreateArtifactRepositoryRequest{
		Name:        ref.Name,
		Namespace:   ref.Namespace,
		Description: description,
		IsPrivate:   private,
	}))
	if err != nil {
		return ArtifactRepository{}, rpcErr(err)
	}
	return repoFromProto(resp.Msg.GetRepository()), nil
}

func (c *Client) listArtifactRepos(ctx context.Context, namespace string) ([]ArtifactRepository, error) {
	rpc := c.Artifacts()
	var repos []ArtifactRepository
	token := ""
	for {
		resp, err := rpc.ListArtifactRepositories(ctx, connect.NewRequest(&v1.ListArtifactRepositoriesRequest{
			Namespace: namespace,
			Page:      &v1.PageRequest{PageSize: maxPageSize, PageToken: token},
		}))
		if err != nil {
			return nil, rpcErr(err)
		}
		for _, r := range resp.Msg.Repositories {
			repos = append(repos, repoFromProto(r))
		}
		token = resp.Msg.GetPage().GetNextPageToken()
		if token == "" {
			return repos, nil
		}
	}
}

// ── Artifacts ────────────────────────────────────────────────────────────

// Rpc bookends the transfer, bytes stream over http
func (c *Client) uploadArtifact(ctx context.Context, ref RepoRef, filePath, version, artifactPath string, properties map[string]string) error {
	rpc := c.Artifacts()

	initResp, err := rpc.InitiateArtifactUpload(ctx, connect.NewRequest(&v1.InitiateArtifactUploadRequest{
		RepoName:  ref.Name,
		Namespace: ref.Namespace,
	}))
	if err != nil {
		return rpcErr(err)
	}
	uploadURL := initResp.Msg.GetUploadUrl()
	if uploadURL == "" {
		return fmt.Errorf("server did not return an upload location")
	}

	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	resp, err := c.doData(ctx, http.MethodPatch, uploadURL, file)
	if err != nil {
		return err
	}
	resp.Body.Close()

	_, err = rpc.CompleteArtifactUpload(ctx, connect.NewRequest(&v1.CompleteArtifactUploadRequest{
		RepoName:   ref.Name,
		Namespace:  ref.Namespace,
		UploadId:   initResp.Msg.GetUploadId(),
		Version:    version,
		Path:       artifactPath,
		Properties: properties,
	}))
	if err != nil {
		return rpcErr(err)
	}
	return nil
}

// Archive streaming has no rpc, the v1 query route is the data plane
func (c *Client) downloadArtifacts(ctx context.Context, ref RepoRef, q url.Values, outputPath string, unpack, flat bool, format string) error {
	endpoint := ref.basePath() + "/query"
	if len(q) > 0 {
		endpoint += "?" + q.Encode()
	}

	resp, err := c.doData(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	tempFile, err := os.CreateTemp("", "dfcli-download-*")
	if err != nil {
		return err
	}
	defer os.Remove(tempFile.Name())

	if _, err := io.Copy(tempFile, resp.Body); err != nil {
		return err
	}
	tempFile.Close()

	if err := os.MkdirAll(outputPath, 0755); err != nil {
		return err
	}

	if !unpack {
		finalPath := outputPath
		if info, err := os.Stat(outputPath); err == nil && info.IsDir() {
			finalPath = filepath.Join(outputPath, fmt.Sprintf("%s_artifacts.%s", ref.Name, format))
		}
		return moveFile(tempFile.Name(), finalPath)
	}

	if err := recursivelyUnpack(tempFile.Name(), outputPath, flat); err != nil {
		return fmt.Errorf("failed to unpack archive: %w", err)
	}
	return nil
}

func (c *Client) deleteArtifact(ctx context.Context, ref RepoRef, version, path string) error {
	_, err := c.Artifacts().DeleteArtifact(ctx, connect.NewRequest(&v1.DeleteArtifactRequest{
		RepoName:  ref.Name,
		Namespace: ref.Namespace,
		Version:   version,
		Path:      path,
	}))
	if err != nil {
		return rpcErr(err)
	}
	return nil
}

// ── Search ───────────────────────────────────────────────────────────────

var artifactSortFields = map[string]bool{
	"name": true, "version": true, "path": true,
	"size": true, "created_at": true, "updated_at": true,
}

type SearchOptions struct {
	Ref        RepoRef
	Name       string
	Version    string
	Path       string
	Properties map[string]string
	Num        int // Zero fetches everything
	Offset     int
	Sort       string
	Order      string
}

func (o SearchOptions) query() *v1.Query {
	q := &v1.Query{}
	for _, f := range []struct{ field, value string }{
		{"name", o.Name}, {"version", o.Version}, {"path", o.Path},
	} {
		if f.value != "" {
			q.Filters = append(q.Filters, &v1.FieldFilter{Field: f.field, Value: f.value})
		}
	}
	if len(q.Filters) == 0 {
		return nil
	}
	return q
}

func (c *Client) searchArtifacts(ctx context.Context, opts SearchOptions) (*SearchResponse, error) {
	sortField := opts.Sort
	if sortField == "" {
		sortField = "created_at"
	}
	if !artifactSortFields[sortField] {
		return nil, fmt.Errorf("invalid sort field %q", sortField)
	}
	order := strings.ToUpper(opts.Order)
	if order != "ASC" && order != "DESC" {
		order = "DESC"
	}

	pageSize := int32(maxPageSize)
	if opts.Num > 0 && opts.Num < maxPageSize {
		pageSize = int32(opts.Num)
	}
	token := ""
	if opts.Offset > 0 {
		token = base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(opts.Offset)))
	}

	rpc := c.Artifacts()
	results := []Artifact{}
	for {
		resp, err := rpc.SearchArtifacts(ctx, connect.NewRequest(&v1.SearchArtifactsRequest{
			RepoName:   opts.Ref.Name,
			Namespace:  opts.Ref.Namespace,
			Properties: opts.Properties,
			Page: &v1.PageRequest{
				PageSize:  pageSize,
				PageToken: token,
				Query:     opts.query(),
				OrderBy:   strings.ToLower(sortField + " " + order),
			},
		}))
		if err != nil {
			return nil, rpcErr(err)
		}

		for _, a := range resp.Msg.Artifacts {
			results = append(results, artifactFromProto(a))
		}
		token = resp.Msg.GetPage().GetNextPageToken()

		if token == "" || (opts.Num > 0 && len(results) >= opts.Num) {
			break
		}
	}
	if opts.Num > 0 && len(results) > opts.Num {
		results = results[:opts.Num]
	}

	// V1 facade quirks, total is row count and offset zero
	return &SearchResponse{
		Results: results,
		Total:   len(results),
		Limit:   len(results),
		Offset:  0,
		Sort:    sortField,
		Order:   order,
	}, nil
}

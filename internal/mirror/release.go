package mirror

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// One way to download an asset, tried in order
type assetSource struct {
	url     string
	headers map[string]string
}

// One downloadable file within a release
type asset struct {
	name string
	size int64
	// Preferred first, quota burning fallbacks last
	sources []assetSource
}

// One published version upstream
type release struct {
	version    string
	prerelease bool
	assets     []asset
}

// Release listing plus the conditional request cursor
type releaseList struct {
	releases []release
	// Cursor for the next poll, empty when unsupported
	etag string
	// Upstream said nothing changed since the cursor
	notModified bool
}

// A release hosting network the artifact monitor can poll
type releaseDriver interface {
	// Confirms the upstream exists and credentials work
	validate(ctx context.Context, c *http.Client, cfg *v1.MirrorConfig) error
	// Newest first, drafts excluded, 304 short circuits via prevETag
	releases(ctx context.Context, c *http.Client, cfg *v1.MirrorConfig, prevETag string) (releaseList, error)
}

func driverFor(t v1.ArtifactRepoType) releaseDriver {
	switch t {
	case v1.ArtifactRepoType_ARTIFACT_REPO_TYPE_GITHUB_RELEASES:
		return githubDriver{}
	case v1.ArtifactRepoType_ARTIFACT_REPO_TYPE_GITLAB_RELEASES:
		return gitlabDriver{}
	case v1.ArtifactRepoType_ARTIFACT_REPO_TYPE_GITEA_RELEASES:
		return giteaDriver{}
	default:
		return nil
	}
}

// MirrorArtifactTypes are the repo types the sweep monitors
var MirrorArtifactTypes = []v1.ArtifactRepoType{
	v1.ArtifactRepoType_ARTIFACT_REPO_TYPE_GITHUB_RELEASES,
	v1.ArtifactRepoType_ARTIFACT_REPO_TYPE_GITLAB_RELEASES,
	v1.ArtifactRepoType_ARTIFACT_REPO_TYPE_GITEA_RELEASES,
}

func fetchJSON(ctx context.Context, c *http.Client, url string, headers map[string]string, out any) error {
	_, _, err := fetchJSONConditional(ctx, c, url, headers, "", out)
	return err
}

// Conditional get, a 304 against prevETag skips decoding entirely
func fetchJSONConditional(ctx context.Context, c *http.Client, url string, headers map[string]string, prevETag string, out any) (etag string, notModified bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", false, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if prevETag != "" {
		req.Header.Set("If-None-Match", prevETag)
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return prevETag, true, nil
	}
	if err := classifyResponse(resp, url); err != nil {
		return "", false, err
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return "", false, err
	}
	return resp.Header.Get("ETag"), false, nil
}

// Non 200 answer from the upstream api
type upstreamError struct {
	status int
	body   string
	url    string
}

func (e *upstreamError) Error() string {
	return fmt.Sprintf("upstream returned HTTP %d for %s", e.status, e.url)
}

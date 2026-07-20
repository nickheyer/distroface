package mirror

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Gitea caps release pages at fifty entries
const giteaPageSize = 50

// Speaks the gitea api, covers forgejo and codeberg too
type giteaDriver struct{}

// Accepts owner/repo (codeberg.org assumed) or a full instance url
func giteaProject(upstream string) (apiBase, slug string, err error) {
	s := strings.TrimSpace(upstream)
	scheme := "https"
	if strings.HasPrefix(s, "http://") {
		scheme = "http"
	}
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimSuffix(strings.Trim(s, "/"), ".git")

	host := "codeberg.org"
	if i := strings.Index(s, "/"); i > 0 && strings.Contains(s[:i], ".") {
		host, s = s[:i], s[i+1:]
	}
	parts := strings.Split(s, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("%w: upstream must be owner/repo or a gitea instance url", ErrInvalid)
	}
	return fmt.Sprintf("%s://%s/api/v1", scheme, host),
		url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]), nil
}

func giteaHeaders(cfg *v1.MirrorConfig) map[string]string {
	h := map[string]string{}
	if t := cfg.GetAuthToken(); t != "" {
		h["Authorization"] = "token " + t
	}
	return h
}

func (giteaDriver) validate(ctx context.Context, c *http.Client, cfg *v1.MirrorConfig) error {
	base, slug, err := giteaProject(cfg.GetUpstream())
	if err != nil {
		return err
	}
	var probe struct {
		FullName string `json:"full_name"`
	}
	err = fetchJSON(ctx, c, base+"/repos/"+slug, giteaHeaders(cfg), &probe)
	if _, limited := RetryAfter(err); limited {
		return fmt.Errorf("the gitea instance rate limited this server, wait before validating again: %w", err)
	}
	if ue, ok := err.(*upstreamError); ok {
		switch ue.status {
		case http.StatusNotFound:
			return fmt.Errorf("%w: repository %q not found on the gitea instance (private repos need a token with read access)", ErrInvalid, cfg.GetUpstream())
		case http.StatusUnauthorized, http.StatusForbidden:
			return fmt.Errorf("%w: the gitea instance rejected the token", ErrInvalid)
		}
	}
	if err != nil {
		return fmt.Errorf("gitea validation failed: %w", err)
	}
	return nil
}

type giteaRelease struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name               string `json:"name"`
		Size               int64  `json:"size"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func (giteaDriver) releases(ctx context.Context, c *http.Client, cfg *v1.MirrorConfig, prevETag string) (releaseList, error) {
	base, slug, err := giteaProject(cfg.GetUpstream())
	if err != nil {
		return releaseList{}, err
	}
	apiHost := ""
	if bu, err := url.Parse(base); err == nil {
		apiHost = bu.Host
	}

	headers := giteaHeaders(cfg)
	var out releaseList
	for page := 1; page <= maxReleasePages; page++ {
		var batch []giteaRelease
		u := fmt.Sprintf("%s/repos/%s/releases?page=%d&limit=%d", base, slug, page, giteaPageSize)
		if page == 1 {
			etag, notModified, err := fetchJSONConditional(ctx, c, u, headers, prevETag, &batch)
			if err != nil {
				return releaseList{}, err
			}
			if notModified {
				return releaseList{etag: prevETag, notModified: true}, nil
			}
			out.etag = etag
		} else if err := fetchJSON(ctx, c, u, headers, &batch); err != nil {
			return releaseList{}, err
		}
		for _, gr := range batch {
			if gr.Draft || gr.TagName == "" {
				continue
			}
			rel := release{version: gr.TagName, prerelease: gr.Prerelease}
			for _, a := range gr.Assets {
				if a.BrowserDownloadURL == "" {
					continue
				}
				// Token stays on the gitea host, attachments live there
				dlHeaders := map[string]string{}
				if du, err := url.Parse(a.BrowserDownloadURL); err == nil && du.Host == apiHost {
					dlHeaders = headers
				}
				rel.assets = append(rel.assets, asset{
					name:    a.Name,
					size:    a.Size,
					sources: []assetSource{{url: a.BrowserDownloadURL, headers: dlHeaders}},
				})
			}
			out.releases = append(out.releases, rel)
		}
		if len(batch) < giteaPageSize {
			break
		}
	}
	return out, nil
}

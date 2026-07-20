package mirror

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

const githubAPI = "https://api.github.com"

// Release listing pages fetched per sync at most
const maxReleasePages = 10

type githubDriver struct{}

// Accepts owner/repo, github.com/owner/repo, or a full url
func githubSlug(upstream string) (string, error) {
	s := strings.TrimSpace(upstream)
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "www.")
	s = strings.TrimPrefix(s, "github.com/")
	s = strings.TrimSuffix(strings.Trim(s, "/"), ".git")
	parts := strings.Split(s, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("%w: upstream must be owner/repo or a github.com url", ErrInvalid)
	}
	return url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]), nil
}

func githubHeaders(cfg *v1.MirrorConfig) map[string]string {
	h := map[string]string{
		"Accept":               "application/vnd.github+json",
		"X-GitHub-Api-Version": "2022-11-28",
	}
	if t := cfg.GetAuthToken(); t != "" {
		h["Authorization"] = "Bearer " + t
	}
	return h
}

func (githubDriver) validate(ctx context.Context, c *http.Client, cfg *v1.MirrorConfig) error {
	slug, err := githubSlug(cfg.GetUpstream())
	if err != nil {
		return err
	}
	var probe struct {
		FullName string `json:"full_name"`
	}
	err = fetchJSON(ctx, c, githubAPI+"/repos/"+slug, githubHeaders(cfg), &probe)
	if _, limited := RetryAfter(err); limited {
		return fmt.Errorf("github rate limited this server, wait before validating again: %w", err)
	}
	if ue, ok := err.(*upstreamError); ok {
		switch ue.status {
		case http.StatusNotFound:
			return fmt.Errorf("%w: github repository %q not found (private repos need a token with repo read access)", ErrInvalid, cfg.GetUpstream())
		case http.StatusUnauthorized:
			return fmt.Errorf("%w: github rejected the token", ErrInvalid)
		case http.StatusForbidden:
			return fmt.Errorf("%w: github denied access (token lacks scope)", ErrInvalid)
		}
	}
	if err != nil {
		return fmt.Errorf("github validation failed: %w", err)
	}
	return nil
}

type githubRelease struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name               string `json:"name"`
		URL                string `json:"url"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int64  `json:"size"`
	} `json:"assets"`
}

func (githubDriver) releases(ctx context.Context, c *http.Client, cfg *v1.MirrorConfig, prevETag string) (releaseList, error) {
	slug, err := githubSlug(cfg.GetUpstream())
	if err != nil {
		return releaseList{}, err
	}

	token := cfg.GetAuthToken()
	apiHeaders := map[string]string{"Accept": "application/octet-stream"}
	if token != "" {
		apiHeaders["Authorization"] = "Bearer " + token
	}

	var out releaseList
	for page := 1; page <= maxReleasePages; page++ {
		var batch []githubRelease
		u := fmt.Sprintf("%s/repos/%s/releases?per_page=100&page=%d", githubAPI, slug, page)
		if page == 1 {
			etag, notModified, err := fetchJSONConditional(ctx, c, u, githubHeaders(cfg), prevETag, &batch)
			if err != nil {
				return releaseList{}, err
			}
			if notModified {
				return releaseList{etag: prevETag, notModified: true}, nil
			}
			out.etag = etag
		} else if err := fetchJSON(ctx, c, u, githubHeaders(cfg), &batch); err != nil {
			return releaseList{}, err
		}
		for _, gr := range batch {
			if gr.Draft || gr.TagName == "" {
				continue
			}
			rel := release{version: gr.TagName, prerelease: gr.Prerelease}
			for _, a := range gr.Assets {
				// Cdn first, quota free and works for public repos
				var sources []assetSource
				if a.BrowserDownloadURL != "" {
					sources = append(sources, assetSource{url: a.BrowserDownloadURL})
				}
				// Api endpoint reaches private repo assets with the token
				if token != "" && a.URL != "" {
					sources = append(sources, assetSource{url: a.URL, headers: apiHeaders})
				}
				if len(sources) == 0 && a.URL != "" {
					sources = append(sources, assetSource{url: a.URL, headers: apiHeaders})
				}
				rel.assets = append(rel.assets, asset{name: a.Name, size: a.Size, sources: sources})
			}
			out.releases = append(out.releases, rel)
		}
		if len(batch) < 100 {
			break
		}
	}
	return out, nil
}

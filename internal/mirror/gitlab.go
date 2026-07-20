package mirror

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

type gitlabDriver struct{}

// Accepts group/project, gitlab.com/group/project, or a self hosted url
func gitlabProject(upstream string) (apiBase, project string, err error) {
	s := strings.TrimSpace(upstream)
	scheme := "https"
	if strings.HasPrefix(s, "http://") {
		scheme = "http"
	}
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimSuffix(strings.Trim(s, "/"), ".git")

	host := "gitlab.com"
	if i := strings.Index(s, "/"); i > 0 && strings.Contains(s[:i], ".") {
		host, s = s[:i], s[i+1:]
	}
	if s == "" || !strings.Contains(s, "/") {
		return "", "", fmt.Errorf("%w: upstream must be group/project or a gitlab url", ErrInvalid)
	}
	return fmt.Sprintf("%s://%s/api/v4", scheme, host), url.PathEscape(s), nil
}

func gitlabHeaders(cfg *v1.MirrorConfig) map[string]string {
	h := map[string]string{}
	if t := cfg.GetAuthToken(); t != "" {
		h["PRIVATE-TOKEN"] = t
	}
	return h
}

func (gitlabDriver) validate(ctx context.Context, c *http.Client, cfg *v1.MirrorConfig) error {
	base, project, err := gitlabProject(cfg.GetUpstream())
	if err != nil {
		return err
	}
	var probe struct {
		PathWithNamespace string `json:"path_with_namespace"`
	}
	err = fetchJSON(ctx, c, base+"/projects/"+project, gitlabHeaders(cfg), &probe)
	if _, limited := RetryAfter(err); limited {
		return fmt.Errorf("gitlab rate limited this server, wait before validating again: %w", err)
	}
	if ue, ok := err.(*upstreamError); ok {
		switch ue.status {
		case http.StatusNotFound:
			return fmt.Errorf("%w: gitlab project %q not found (private projects need a token with read_api scope)", ErrInvalid, cfg.GetUpstream())
		case http.StatusUnauthorized:
			return fmt.Errorf("%w: gitlab rejected the token", ErrInvalid)
		}
	}
	if err != nil {
		return fmt.Errorf("gitlab validation failed: %w", err)
	}
	return nil
}

type gitlabRelease struct {
	TagName         string `json:"tag_name"`
	UpcomingRelease bool   `json:"upcoming_release"`
	Assets          struct {
		Links []struct {
			Name           string `json:"name"`
			DirectAssetURL string `json:"direct_asset_url"`
			URL            string `json:"url"`
		} `json:"links"`
	} `json:"assets"`
}

func (gitlabDriver) releases(ctx context.Context, c *http.Client, cfg *v1.MirrorConfig, prevETag string) (releaseList, error) {
	base, project, err := gitlabProject(cfg.GetUpstream())
	if err != nil {
		return releaseList{}, err
	}
	apiHost := ""
	if bu, err := url.Parse(base); err == nil {
		apiHost = bu.Host
	}

	headers := gitlabHeaders(cfg)
	var out releaseList
	for page := 1; page <= maxReleasePages; page++ {
		var batch []gitlabRelease
		u := fmt.Sprintf("%s/projects/%s/releases?per_page=100&page=%d", base, project, page)
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
			if gr.TagName == "" {
				continue
			}
			rel := release{version: gr.TagName, prerelease: gr.UpcomingRelease}
			for _, l := range gr.Assets.Links {
				dl := l.DirectAssetURL
				if dl == "" {
					dl = l.URL
				}
				if dl == "" {
					continue
				}
				// Asset links point anywhere, the token stays on our instance
				dlHeaders := map[string]string{}
				if du, err := url.Parse(dl); err == nil && du.Host == apiHost {
					dlHeaders = headers
				}
				rel.assets = append(rel.assets, asset{
					name:    l.Name,
					sources: []assetSource{{url: dl, headers: dlHeaders}},
				})
			}
			out.releases = append(out.releases, rel)
		}
		if len(batch) < 100 {
			break
		}
	}
	return out, nil
}

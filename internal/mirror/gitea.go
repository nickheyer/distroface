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

// Accepts owner/repo (codeberg.org assumed) or any page url under the repo
func giteaProject(upstream string) (apiBase, slug string, err error) {
	s := strings.TrimSpace(upstream)
	scheme, host := "https", "codeberg.org"
	if strings.Contains(s, "://") {
		u, perr := url.Parse(s)
		if perr != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return "", "", fmt.Errorf("%w: %q is not an http(s) url", ErrInvalid, upstream)
		}
		scheme, host, s = u.Scheme, strings.ToLower(u.Host), u.Path
	} else if i := strings.Index(s, "/"); i > 0 && strings.Contains(s[:i], ".") && strings.Count(s, "/") >= 2 {
		host, s = s[:i], s[i+1:]
	}
	parts := strings.Split(strings.Trim(s, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("%w: upstream must be owner/repo or a repository url on the instance", ErrInvalid)
	}
	owner, repo := parts[0], strings.TrimSuffix(parts[1], ".git")
	return fmt.Sprintf("%s://%s/api/v1", scheme, host),
		url.PathEscape(owner) + "/" + url.PathEscape(repo), nil
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
	TagName    string     `json:"tag_name"`
	Draft      bool       `json:"draft"`
	Prerelease bool       `json:"prerelease"`
	ZipballURL string     `json:"zipball_url"`
	TarballURL string     `json:"tarball_url"`
	Assets     []struct { // the tar/zip src not included in assets
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
		apiHost = bu.Hostname()
	}
	repoName, _ := url.PathUnescape(slug[strings.LastIndex(slug, "/")+1:])

	headers := giteaHeaders(cfg)
	tokenFor := func(u string) map[string]string {
		if du, err := url.Parse(u); err == nil && strings.EqualFold(du.Hostname(), apiHost) {
			return headers
		}
		return map[string]string{}
	}
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
				rel.assets = append(rel.assets, asset{
					name:    a.Name,
					size:    a.Size,
					sources: []assetSource{{url: a.BrowserDownloadURL, headers: tokenFor(a.BrowserDownloadURL)}},
				})
			}
			for _, src := range []struct{ ext, url string }{{".zip", gr.ZipballURL}, {".tar.gz", gr.TarballURL}} {
				if src.url == "" {
					continue
				}
				rel.assets = append(rel.assets, asset{
					name:    repoName + "-" + gr.TagName + src.ext,
					sources: []assetSource{{url: src.url, headers: tokenFor(src.url)}},
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

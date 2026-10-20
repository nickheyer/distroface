package mirror

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/go-git/go-git/v6/plumbing"
	gittransport "github.com/go-git/go-git/v6/plumbing/transport"
	githttp "github.com/go-git/go-git/v6/plumbing/transport/http"
)

func TestParseGitUpstream(t *testing.T) {
	cases := []struct {
		in    string
		url   string
		host  string
		path  string
		forge string
		ssh   bool
	}{
		{"owner/repo", "https://github.com/owner/repo.git", "github.com", "owner/repo", forgeGitHub, false},
		{"gitlab.com/group/sub/project", "https://gitlab.com/group/sub/project.git", "gitlab.com", "group/sub/project", forgeGitLab, false},
		{"codeberg.org/owner/repo/", "https://codeberg.org/owner/repo.git", "codeberg.org", "owner/repo", forgeGitea, false},
		{"https://github.com/Owner/Repo.git", "https://github.com/Owner/Repo.git", "github.com", "Owner/Repo", forgeGitHub, false},
		{"https://git.example.org/team/app", "https://git.example.org/team/app", "git.example.org", "team/app", "", false},
		{"ssh://git@gitlab.com/group/project.git", "ssh://git@gitlab.com/group/project.git", "gitlab.com", "group/project", forgeGitLab, true},
		{"git@github.com:owner/repo.git", "ssh://git@github.com/owner/repo.git", "github.com", "owner/repo", forgeGitHub, true},
	}
	for _, c := range cases {
		up, err := parseGitUpstream(c.in)
		if err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if up.URL != c.url || up.Host != c.host || up.Path != c.path || up.Forge != c.forge || up.SSH != c.ssh {
			t.Errorf("%s: got %+v", c.in, up)
		}
	}
	for _, bad := range []string{"", "repo", "git://github.com/owner/repo.git", "https://github.com/", "file:///tmp/repo"} {
		if _, err := parseGitUpstream(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: expected ErrInvalid, got %v", bad, err)
		}
	}
}

func TestUpstreamHelpers(t *testing.T) {
	up, _ := parseGitUpstream("owner/repo")
	if up.wikiURL() != "https://github.com/owner/repo.wiki.git" {
		t.Errorf("wiki url %s", up.wikiURL())
	}
	if up.lfsBatchURL() != "https://github.com/owner/repo.git/info/lfs/objects/batch" {
		t.Errorf("lfs url %s", up.lfsBatchURL())
	}
	if up.apiBase() != githubAPI || up.releaseUpstream() != "owner/repo" || up.registryHost() != "ghcr.io" {
		t.Errorf("github helpers %s %s %s", up.apiBase(), up.releaseUpstream(), up.registryHost())
	}
	gl, _ := parseGitUpstream("https://gitlab.example.com/group/project")
	if gl.apiBase() != "https://gitlab.example.com/api/v4" || gl.registryHost() != "registry.gitlab.example.com" {
		t.Errorf("gitlab helpers %s %s", gl.apiBase(), gl.registryHost())
	}
	gt, _ := parseGitUpstream("https://codeberg.org/owner/repo")
	if gt.apiBase() != "https://codeberg.org/api/v1" || gt.releaseUpstream() != "https://codeberg.org/owner/repo" || gt.registryHost() != "codeberg.org" {
		t.Errorf("gitea helpers %s %s %s", gt.apiBase(), gt.releaseUpstream(), gt.registryHost())
	}
	ghe, _ := parseGitUpstream("https://github.corp.example/org/repo")
	if ghe.apiBase() != "https://github.corp.example/api/v3" {
		t.Errorf("enterprise api %s", ghe.apiBase())
	}
}

func TestUpstreamForForge(t *testing.T) {
	up, err := upstreamForForge(forgeGitea, "owner/repo")
	if err != nil || up.Host != "codeberg.org" || up.Forge != forgeGitea {
		t.Fatalf("gitea shorthand: %+v %v", up, err)
	}
	up, err = upstreamForForge(forgeGitLab, "https://gitlab.example.com/group/project")
	if err != nil || up.Host != "gitlab.example.com" || up.Forge != forgeGitLab {
		t.Fatalf("gitlab url: %+v %v", up, err)
	}
}

func TestGitBasicAuth(t *testing.T) {
	cases := []struct {
		forge, username, token, wantUser, wantPass string
	}{
		{forgeGitHub, "", "tok", "x-access-token", "tok"},
		{forgeGitLab, "", "tok", "oauth2", "tok"},
		{forgeGitea, "", "tok", "token", "tok"},
		{"", "", "tok", "token", "tok"},
		{forgeGitHub, "alice", "tok", "alice", "tok"},
		{forgeGitHub, "alice", "", "", ""},
	}
	for _, c := range cases {
		u, p := gitBasicAuth(c.forge, c.username, c.token)
		if u != c.wantUser || p != c.wantPass {
			t.Errorf("%s/%s/%s: got %s %s", c.forge, c.username, c.token, u, p)
		}
	}
}

func TestGitMirrorStateRoundTrip(t *testing.T) {
	if parseGitMirrorState("").Encode() != "" {
		t.Fatal("zero state must encode empty")
	}
	until := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	st := GitMirrorState{SSHPrivateKey: "PEM", SSHPublicKey: "ssh-ed25519 AAAA", HostKey: "SHA256:abc"}
	st.Failures = 2
	st.CooldownUntil = until
	raw := st.Encode()
	back := parseGitMirrorState(raw)
	if back != st {
		t.Fatalf("round trip mismatch: %+v vs %+v", back, st)
	}
	// Generic readers still see the cooldown in richer state
	if plain := ParseState(raw); plain.Failures != 2 || !plain.CooldownUntil.Equal(until) {
		t.Fatalf("ParseState lost fields: %+v", plain)
	}
	push := pushMirrorState{HostKey: "SHA256:xyz"}
	push.RateLimited = true
	if got := parsePushMirrorState(push.encode()); got != push {
		t.Fatalf("push state mismatch: %+v", got)
	}
	imp := parseImportState(`{"forge":"github","cursor":{"issues":3},"labels":{"bug":"id1"},"max_number":42}`)
	if imp.Forge != forgeGitHub || imp.Cursor["issues"] != 3 || imp.Labels["bug"] != "id1" || imp.MaxNumber != 42 {
		t.Fatalf("import state parse: %+v", imp)
	}
	if imp.Pulls == nil || imp.Counts == nil || imp.Milestones == nil {
		t.Fatal("import state maps must be initialised")
	}
	if again := parseImportState(imp.encode()); again.Cursor["issues"] != 3 {
		t.Fatal("import state encode lost the cursor")
	}
}

func TestPatternRefspecs(t *testing.T) {
	refs := []*plumbing.Reference{
		plumbing.NewHashReference("refs/heads/main", plumbing.ZeroHash),
		plumbing.NewHashReference("refs/heads/release/1.0", plumbing.ZeroHash),
		plumbing.NewHashReference("refs/tags/v1.0", plumbing.ZeroHash),
		plumbing.NewHashReference("refs/tags/v1.0^{}", plumbing.ZeroHash),
		plumbing.NewHashReference("refs/pull/3/head", plumbing.ZeroHash),
		plumbing.NewSymbolicReference("HEAD", "refs/heads/main"),
	}
	specs, wanted := patternRefspecs(refs, "v*")
	if len(specs) != 1 || specs[0] != "+refs/tags/v1.0:refs/tags/v1.0" || !wanted["refs/tags/v1.0"] {
		t.Fatalf("v* -> %v", specs)
	}
	// A bare star stops at slashes like path.Match
	specs, _ = patternRefspecs(refs, "*")
	if len(specs) != 2 {
		t.Fatalf("* should keep main and v1.0, got %v", specs)
	}
	specs, _ = patternRefspecs(refs, "release/*")
	if len(specs) != 1 || specs[0] != "+refs/heads/release/1.0:refs/heads/release/1.0" {
		t.Fatalf("release/* -> %v", specs)
	}
}

func TestResolveImageRef(t *testing.T) {
	up, _ := parseGitUpstream("owner/repo")
	cases := []struct{ in, upstream, local string }{
		{"owner/repo", "ghcr.io/owner/repo", "repo"},
		{"web", "ghcr.io/owner/repo/web", "repo/web"},
		{"owner/repo/web:latest", "ghcr.io/owner/repo/web", "repo/web"},
		{"ghcr.io/other/image", "ghcr.io/other/image", "repo/image"},
		{"registry.example.com:5000/owner/repo/api", "registry.example.com:5000/owner/repo/api", "repo/api"},
	}
	for _, c := range cases {
		gotUp, gotLocal, err := resolveImageRef(up, "repo", c.in)
		if err != nil || gotUp != c.upstream || gotLocal != c.local {
			t.Errorf("%s: got %s %s %v", c.in, gotUp, gotLocal, err)
		}
	}
	for _, bad := range []string{"", "ghcr.io"} {
		if _, _, err := resolveImageRef(up, "repo", bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: expected ErrInvalid, got %v", bad, err)
		}
	}
}

func TestClassifyGitErr(t *testing.T) {
	setLimits(defaultLimits())
	u, _ := url.Parse("https://github.com/owner/repo.git/info/refs")
	limited := classifyGitErr(fmt.Errorf("fetch: %w", &githttp.Err{URL: u, Status: http.StatusTooManyRequests}))
	if _, ok := RetryAfter(limited); !ok {
		t.Fatalf("429 must become a rate limit, got %v", limited)
	}
	abuse := classifyGitErr(fmt.Errorf("%w: %w", gittransport.ErrAuthorizationFailed, &githttp.Err{URL: u, Status: http.StatusForbidden, Reason: "API rate limit exceeded"}))
	if _, ok := RetryAfter(abuse); !ok {
		t.Fatalf("403 rate limit text must become a rate limit, got %v", abuse)
	}
	denied := classifyGitErr(fmt.Errorf("%w: %w", gittransport.ErrAuthorizationFailed, &githttp.Err{URL: u, Status: http.StatusForbidden}))
	if _, ok := RetryAfter(denied); ok || !errors.Is(denied, gittransport.ErrAuthorizationFailed) {
		t.Fatalf("plain 403 must stay an auth failure, got %v", denied)
	}
	var nothing *nothingMirroredError
	if !errors.As(classifyGitErr(gittransport.ErrEmptyRemoteRepository), &nothing) {
		t.Fatal("empty upstream must be a nothing mirrored outcome")
	}
	if classifyGitErr(nil) != nil {
		t.Fatal("nil stays nil")
	}
}

func TestDescribeProbeError(t *testing.T) {
	up, _ := parseGitUpstream("owner/repo")
	if describeProbeError(up, gittransport.ErrEmptyRemoteRepository) != nil {
		t.Fatal("empty upstream is valid")
	}
	err := describeProbeError(up, fmt.Errorf("%w: %w", gittransport.ErrRepositoryNotFound, &githttp.Err{Status: 404}))
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("not found should be invalid: %v", err)
	}
}

func TestImportHelpers(t *testing.T) {
	if nextImportPhase(importPhaseFetch) != importPhaseWiki || nextImportPhase(importPhaseReleases) != importDone || nextImportPhase("bogus") != importDone {
		t.Fatal("phase order broken")
	}
	if normalizeColor("D73A4A") != "#d73a4a" || normalizeColor("#ededed") != "#ededed" || normalizeColor("") != "#ededed" {
		t.Fatal("color normalisation broken")
	}
	for in, want := range map[string]string{"APPROVED": "approved", "REQUEST_CHANGES": "changes_requested", "COMMENT": "commented", "DISMISSED": "commented"} {
		if got, ok := normalizeReviewState(in); !ok || got != want {
			t.Errorf("%s -> %s %v", in, got, ok)
		}
	}
	if _, ok := normalizeReviewState("PENDING"); ok {
		t.Error("pending reviews are skipped")
	}
	if !isHexHash("0123456789abcdef0123456789abcdef01234567") || isHexHash("main") {
		t.Error("hash detection broken")
	}
	if normalizeIssueState("opened") != "open" || normalizeIssueState("merged") != "closed" {
		t.Error("issue state normalisation broken")
	}
	if escapePath("group/sub project") != "group/sub%20project" {
		t.Error("path escaping broken")
	}
}

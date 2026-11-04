package ghapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/nickheyer/distroface/internal/forge"
	"github.com/nickheyer/distroface/internal/gitrepo"
)

func testHandler() *Handler {
	h := &Handler{mux: http.NewServeMux()}
	h.routes()
	return h
}

func TestRoutePatterns(t *testing.T) {
	h := testHandler()
	cases := map[string]string{
		"GET /user":                                         "GET /user",
		"GET /repos/o/r":                                    "GET /repos/{owner}/{repo}",
		"GET /repos/o/r/releases/latest":                    "GET /repos/{owner}/{repo}/releases/latest",
		"GET /repos/o/r/releases/tags/v1.2.3":               "/repos/{owner}/{repo}/releases/{a}/{b}",
		"GET /repos/o/r/releases/42":                        "GET /repos/{owner}/{repo}/releases/{id}",
		"POST /repos/o/r/releases/42/assets":                "/repos/{owner}/{repo}/releases/{a}/{b}",
		"DELETE /repos/o/r/releases/assets/7":               "/repos/{owner}/{repo}/releases/{a}/{b}",
		"POST /repos/o/r/issues/3/comments":                 "/repos/{owner}/{repo}/issues/{a}/{b}",
		"PATCH /repos/o/r/issues/comments/9":                "/repos/{owner}/{repo}/issues/{a}/{b}",
		"DELETE /repos/o/r/issues/3/labels/bug":             "DELETE /repos/{owner}/{repo}/issues/{number}/labels/{name}",
		"GET /repos/o/r/git/ref/heads/main":                 "GET /repos/{owner}/{repo}/git/ref/{ref...}",
		"GET /repos/o/r/git/refs/tags/v1":                   "GET /repos/{owner}/{repo}/git/refs/{ref...}",
		"GET /repos/o/r/contents/dir/file.txt":              "GET /repos/{owner}/{repo}/contents/{path...}",
		"GET /repos/o/r/commits/abc/status":                 "GET /repos/{owner}/{repo}/commits/{ref}/status",
		"GET /repos/o/r/commits/abc/statuses":               "GET /repos/{owner}/{repo}/commits/{ref}/statuses",
		"POST /repos/o/r/statuses/abc":                      "POST /repos/{owner}/{repo}/statuses/{sha}",
		"GET /repos/o/r/actions/runs/x/jobs":                "GET /repos/{owner}/{repo}/actions/runs/{run_id}/jobs",
		"GET /repos/o/r/branches/release/1.0":               "GET /repos/{owner}/{repo}/branches/{branch...}",
		"GET /meta":                                         "/",
		"GET /repos/o/r/nope":                               "/",
		"GET /repos/o/r/pulls/5":                            "GET /repos/{owner}/{repo}/pulls/{number}",
		"POST /repos/o/r/pulls":                             "POST /repos/{owner}/{repo}/pulls",
		"GET /rate_limit":                                   "GET /rate_limit",
		"GET /repos/o/r/issues?state=all&labels=a%2Cb":      "GET /repos/{owner}/{repo}/issues",
		"PUT /repos/o/r/issues/3/labels":                    "/repos/{owner}/{repo}/issues/{a}/{b}",
		"GET /repos/o/r/labels":                             "GET /repos/{owner}/{repo}/labels",
		"GET /repos/o/r/releases/42/assets?page=2":          "/repos/{owner}/{repo}/releases/{a}/{b}",
		"PATCH /repos/o/r/releases/assets/7":                "/repos/{owner}/{repo}/releases/{a}/{b}",
		"GET /repos/o/r/contents":                           "GET /repos/{owner}/{repo}/contents",
		"GET /repos/o/r/actions/runs/abc-def":               "GET /repos/{owner}/{repo}/actions/runs/{run_id}",
		"GET /repos/o/r/issues/3/labels":                    "/repos/{owner}/{repo}/issues/{a}/{b}",
		"POST /repos/o/r/issues":                            "POST /repos/{owner}/{repo}/issues",
		"PATCH /repos/o/r/issues/3":                         "PATCH /repos/{owner}/{repo}/issues/{number}",
		"GET /repos/o/r/pulls?state=open&head=o:feature":    "GET /repos/{owner}/{repo}/pulls",
		"GET /repos/o/r/issues/comments/9":                  "/repos/{owner}/{repo}/issues/{a}/{b}",
		"DELETE /repos/o/r/issues/comments/9":               "/repos/{owner}/{repo}/issues/{a}/{b}",
		"GET /repos/o/r/pulls/5/comments":                   "GET /repos/{owner}/{repo}/pulls/{number}/comments",
		"GET /repos/o/r/commits/main":                       "GET /repos/{owner}/{repo}/commits/{ref}",
		"POST /repos/o/r/releases":                          "POST /repos/{owner}/{repo}/releases",
		"GET /repos/o/r/releases?per_page=5":                "GET /repos/{owner}/{repo}/releases",
		"DELETE /repos/o/r/releases/42":                     "DELETE /repos/{owner}/{repo}/releases/{id}",
		"PATCH /repos/o/r/releases/42":                      "PATCH /repos/{owner}/{repo}/releases/{id}",
		"GET /repos/o/r/releases/assets/7":                  "/repos/{owner}/{repo}/releases/{a}/{b}",
		"GET /repos/o/r/issues/3":                           "GET /repos/{owner}/{repo}/issues/{number}",
		"GET /repos/o/r/issues/3/comments":                  "/repos/{owner}/{repo}/issues/{a}/{b}",
		"POST /repos/o/r/issues/3/labels":                   "/repos/{owner}/{repo}/issues/{a}/{b}",
		"POST /repos/o/r/pulls/5/comments":                  "POST /repos/{owner}/{repo}/pulls/{number}/comments",
		"GET /repos/o/r/contents/README.md?ref=feature/x":   "GET /repos/{owner}/{repo}/contents/{path...}",
		"GET /repos/o/r/git/ref/heads/feature/with/slashes": "GET /repos/{owner}/{repo}/git/ref/{ref...}",
	}
	for in, want := range cases {
		method, target, _ := strings.Cut(in, " ")
		req := httptest.NewRequest(method, target, nil)
		_, pattern := h.mux.Handler(req)
		if pattern != want {
			t.Errorf("%s matched %q, want %q", in, pattern, want)
		}
	}
}

func TestUnknownPathIsGitHubJSON(t *testing.T) {
	h := testHandler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/meta", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Message != "Not Found" {
		t.Fatalf("body = %s", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content type = %q", ct)
	}
}

func TestCredential(t *testing.T) {
	basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:df_basic"))
	cases := []struct {
		header, cookie, want string
	}{
		{"Bearer df_abc", "", "df_abc"},
		{"token df_tok", "", "df_tok"},
		{"TOKEN df_upper", "", "df_upper"},
		{"Basic " + basic, "", "df_basic"},
		{"Basic !!!", "", ""},
		{"", "distroface_session=sess123; other=1", "sess123"},
		{"", "", ""},
		{"Digest nope", "", ""},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/user", nil)
		if c.header != "" {
			req.Header.Set("Authorization", c.header)
		}
		if c.cookie != "" {
			req.Header.Set("Cookie", c.cookie)
		}
		if got := credential(req); got != c.want {
			t.Errorf("credential(%q, %q) = %q, want %q", c.header, c.cookie, got, c.want)
		}
	}
}

func TestParsePage(t *testing.T) {
	cases := map[string]pageParams{
		"":                      {page: 1, perPage: defaultPerPage},
		"page=3&per_page=10":    {page: 3, perPage: 10},
		"page=0&per_page=0":     {page: 1, perPage: defaultPerPage},
		"per_page=500":          {page: 1, perPage: maxPerPage},
		"page=abc&per_page=xyz": {page: 1, perPage: defaultPerPage},
	}
	for raw, want := range cases {
		req := httptest.NewRequest(http.MethodGet, "/repos/o/r/issues?"+raw, nil)
		if got := parsePage(req); got != want {
			t.Errorf("parsePage(%q) = %+v, want %+v", raw, got, want)
		}
	}
	if p := (pageParams{page: 3, perPage: 10}); p.offset() != 20 {
		t.Errorf("offset = %d", p.offset())
	}
}

func TestLinkHeader(t *testing.T) {
	base, _ := url.Parse("https://df.example.com/api/v3/repos/o/r/issues?state=all")
	got := linkHeader(base, pageParams{page: 2, perPage: 10}, 45)
	for _, want := range []string{
		`<https://df.example.com/api/v3/repos/o/r/issues?page=3&per_page=10&state=all>; rel="next"`,
		`<https://df.example.com/api/v3/repos/o/r/issues?page=5&per_page=10&state=all>; rel="last"`,
		`<https://df.example.com/api/v3/repos/o/r/issues?page=1&per_page=10&state=all>; rel="first"`,
		`<https://df.example.com/api/v3/repos/o/r/issues?page=1&per_page=10&state=all>; rel="prev"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("link header missing %s in %s", want, got)
		}
	}
	if l := linkHeader(base, pageParams{page: 1, perPage: 10}, 5); l != "" {
		t.Errorf("single page should have no links, got %s", l)
	}
	if l := linkHeader(base, pageParams{page: 1, perPage: 10}, 11); !strings.Contains(l, `rel="next"`) || strings.Contains(l, `rel="prev"`) {
		t.Errorf("first of two pages = %s", l)
	}
}

func TestNumIDAndMatching(t *testing.T) {
	id := "3b2f1c9e-1d2a-4f5b-9c8d-7e6f5a4b3c2d"
	n := numID(id)
	if n <= 0 || n >= 1<<53 {
		t.Fatalf("numID out of the safe integer range: %d", n)
	}
	if numID(id) != n {
		t.Fatal("numID is not stable")
	}
	if numID("12345") != 12345 {
		t.Errorf("numeric ids pass through, got %d", numID("12345"))
	}
	if !idMatches(fmt.Sprint(n), id) || !idMatches(id, id) || idMatches("999", id) {
		t.Error("idMatches should accept the derived integer and the row id only")
	}
	if numID("a") == numID("b") {
		t.Error("distinct ids collide")
	}
}

func TestStatusOf(t *testing.T) {
	cases := map[error]int{
		forge.ErrNotFound:              http.StatusNotFound,
		gitrepo.ErrRepoNotFound:        http.StatusNotFound,
		forge.ErrForbidden:             http.StatusForbidden,
		gitrepo.ErrArchived:            http.StatusForbidden,
		forge.ErrLocked:                http.StatusForbidden,
		forge.ErrInvalid:               http.StatusUnprocessableEntity,
		forge.ErrConflict:              http.StatusUnprocessableEntity,
		gitrepo.ErrTooLarge:            http.StatusRequestEntityTooLarge,
		errors.New("database on fire"): http.StatusInternalServerError,
	}
	for err, want := range cases {
		wrapped := fmt.Errorf("context: %w", err)
		if got, _ := statusOf(wrapped); got != want {
			t.Errorf("statusOf(%v) = %d, want %d", err, got, want)
		}
	}
	if _, msg := statusOf(errors.New("secret detail")); msg != "Internal error" {
		t.Errorf("internal errors must not leak, got %q", msg)
	}
}

func TestIssueOrder(t *testing.T) {
	cases := map[[2]string]string{
		{"", ""}:             "created_at DESC",
		{"updated", "asc"}:   "updated_at ASC",
		{"comments", "desc"}: "comments DESC",
		{"bogus", "ASC"}:     "created_at ASC",
	}
	for in, want := range cases {
		if got := issueOrder(in[0], in[1]); got != want {
			t.Errorf("issueOrder(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

func TestStringListAcceptsScalarAndList(t *testing.T) {
	var body issueBody
	if err := json.Unmarshal([]byte(`{"labels":"bug","assignees":["a","b"]}`), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Labels) != 1 || body.Labels[0] != "bug" || len(body.Assignees) != 2 {
		t.Errorf("decoded %+v", body)
	}
}

func TestCombinedState(t *testing.T) {
	if combinedState(nil) != "pending" {
		t.Error("no statuses should be pending")
	}
}

func TestPageSlice(t *testing.T) {
	items := []int{1, 2, 3, 4, 5}
	if got := pageSlice(items, pageParams{page: 2, perPage: 2}); len(got) != 2 || got[0] != 3 {
		t.Errorf("page 2 = %v", got)
	}
	if got := pageSlice(items, pageParams{page: 9, perPage: 2}); len(got) != 0 {
		t.Errorf("past the end = %v", got)
	}
}

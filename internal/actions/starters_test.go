package actions

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/nickheyer/distroface/pkg/logger"
)

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseStartersReadsPropertiesAndIcons(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "ci", "go.yml"), "name: Go\non:\n  push:\n    branches: [ $default-branch ]\n")
	writeFile(t, filepath.Join(dir, "ci", "properties", "go.properties.json"), `{"name":"Go","description":"Build a Go project.","iconName":"go","categories":["Continuous integration","Go"],"filePatterns":["go.mod$"]}`)
	writeFile(t, filepath.Join(dir, "ci", "orphan.yml"), "on: push\n")
	writeFile(t, filepath.Join(dir, "ci", "blank.yml"), "name: CI\non: push\n")
	writeFile(t, filepath.Join(dir, "ci", "properties", "blank.properties.json"), `{"name":"Simple workflow","creator":"GitHub","iconName":"blank","categories":null}`)
	writeFile(t, filepath.Join(dir, "ci", "npm-publish-github-packages.yml"), "on: release\nsteps:\n  - uses: actions/setup-node@v4\n    with:\n      registry-url: $registry-url(npm)\n")
	writeFile(t, filepath.Join(dir, "ci", "properties", "npm-publish-github-packages.properties.json"), `{"name":"Publish Node.js Package to GitHub Packages","iconName":"npm","categories":["JavaScript"]}`)
	writeFile(t, filepath.Join(dir, "deployments", "pages.yml"), "name: Pages\non: push\n")
	writeFile(t, filepath.Join(dir, "deployments", "properties", "pages.properties.json"), `{"name":"Deploy pages","description":"Publish.","iconName":"missing","categories":["Deployment"]}`)
	writeFile(t, filepath.Join(dir, "code-scanning", "codeql.yml"), "name: CodeQL\non: push\n")
	writeFile(t, filepath.Join(dir, "code-scanning", "properties", "codeql.properties.json"), `{"name":"CodeQL Analysis","creator":"GitHub","iconName":"octicon mark-github","categories":["Code Scanning","Go"]}`)
	writeFile(t, filepath.Join(dir, "automation", "greetings.yml"), "name: Greetings\non: issues\n")
	writeFile(t, filepath.Join(dir, "automation", "properties", "greetings.properties.json"), `{"name":"Greetings","iconName":"octicon smiley","categories":["Automation"]}`)
	writeFile(t, filepath.Join(dir, "icons", "go.svg"), "<svg/>")
	writeFile(t, filepath.Join(dir, "README.md"), "x")

	list, err := parseStarters(dir)
	if err != nil {
		t.Fatal(err)
	}
	var ids, sections []string
	for _, tpl := range list {
		ids = append(ids, tpl.ID)
		sections = append(sections, tpl.Category)
	}
	wantIDs := "deployments/pages code-scanning/codeql ci/go ci/blank automation/greetings"
	if strings.Join(ids, " ") != wantIDs {
		t.Fatalf("ids = %q, want %q", strings.Join(ids, " "), wantIDs)
	}
	wantSections := "Deployment Security Continuous integration Continuous integration Automation"
	if strings.Join(sections, " ") != wantSections {
		t.Fatalf("sections = %q, want %q", strings.Join(sections, " "), wantSections)
	}
	goT := list[2]
	if goT.IconSVG != "<svg/>" || goT.IconName != "" || goT.FileName != "go.yml" || len(goT.FilePatterns) != 1 || goT.Creator != "" {
		t.Fatalf("go = %+v", goT)
	}
	if codeql := list[1]; codeql.IconName != "mark-github" || codeql.IconSVG != "" || codeql.Creator != "GitHub" {
		t.Fatalf("codeql = %+v", codeql)
	}
	if list[0].IconSVG != "" || list[0].IconName != "" {
		t.Fatalf("pages = %+v", list[0])
	}
	if list[3].Name != "Simple workflow" || list[3].Creator != "GitHub" {
		t.Fatalf("blank = %+v", list[3])
	}
}

func TestRenderWorkflowTemplateQuotesAndExpands(t *testing.T) {
	in := TemplateRender{DefaultBranch: "trunk", ProtectedBranches: []string{"release/*", "trunk", " "}, RegistryHost: "registry.example.com"}
	cases := map[string]string{
		"branches: [ $default-branch, $protected-branches ]": `branches: [ "trunk", "release/*" ]`,
		"branches: [$default-branch, $protected-branches]":   `branches: ["trunk", "release/*"]`,
		"branches: [$default-branch]":                        `branches: ["trunk"]`,
		"- '$default-branch'":                                "- 'trunk'",
		"if: github.ref == 'refs/heads/$default-branch'":     "if: github.ref == 'refs/heads/trunk'",
		"# push to the $default-branch branch":               `# push to the "trunk" branch`,
		"DEFAULT_BRANCH: $default-branch":                    `DEFAULT_BRANCH: "trunk"`,
		"registry-url: $registry-url(container)":             "registry-url: registry.example.com",
	}
	for src, want := range cases {
		if got := RenderWorkflowTemplate(src, in); got != want {
			t.Errorf("%q rendered %q, want %q", src, got, want)
		}
	}
	if got := RenderWorkflowTemplate("branches: [ $default-branch, $protected-branches ]", TemplateRender{}); got != `branches: [ "main" ]` {
		t.Errorf("no protections rendered %q", got)
	}
	weekly := regexp.MustCompile(`^- cron: '\d+ \d+ \* \* \d'$`)
	if got := RenderWorkflowTemplate("- cron: $cron-weekly", in); !weekly.MatchString(got) {
		t.Errorf("weekly = %q", got)
	}
	daily := regexp.MustCompile(`^cron: '\d+ \d+ \* \* \*'$`)
	if got := RenderWorkflowTemplate("cron: '$cron-daily'", in); !daily.MatchString(got) {
		t.Errorf("daily = %q", got)
	}
}

func TestRenderWorkflowTemplateCodeQLMatrix(t *testing.T) {
	src := "      matrix:\n        $codeql-languages-matrix\n        # CodeQL supports the following values keywords for 'language': $supported-codeql-languages\n"
	in := TemplateRender{CodeQLLanguages: []CodeQLLanguage{{"go", "autobuild"}, {"actions", "none"}}}
	want := "      matrix:\n        include:\n        - language: go\n          build-mode: autobuild\n        - language: actions\n          build-mode: none\n" +
		"        # CodeQL supports the following values keywords for 'language': 'actions', 'c-cpp', 'csharp', 'go', 'java-kotlin', 'javascript-typescript', 'python', 'ruby', 'rust', 'swift'\n"
	if got := RenderWorkflowTemplate(src, in); got != want {
		t.Fatalf("rendered\n%s\nwant\n%s", got, want)
	}
	empty := RenderWorkflowTemplate(src, TemplateRender{})
	if !strings.Contains(empty, "        include: []\n") || strings.Contains(empty, "$codeql") {
		t.Fatalf("empty matrix rendered\n%s", empty)
	}
}

func TestCodeQLMatrixOrdersByBytes(t *testing.T) {
	got := codeqlMatrix(map[string]int64{"TypeScript": 10, "JavaScript": 5, "Go": 100, "Shell": 1000, "C": 3, "C++": 4, "Kotlin": 1}, true)
	want := []CodeQLLanguage{{"go", "autobuild"}, {"javascript-typescript", "none"}, {"c-cpp", "manual"}, {"java-kotlin", "autobuild"}, {"actions", "none"}}
	if len(got) != len(want) {
		t.Fatalf("matrix = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("matrix[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
	if m := codeqlMatrix(map[string]int64{"Shell": 5}, false); len(m) != 0 {
		t.Fatalf("shell only = %+v", m)
	}
}

func initStarterRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	r, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	w, err := r.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	for p, c := range files {
		writeFile(t, filepath.Join(dir, p), c)
		if _, err := w.Add(p); err != nil {
			t.Fatal(err)
		}
	}
	sig := &object.Signature{Name: "starters", Email: "starters@example.com", When: time.Now()}
	if _, err := w.Commit("starters", &git.CommitOptions{Author: sig, Committer: sig}); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRefreshStartersStampsFailuresAndSwaps(t *testing.T) {
	ctx := context.Background()
	src := initStarterRepo(t, map[string]string{
		"ci/x.yml":                        "name: X\non: push\n",
		"ci/properties/x.properties.json": `{"name":"X"}`,
	})
	m := &Manager{dataDir: t.TempDir(), log: logger.New()}
	dir := m.starterDir()
	if err := m.refreshStarters(ctx, src); err != nil {
		t.Fatal(err)
	}
	stamp, ok := readStarterStamp(dir)
	if !ok || stamp.URL != src || stamp.FetchedAt.IsZero() || stamp.LastError != "" {
		t.Fatalf("stamp = %+v", stamp)
	}
	if list, err := parseStarters(dir); err != nil || len(list) != 1 || list[0].ID != "ci/x" {
		t.Fatalf("templates = %+v, %v", list, err)
	}

	// Stale copy plus a vanished source keeps serving and records the failure
	stale := stamp.FetchedAt.Add(-25 * time.Hour)
	stamp.FetchedAt = stale
	if err := writeStarterStamp(dir, stamp); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(src); err != nil {
		t.Fatal(err)
	}
	if err := m.refreshStarters(ctx, src); err != nil {
		t.Fatalf("stale refresh must serve the old copy, got %v", err)
	}
	failed, _ := readStarterStamp(dir)
	if failed.LastError == "" || failed.AttemptedAt.IsZero() || !failed.FetchedAt.Equal(stale) || failed.URL != src {
		t.Fatalf("failed stamp = %+v", failed)
	}
	if list, err := parseStarters(dir); err != nil || len(list) != 1 {
		t.Fatalf("old copy gone: %+v, %v", list, err)
	}
	if err := m.refreshStarters(ctx, src); err != nil {
		t.Fatal(err)
	}
	if again, _ := readStarterStamp(dir); !again.AttemptedAt.Equal(failed.AttemptedAt) {
		t.Fatalf("retried inside the backoff window: %+v", again)
	}

	// Another unreachable source has no copy to serve, so it errors and backs off
	missing := filepath.Join(t.TempDir(), "missing")
	if err := m.refreshStarters(ctx, missing); err == nil {
		t.Fatal("missing source must fail")
	}
	moved, _ := readStarterStamp(dir)
	if moved.URL != src || moved.AttemptedURL != missing || moved.LastError == "" || !moved.FetchedAt.Equal(stale) {
		t.Fatalf("stamp after failed switch = %+v", moved)
	}
	if err := m.refreshStarters(ctx, missing); err == nil || !strings.Contains(err.Error(), "retry after") {
		t.Fatalf("second failure must report the backoff, got %v", err)
	}

	// A reachable new source replaces the old checkout
	src2 := initStarterRepo(t, map[string]string{
		"ci/y.yml":                        "name: Y\non: push\n",
		"ci/properties/y.properties.json": `{"name":"Y"}`,
	})
	if err := m.refreshStarters(ctx, src2); err != nil {
		t.Fatal(err)
	}
	fresh, _ := readStarterStamp(dir)
	if fresh.URL != src2 || fresh.LastError != "" || fresh.FetchedAt.Before(stale.Add(24*time.Hour)) {
		t.Fatalf("fresh stamp = %+v", fresh)
	}
	if list, err := parseStarters(dir); err != nil || len(list) != 1 || list[0].ID != "ci/y" {
		t.Fatalf("templates after swap = %+v, %v", list, err)
	}
	for _, leftover := range []string{dir + ".old", dir + ".fetch"} {
		if _, err := os.Stat(leftover); err == nil {
			t.Fatalf("%s left behind", leftover)
		}
	}

	// No checkout at all records the failure where the next clone will land
	bare := &Manager{dataDir: t.TempDir(), log: logger.New()}
	if err := bare.refreshStarters(ctx, missing); err == nil {
		t.Fatal("first clone failure must surface")
	}
	first, ok := readStarterStamp(bare.starterDir())
	if !ok || first.URL != missing || !first.FetchedAt.IsZero() || first.LastError == "" {
		t.Fatalf("first failure stamp = %+v", first)
	}
	if err := bare.refreshStarters(ctx, missing); err == nil || !strings.Contains(err.Error(), "retry after") {
		t.Fatalf("backoff missing: %v", err)
	}
}

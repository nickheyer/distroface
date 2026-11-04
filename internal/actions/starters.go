package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-git/v6"
	"github.com/nickheyer/distroface/internal/db"
)

const (
	defaultStarterWorkflowsURL = "https://github.com/actions/starter-workflows"
	starterRefreshEvery        = 24 * time.Hour
	starterRetryAfter          = 10 * time.Minute
	starterCloneTimeout        = 2 * time.Minute
	starterSourceFile          = ".distroface-source.json"
	starterOcticonPrefix       = "octicon "
)

// Section per source directory, the order GitHub lists them in
var starterSections = []struct{ Dir, Title string }{
	{"deployments", "Deployment"},
	{"code-scanning", "Security"},
	{"ci", "Continuous integration"},
	{"automation", "Automation"},
	{"pages", "Pages"},
}

// Registry kinds a $registry-url placeholder can name here
var starterRegistryKinds = map[string]bool{"container": true, "docker": true}

var starterRegistryURL = regexp.MustCompile(`\$registry-url\(([a-z]*)\)`)

var starterPlaceholder = regexp.MustCompile(`\$default-branch,\s*\$protected-branches|\$default-branch|\$protected-branches|\$cron-daily|\$cron-weekly|\$supported-codeql-languages|\$registry-url\([a-z]*\)`)

// WorkflowTemplate is one starter workflow with its properties file
type WorkflowTemplate struct {
	// Directory and file base inside the starter repository, such as ci/go
	ID          string
	Name        string
	Description string
	// Section title the source directory maps to
	Category     string
	Categories   []string
	FilePatterns []string
	Creator      string
	// Octicon name when the sidecar points at one instead of a file
	IconName string
	IconSVG  string
	FileName string
	Content  string
}

// Sidecar every starter workflow ships under properties
type starterProperties struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	IconName     string   `json:"iconName"`
	Categories   []string `json:"categories"`
	FilePatterns []string `json:"filePatterns"`
	Creator      string   `json:"creator"`
}

// Checkout origin, fetch time, and last refresh outcome
type starterSource struct {
	URL       string    `json:"url"`
	FetchedAt time.Time `json:"fetched_at"`
	// Last failed clone, cleared by the next success
	AttemptedURL string    `json:"attempted_url,omitempty"`
	AttemptedAt  time.Time `json:"attempted_at,omitempty"`
	LastError    string    `json:"last_error,omitempty"`
}

// StarterCatalog is the parsed checkout with its freshness
type StarterCatalog struct {
	Templates []*WorkflowTemplate
	Source    string
	FetchedAt time.Time
	// Last failed refresh, the templates come from an older fetch
	RefreshError string
}

// CodeQLLanguage is one matrix entry of the CodeQL starter
type CodeQLLanguage struct {
	Name      string
	BuildMode string
}

// TemplateRender carries what the placeholders of a starter need
type TemplateRender struct {
	DefaultBranch     string
	ProtectedBranches []string
	CodeQLLanguages   []CodeQLLanguage
	RegistryHost      string
}

// SetDataDir names where the starter workflow checkout lives
func (m *Manager) SetDataDir(dir string) { m.dataDir = dir }

// StarterWorkflowsURL is the repository the starter cards come from
func (m *Manager) StarterWorkflowsURL(ctx context.Context) string {
	if u := strings.TrimSpace(m.res.System(ctx).GetGit().GetActions().GetStarterWorkflowsUrl()); u != "" {
		return strings.TrimRight(u, "/")
	}
	return defaultStarterWorkflowsURL
}

func (m *Manager) starterDir() string {
	return filepath.Join(m.dataDir, "starter-workflows")
}

// WorkflowTemplates answers the starters, refreshing the checkout when stale
func (m *Manager) WorkflowTemplates(ctx context.Context) (*StarterCatalog, error) {
	source := m.StarterWorkflowsURL(ctx)
	if m.dataDir == "" {
		return nil, errors.New("starter workflows need a data directory")
	}
	m.starterMu.Lock()
	defer m.starterMu.Unlock()
	if err := m.refreshStarters(ctx, source); err != nil {
		return nil, err
	}
	dir := m.starterDir()
	list, err := parseStarters(dir)
	if err != nil {
		return nil, err
	}
	stamp, _ := readStarterStamp(dir)
	catalog := &StarterCatalog{Templates: list, Source: source, FetchedAt: stamp.FetchedAt}
	if stamp.AttemptedURL == source {
		catalog.RefreshError = stamp.LastError
	}
	return catalog, nil
}

// WorkflowTemplate finds one starter by id
func (m *Manager) WorkflowTemplate(ctx context.Context, id string) (*WorkflowTemplate, error) {
	catalog, err := m.WorkflowTemplates(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range catalog.Templates {
		if t.ID == id {
			return t, nil
		}
	}
	return nil, fmt.Errorf("%w: starter workflow %s", ErrNotFound, id)
}

// Background refresh keeps requests off the clone path
func (m *Manager) starterLoop(ctx context.Context) {
	if m.dataDir == "" {
		return
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(time.Minute):
	}
	m.refreshStarterCatalog(ctx)
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.refreshStarterCatalog(ctx)
		}
	}
}

func (m *Manager) refreshStarterCatalog(ctx context.Context) {
	source := m.StarterWorkflowsURL(ctx)
	m.starterMu.Lock()
	defer m.starterMu.Unlock()
	if err := m.refreshStarters(ctx, source); err != nil {
		m.log.Warn("actions: %v", err)
	}
}

func readStarterStamp(dir string) (starterSource, bool) {
	var stamp starterSource
	raw, err := os.ReadFile(filepath.Join(dir, starterSourceFile))
	if err != nil || json.Unmarshal(raw, &stamp) != nil {
		return starterSource{}, false
	}
	return stamp, true
}

func writeStarterStamp(dir string, stamp starterSource) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(stamp)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, starterSourceFile), raw, 0o644)
}

// Clones the source once a day, a failed refresh waits before retrying
func (m *Manager) refreshStarters(ctx context.Context, source string) error {
	dir := m.starterDir()
	current, stamped := readStarterStamp(dir)
	usable := stamped && current.URL == source && !current.FetchedAt.IsZero()
	if usable && time.Since(current.FetchedAt) < starterRefreshEvery {
		return nil
	}
	if stamped && current.LastError != "" && current.AttemptedURL == source && time.Since(current.AttemptedAt) < starterRetryAfter {
		if usable {
			return nil
		}
		return fmt.Errorf("fetch starter workflows from %s: %s (retry after %s)", source, current.LastError, current.AttemptedAt.Add(starterRetryAfter).Format(time.RFC3339))
	}
	tmp := dir + ".fetch"
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(filepath.Dir(tmp), 0o755); err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, starterCloneTimeout)
	defer cancel()
	if _, err := git.PlainCloneContext(cctx, tmp, &git.CloneOptions{URL: source, Depth: 1, SingleBranch: true, Tags: git.NoTags}); err != nil {
		_ = os.RemoveAll(tmp)
		failed := current
		if !stamped {
			failed.URL = source
		}
		failed.AttemptedURL, failed.AttemptedAt, failed.LastError = source, time.Now().UTC(), err.Error()
		if werr := writeStarterStamp(dir, failed); werr != nil {
			m.log.Warn("actions: starter workflow stamp at %s not written: %v", dir, werr)
		}
		if usable {
			m.log.Warn("actions: starter workflows at %s could not be refreshed, serving the copy from %s: %v", source, current.FetchedAt.Format(time.RFC3339), err)
			return nil
		}
		return fmt.Errorf("fetch starter workflows from %s: %w", source, err)
	}
	if err := writeStarterStamp(tmp, starterSource{URL: source, FetchedAt: time.Now().UTC()}); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	return swapStarterDir(dir, tmp)
}

// Old checkout steps aside so a failed rename leaves it reachable
func swapStarterDir(dir, tmp string) error {
	old := dir + ".old"
	_ = os.RemoveAll(old)
	hadOld := false
	if _, err := os.Stat(dir); err == nil {
		if err := os.Rename(dir, old); err != nil {
			_ = os.RemoveAll(tmp)
			return err
		}
		hadOld = true
	}
	if err := os.Rename(tmp, dir); err != nil {
		if hadOld {
			_ = os.Rename(old, dir)
		}
		_ = os.RemoveAll(tmp)
		return err
	}
	return os.RemoveAll(old)
}

func starterSection(dir string) (string, int) {
	for i, s := range starterSections {
		if s.Dir == dir {
			return s.Title, i
		}
	}
	return strings.ToUpper(dir[:1]) + dir[1:], len(starterSections)
}

// Reads every workflow that has a properties sidecar, icons included
func parseStarters(dir string) ([]*WorkflowTemplate, error) {
	groups, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []*WorkflowTemplate
	order := map[string]int{}
	for _, g := range groups {
		if !g.IsDir() || strings.HasPrefix(g.Name(), ".") || g.Name() == "icons" {
			continue
		}
		files, err := os.ReadDir(filepath.Join(dir, g.Name()))
		if err != nil {
			continue
		}
		section, rank := starterSection(g.Name())
		for _, f := range files {
			if f.IsDir() || !IsWorkflowFile(f.Name()) {
				continue
			}
			base := workflowBaseName(f.Name())
			raw, err := os.ReadFile(filepath.Join(dir, g.Name(), "properties", base+".properties.json"))
			if err != nil {
				continue
			}
			var props starterProperties
			// One broken sidecar upstream hides that starter, never the catalog
			if err := json.Unmarshal(raw, &props); err != nil {
				continue
			}
			content, err := os.ReadFile(filepath.Join(dir, g.Name(), f.Name()))
			if err != nil {
				return nil, err
			}
			// Starters for registries this instance lacks stay hidden
			if needsUnservedRegistry(string(content)) {
				continue
			}
			t := &WorkflowTemplate{
				ID: g.Name() + "/" + base, Name: props.Name, Description: props.Description, Category: section,
				Categories: props.Categories, FilePatterns: props.FilePatterns, Creator: props.Creator,
				FileName: f.Name(), Content: string(content),
			}
			if t.Name == "" {
				t.Name = base
			}
			switch {
			case strings.HasPrefix(props.IconName, starterOcticonPrefix):
				t.IconName = strings.TrimSpace(strings.TrimPrefix(props.IconName, starterOcticonPrefix))
			case props.IconName != "":
				if svg, err := os.ReadFile(filepath.Join(dir, "icons", props.IconName+".svg")); err == nil {
					t.IconSVG = string(svg)
				}
			}
			order[t.ID] = rank
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if order[out[i].ID] != order[out[j].ID] {
			return order[out[i].ID] < order[out[j].ID]
		}
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func needsUnservedRegistry(content string) bool {
	for _, m := range starterRegistryURL.FindAllStringSubmatch(content, -1) {
		if !starterRegistryKinds[m[1]] {
			return true
		}
	}
	return false
}

// TemplateNeedsCodeQL reports whether rendering wants the language scan
func TemplateNeedsCodeQL(content string) bool {
	return strings.Contains(content, "$codeql-languages-matrix") || strings.Contains(content, "$supported-codeql-languages")
}

// Linguist names CodeQL analyzes, with the build mode GitHub picks
var codeqlByLinguist = map[string]CodeQLLanguage{
	"C":          {"c-cpp", "manual"},
	"C++":        {"c-cpp", "manual"},
	"C#":         {"csharp", "autobuild"},
	"Go":         {"go", "autobuild"},
	"Java":       {"java-kotlin", "autobuild"},
	"Kotlin":     {"java-kotlin", "autobuild"},
	"JavaScript": {"javascript-typescript", "none"},
	"TypeScript": {"javascript-typescript", "none"},
	"TSX":        {"javascript-typescript", "none"},
	"Python":     {"python", "none"},
	"Ruby":       {"ruby", "none"},
	"Swift":      {"swift", "manual"},
	"Rust":       {"rust", "manual"},
}

var codeqlLanguageNames = []string{"actions", "c-cpp", "csharp", "go", "java-kotlin", "javascript-typescript", "python", "ruby", "rust", "swift"}

// CodeQLLanguages detects the matrix entries for a repository, largest language first
func (m *Manager) CodeQLLanguages(ctx context.Context, repo *db.GitRepository) ([]CodeQLLanguage, error) {
	r, err := m.openRepo(repo)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	head := r.BranchHash(repo.DefaultBranch)
	if head.IsZero() {
		return nil, nil
	}
	bytes, err := r.Languages(ctx, head)
	if err != nil {
		return nil, err
	}
	return codeqlMatrix(bytes, len(loadWorkflowFiles(ctx, r, head)) > 0), nil
}

func codeqlMatrix(bytes map[string]int64, hasWorkflows bool) []CodeQLLanguage {
	size := map[string]int64{}
	modes := map[string]string{}
	for lang, b := range bytes {
		ql, ok := codeqlByLinguist[lang]
		if !ok {
			continue
		}
		size[ql.Name] += b
		modes[ql.Name] = ql.BuildMode
	}
	names := make([]string, 0, len(size))
	for n := range size {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		if size[names[i]] != size[names[j]] {
			return size[names[i]] > size[names[j]]
		}
		return names[i] < names[j]
	})
	out := make([]CodeQLLanguage, 0, len(names)+1)
	for _, n := range names {
		out = append(out, CodeQLLanguage{Name: n, BuildMode: modes[n]})
	}
	if hasWorkflows {
		out = append(out, CodeQLLanguage{Name: "actions", BuildMode: "none"})
	}
	return out
}

// Values every placeholder of one rendering shares
type starterValues struct {
	in       TemplateRender
	branches []string
	daily    string
	weekly   string
}

// RenderWorkflowTemplate fills the placeholders starters carry for a repository
func RenderWorkflowTemplate(content string, in TemplateRender) string {
	if in.DefaultBranch == "" {
		in.DefaultBranch = "main"
	}
	minute, hour := rand.IntN(60), rand.IntN(24)
	v := starterValues{
		in:       in,
		branches: []string{in.DefaultBranch},
		daily:    fmt.Sprintf("%d %d * * *", minute, hour),
		weekly:   fmt.Sprintf("%d %d * * %d", minute, hour, rand.IntN(7)),
	}
	for _, p := range in.ProtectedBranches {
		p = strings.TrimSpace(p)
		if p != "" && p != in.DefaultBranch {
			v.branches = append(v.branches, p)
		}
	}
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "$codeql-languages-matrix" {
			lines[i] = codeqlMatrixBlock(line[:len(line)-len(strings.TrimLeft(line, " \t"))], in.CodeQLLanguages)
			continue
		}
		lines[i] = renderStarterLine(line, v)
	}
	return strings.Join(lines, "\n")
}

func renderStarterLine(line string, v starterValues) string {
	locs := starterPlaceholder.FindAllStringIndex(line, -1)
	if len(locs) == 0 {
		return line
	}
	var sb strings.Builder
	last := 0
	for _, loc := range locs {
		sb.WriteString(line[last:loc[0]])
		sb.WriteString(renderPlaceholder(line[loc[0]:loc[1]], insideQuotes(line[:loc[0]]), v))
		last = loc[1]
	}
	sb.WriteString(line[last:])
	return sb.String()
}

// Placeholder values take quotes unless the line already quotes them
func renderPlaceholder(ph string, quoted bool, v starterValues) string {
	in, branches, daily, weekly := v.in, v.branches, v.daily, v.weekly
	quote := func(s, q string) string {
		if quoted {
			return s
		}
		return q + s + q
	}
	switch {
	case strings.HasPrefix(ph, "$default-branch,"), ph == "$protected-branches":
		parts := make([]string, len(branches))
		for i, b := range branches {
			parts[i] = quote(b, `"`)
		}
		return strings.Join(parts, ", ")
	case ph == "$default-branch":
		return quote(in.DefaultBranch, `"`)
	case ph == "$cron-daily":
		return quote(daily, "'")
	case ph == "$cron-weekly":
		return quote(weekly, "'")
	case ph == "$supported-codeql-languages":
		parts := make([]string, len(codeqlLanguageNames))
		for i, n := range codeqlLanguageNames {
			parts[i] = "'" + n + "'"
		}
		return strings.Join(parts, ", ")
	case strings.HasPrefix(ph, "$registry-url("):
		return in.RegistryHost
	}
	return ph
}

// Odd quote counts before the placeholder mean it sits in a string
func insideQuotes(prefix string) bool {
	if strings.HasPrefix(strings.TrimSpace(prefix), "#") {
		return false
	}
	return strings.Count(prefix, "'")%2 == 1 || strings.Count(prefix, `"`)%2 == 1
}

func codeqlMatrixBlock(indent string, langs []CodeQLLanguage) string {
	if len(langs) == 0 {
		return strings.Join([]string{
			indent + "include: []",
			indent + "# No CodeQL supported language was detected, add entries such as",
			indent + "# - language: go",
			indent + "#   build-mode: autobuild",
		}, "\n")
	}
	lines := []string{indent + "include:"}
	for _, l := range langs {
		lines = append(lines, indent+"- language: "+l.Name, indent+"  build-mode: "+l.BuildMode)
	}
	return strings.Join(lines, "\n")
}

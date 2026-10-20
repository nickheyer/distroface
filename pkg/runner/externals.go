package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/client"
	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/config"
	"github.com/go-git/go-git/v6/plumbing"
	gitclient "github.com/go-git/go-git/v6/plumbing/client"
	githttp "github.com/go-git/go-git/v6/plumbing/transport/http"
	"github.com/go-git/go-git/v6/storage/memory"
	"github.com/google/uuid"
)

// Externals dir as seen inside containers
const containerExternals = "/__e"

// Floating action refs are compared against the remote this often
const actionRecheck = 10 * time.Minute

// Libc the node build links against
type nodeFlavor string

const (
	flavorGlibc nodeFlavor = "glibc"
	flavorMusl  nodeFlavor = "musl"
)

var nodeFlavors = []nodeFlavor{flavorGlibc, flavorMusl}

// Node binaries and action checkouts shared across jobs
type externals struct {
	root string
	http *http.Client

	mu     sync.Mutex
	failed map[string]error
	locks  map[string]*sync.Mutex
}

func newExternals(root string, httpClient *http.Client) *externals {
	return &externals{root: root, http: httpClient, failed: map[string]error{}, locks: map[string]*sync.Mutex{}}
}

// One mutex per cache entry so concurrent jobs share downloads
func (e *externals) lock(key string) *sync.Mutex {
	e.mu.Lock()
	defer e.mu.Unlock()
	m := e.locks[key]
	if m == nil {
		m = &sync.Mutex{}
		e.locks[key] = m
	}
	return m
}

// Major version an action's runs.using asks for
func nodeVersionFor(using string) int {
	if strings.EqualFold(strings.TrimSpace(using), "node24") {
		return 24
	}
	return 20
}

// Directory holding one node build
func nodeDir(root string, ver int, flavor nodeFlavor) string {
	return filepath.Join(root, "node"+strconv.Itoa(ver), string(flavor))
}

// Same directory in the container view
func nodeContainerDir(ver int, flavor nodeFlavor) string {
	return path.Join(containerExternals, "node"+strconv.Itoa(ver), string(flavor))
}

func nodeImage(ver int, flavor nodeFlavor) string {
	if flavor == flavorMusl {
		return fmt.Sprintf("docker.io/library/node:%d-alpine", ver)
	}
	return fmt.Sprintf("docker.io/library/node:%d-bookworm-slim", ver)
}

// Copies node and its c++ runtime out of the image
func nodeExtractScript(flavor nodeFlavor) string {
	libs := "/usr/lib/$(uname -m)-linux-gnu"
	if flavor == flavorMusl {
		libs = "/usr/lib"
	}
	return strings.Join([]string{
		"set -e",
		"mkdir -p /out/bin /out/lib",
		"cp /usr/local/bin/node /out/bin/node",
		"cp -L " + libs + "/libstdc++.so.6 /out/lib/libstdc++.so.6",
		"cp -L " + libs + "/libgcc_s.so.1 /out/lib/libgcc_s.so.1",
		"chmod 0755 /out/bin/node /out/lib/*",
		"chmod -R a+rX /out",
	}, "\n")
}

func nodeReady(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "bin", "node"))
	return err == nil && info.Size() > 0
}

// Ensures one node build on disk, extracting it on first use
func (e *externals) ensureNode(ctx context.Context, cli *client.Client, ver int, flavor nodeFlavor, out io.Writer) (string, error) {
	dir := nodeDir(e.root, ver, flavor)
	key := "node/" + strconv.Itoa(ver) + "/" + string(flavor)
	m := e.lock(key)
	m.Lock()
	defer m.Unlock()
	if nodeReady(dir) {
		return dir, nil
	}
	e.mu.Lock()
	err, failed := e.failed[key]
	e.mu.Unlock()
	if failed {
		return "", err
	}
	err = e.extract(ctx, cli, ver, flavor, dir, out)
	if err != nil {
		e.mu.Lock()
		e.failed[key] = err
		e.mu.Unlock()
		fmt.Fprintf(out, "##[warning]node %d (%s) could not be prepared: %v\n", ver, flavor, err)
		return "", err
	}
	return dir, nil
}

func (e *externals) extract(ctx context.Context, cli *client.Client, ver int, flavor nodeFlavor, dir string, out io.Writer) error {
	if cli == nil {
		return fmt.Errorf("docker is required to extract node")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	image := nodeImage(ver, flavor)
	fmt.Fprintf(out, "Preparing node %d (%s) from %s\n", ver, flavor, image)
	if err := pullImage(ctx, cli, image, "", out); err != nil {
		return err
	}
	code, err := runContainer(ctx, cli, containerRun{
		Image: image, Entrypoint: []string{"sh"}, Cmd: []string{"-c", nodeExtractScript(flavor)},
		Binds: []string{dir + ":/out"}, Out: out,
	})
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("extract exited with code %d", code)
	}
	if !nodeReady(dir) {
		return fmt.Errorf("extract left no node binary in %s", dir)
	}
	return nil
}

// Resolved node invocation for one major version
type nodeCmd struct {
	cmd []string
	env []string
}

// Image node when new enough, own builds otherwise
func (j *job) nodeCommand(ctx context.Context, using string) ([]string, []string, error) {
	ver := nodeVersionFor(using)
	j.mu.Lock()
	cached := j.nodeCmds[ver]
	j.mu.Unlock()
	if cached != nil {
		return cached.cmd, cached.env, nil
	}
	remember := func(cmd, env []string) {
		j.mu.Lock()
		j.nodeCmds[ver] = &nodeCmd{cmd: cmd, env: env}
		j.mu.Unlock()
	}
	// Absolute path so GITHUB_PATH changes cannot swap it
	imageNode := j.exec.Which(ctx, "node")
	imageMajor := 0
	if imageNode != "" {
		imageMajor = j.nodeMajor(ctx, imageNode)
		if imageMajor >= ver {
			remember([]string{imageNode}, nil)
			return []string{imageNode}, nil, nil
		}
	}
	useImageNode := func(reason string) ([]string, []string, error) {
		j.log.Line("##[warning]Running node%d actions with the job image's node %d (%s)", ver, imageMajor, reason)
		remember([]string{imageNode}, nil)
		return []string{imageNode}, nil, nil
	}
	cli, err := j.dockerClient()
	if err != nil {
		if imageNode != "" {
			return useImageNode(err.Error())
		}
		return nil, nil, fmt.Errorf("node is not in the job image and %w", err)
	}
	var errs []string
	for _, flavor := range nodeFlavors {
		dir, err := j.r.ext.ensureNode(ctx, cli, ver, flavor, j.log)
		if err != nil {
			errs = append(errs, string(flavor)+": "+err.Error())
			continue
		}
		bin, lib := filepath.Join(dir, "bin", "node"), filepath.Join(dir, "lib")
		if j.confined {
			base := nodeContainerDir(ver, flavor)
			bin, lib = path.Join(base, "bin", "node"), path.Join(base, "lib")
		}
		for _, env := range [][]string{nil, {"LD_LIBRARY_PATH=" + lib}} {
			code, err := j.exec.Exec(ctx, execSpec{Cmd: []string{bin, "--version"}, Env: env, Out: io.Discard})
			if err == nil && code == 0 {
				j.log.Line("Using the runner's node %d (%s) for javascript actions", ver, flavor)
				remember([]string{bin}, env)
				return []string{bin}, env, nil
			}
		}
		errs = append(errs, string(flavor)+": binary does not run in this image")
	}
	if imageNode != "" {
		return useImageNode(strings.Join(errs, ", "))
	}
	return nil, nil, fmt.Errorf("no node %d runs in image %s, pick a container image that ships node (%s)", ver, j.imageName(), strings.Join(errs, ", "))
}

// Major version a node binary reports, zero when unknown
func (j *job) nodeMajor(ctx context.Context, bin string) int {
	var buf bytes.Buffer
	code, err := j.exec.Exec(ctx, execSpec{Cmd: []string{bin, "--version"}, Out: &buf})
	if err != nil || code != 0 {
		return 0
	}
	v := strings.TrimPrefix(strings.TrimSpace(buf.String()), "v")
	major, _, _ := strings.Cut(v, ".")
	n, _ := strconv.Atoi(major)
	return n
}

// Docker client of the job, opened on demand for host jobs
func (j *job) dockerClient() (*client.Client, error) {
	j.mu.Lock()
	cli := j.docker
	j.mu.Unlock()
	if cli != nil {
		return cli, nil
	}
	cli, err := newDockerClient(j.r.cfg.DockerHost)
	if err != nil {
		return nil, err
	}
	j.mu.Lock()
	if j.docker == nil {
		j.docker = cli
	} else {
		cli.Close()
		cli = j.docker
	}
	j.mu.Unlock()
	return cli, nil
}

// Image the job runs in, the host description otherwise
func (j *job) imageName() string {
	if j.exec == nil {
		return "unknown"
	}
	return strings.TrimPrefix(j.exec.Describe(), "docker ")
}

// Action cache

// Kinds a ref resolved to
const (
	refBranch = "branch"
	refTag    = "tag"
	refSHA    = "sha"
)

// Marker written beside a cached action checkout
type actionMarker struct {
	Hash    string    `json:"hash"`
	Kind    string    `json:"kind"`
	Source  string    `json:"source"`
	Checked time.Time `json:"checked"`
}

const actionMarkerFile = ".dfcli-completed"

// Directory the cache keeps one source url and ref in
func (e *externals) actionDir(source, ref string) string {
	u, err := url.Parse(source)
	host := "unknown"
	p := source
	if err == nil && u.Host != "" {
		host, p = u.Host, strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git")
	}
	parts := strings.Split(p, "/")
	segs := []string{e.root, "actions", safeSegment(host)}
	for _, part := range parts {
		segs = append(segs, safeSegment(part))
	}
	segs = append(segs, safeSegment(ref))
	return filepath.Join(segs...)
}

func readMarker(dir string) *actionMarker {
	raw, err := os.ReadFile(filepath.Join(dir, actionMarkerFile))
	if err != nil {
		return nil
	}
	var m actionMarker
	if json.Unmarshal(raw, &m) != nil || m.Hash == "" {
		return nil
	}
	return &m
}

func writeMarker(dir string, m *actionMarker) error {
	raw, _ := json.Marshal(m)
	return os.WriteFile(filepath.Join(dir, actionMarkerFile), raw, 0o644)
}

// Cached action checkout used under its lock, refreshed when moved
func (e *externals) ensureAction(ctx context.Context, source, ref, token string, out io.Writer, use func(dir string) error) error {
	dir := e.actionDir(source, ref)
	m := e.lock(dir)
	m.Lock()
	defer m.Unlock()
	fresh := false
	if marker := readMarker(dir); marker != nil {
		switch {
		case marker.Kind == refSHA || time.Since(marker.Checked) < actionRecheck:
			fresh = true
		default:
			hash, kind, err := e.resolveRemote(ctx, source, ref, token)
			switch {
			case err == nil && hash == marker.Hash:
				marker.Checked = time.Now()
				_ = writeMarker(dir, marker)
				fresh = true
			case err != nil:
				fmt.Fprintf(out, "##[warning]Could not check %s@%s against %s (%v), using the cached copy\n", path.Base(strings.TrimSuffix(source, ".git")), ref, source, err)
				fresh = true
			default:
				fmt.Fprintf(out, "Action %s moved from %s to %s (%s)\n", ref, shortID(marker.Hash), shortID(hash), kind)
			}
		}
	}
	if !fresh {
		if err := e.download(ctx, source, ref, token, dir); err != nil {
			return err
		}
	}
	return use(dir)
}

// Clones into a scratch dir and swaps it in whole
func (e *externals) download(ctx context.Context, source, ref, token, dir string) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	tmp := dir + ".tmp-" + uuid.NewString()[:8]
	kind, hash, err := cloneRef(ctx, e.http, source, tmp, ref, 1, token)
	if err != nil {
		os.RemoveAll(tmp)
		return err
	}
	if err := writeMarker(tmp, &actionMarker{Hash: hash, Kind: kind, Source: source, Checked: time.Now()}); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	_ = os.RemoveAll(dir)
	if err := os.Rename(tmp, dir); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	return nil
}

// Remote hash and kind of a ref without downloading
func (e *externals) resolveRemote(ctx context.Context, source, ref, token string) (string, string, error) {
	if shaPattern.MatchString(ref) {
		return ref, refSHA, nil
	}
	rem := git.NewRemote(memory.NewStorage(), &config.RemoteConfig{Name: "origin", URLs: []string{source}})
	lctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	refs, err := rem.ListContext(lctx, &git.ListOptions{ClientOptions: gitClientOptions(e.http, token), PeelingOption: git.AppendPeeled})
	if err != nil {
		return "", "", err
	}
	byName := map[plumbing.ReferenceName]*plumbing.Reference{}
	for _, r := range refs {
		byName[r.Name()] = r
	}
	pick := func(name plumbing.ReferenceName) (string, bool) {
		if r, ok := byName[name+"^{}"]; ok {
			return r.Hash().String(), true
		}
		if r, ok := byName[name]; ok {
			return r.Hash().String(), true
		}
		return "", false
	}
	if strings.HasPrefix(ref, "refs/") {
		if h, ok := pick(plumbing.ReferenceName(ref)); ok {
			kind := refBranch
			if strings.HasPrefix(ref, "refs/tags/") {
				kind = refTag
			}
			return h, kind, nil
		}
		return "", "", fmt.Errorf("%s not found on %s", ref, source)
	}
	if h, ok := pick(plumbing.NewBranchReferenceName(ref)); ok {
		return h, refBranch, nil
	}
	if h, ok := pick(plumbing.NewTagReferenceName(ref)); ok {
		return h, refTag, nil
	}
	return "", "", fmt.Errorf("%s is neither a branch nor a tag on %s", ref, source)
}

func gitClientOptions(httpClient *http.Client, token string) []gitclient.Option {
	var copts []gitclient.Option
	if httpClient != nil {
		copts = append(copts, gitclient.WithHTTPClient(httpClient))
	}
	if token != "" {
		copts = append(copts, gitclient.WithHTTPAuth(&githttp.BasicAuth{Username: jobTokenUser, Password: token}))
	}
	return copts
}

// Host side clone trying branch, tag, then sha
func cloneRef(ctx context.Context, httpClient *http.Client, source, dest, ref string, depth int, token string) (kind, hash string, err error) {
	copts := gitClientOptions(httpClient, token)
	ref = strings.TrimSpace(ref)
	if err := os.MkdirAll(dest, 0o777); err != nil {
		return "", "", err
	}
	if entries, _ := os.ReadDir(dest); len(entries) > 0 {
		return "", "", fmt.Errorf("destination %s is not empty", dest)
	}
	clone := func(name plumbing.ReferenceName, d int) (*git.Repository, error) {
		repo, err := git.PlainCloneContext(ctx, dest, &git.CloneOptions{URL: source, ReferenceName: name, SingleBranch: name != "", Depth: d, ClientOptions: copts, Tags: git.NoTags})
		if err != nil {
			_ = os.RemoveAll(dest)
			_ = os.MkdirAll(dest, 0o777)
		}
		return repo, err
	}
	headHash := func(repo *git.Repository) string {
		if h, err := repo.Head(); err == nil {
			return h.Hash().String()
		}
		return ""
	}
	if ref == "" {
		repo, err := clone("", depth)
		if err != nil {
			return "", "", err
		}
		return refBranch, headHash(repo), nil
	}
	if shaPattern.MatchString(ref) {
		if _, err := clone("", 0); err != nil {
			return "", "", err
		}
		return refSHA, ref, checkoutHash(dest, ref)
	}
	var candidates []plumbing.ReferenceName
	if strings.HasPrefix(ref, "refs/") {
		candidates = append(candidates, plumbing.ReferenceName(ref))
	} else {
		candidates = append(candidates, plumbing.NewBranchReferenceName(ref), plumbing.NewTagReferenceName(ref))
	}
	var last error
	for _, c := range candidates {
		repo, err := clone(c, depth)
		if err == nil {
			kind := refBranch
			if c.IsTag() {
				kind = refTag
			}
			return kind, headHash(repo), nil
		}
		last = err
	}
	repo, err := clone("", 0)
	if err != nil {
		return "", "", last
	}
	if err := checkoutHash(dest, ref); err != nil {
		return "", "", errors.Join(last, err)
	}
	return refSHA, headHash(repo), nil
}

func checkoutHash(dest, rev string) error {
	repo, err := git.PlainOpen(dest)
	if err != nil {
		return err
	}
	h, err := repo.ResolveRevision(plumbing.Revision(rev))
	if err != nil {
		return fmt.Errorf("resolve %s: %w", rev, err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return err
	}
	return wt.Checkout(&git.CheckoutOptions{Hash: *h, Force: true})
}

// Copies a tree into a root, skipping git metadata
func copyTreeInto(root *os.Root, src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return root.MkdirAll(dst, 0o777)
		}
		if info.IsDir() && info.Name() == ".git" {
			return filepath.SkipDir
		}
		if info.Name() == actionMarkerFile {
			return nil
		}
		target := filepath.Join(dst, rel)
		switch {
		case info.IsDir():
			if err := root.MkdirAll(target, 0o777); err != nil {
				return err
			}
			return root.Chmod(target, 0o777)
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			_ = root.Remove(target)
			return root.Symlink(link, target)
		case info.Mode().IsRegular():
			return copyFileInto(root, p, target, info.Mode().Perm()|0o644)
		}
		return nil
	})
}

func copyFileInto(root *os.Root, src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := root.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return root.Chmod(dst, mode)
}

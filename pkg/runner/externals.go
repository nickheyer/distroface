package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/docker/docker/client"
)

// Externals dir as seen inside containers
const containerExternals = "/__e"

// Libc the node build links against
type nodeFlavor string

const (
	flavorGlibc nodeFlavor = "glibc"
	flavorMusl  nodeFlavor = "musl"
)

var nodeFlavors = []nodeFlavor{flavorGlibc, flavorMusl}

// Node binaries lent to job images that ship none
type externals struct {
	root string

	mu     sync.Mutex
	failed map[string]error
}

func newExternals(root string) *externals {
	return &externals{root: root, failed: map[string]error{}}
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
	key := strconv.Itoa(ver) + "/" + string(flavor)
	e.mu.Lock()
	defer e.mu.Unlock()
	if nodeReady(dir) {
		return dir, nil
	}
	if err, ok := e.failed[key]; ok {
		return "", err
	}
	err := e.extract(ctx, cli, ver, flavor, dir, out)
	if err != nil {
		e.failed[key] = err
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

// Node from the image first, the runner's own builds second
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
	if j.exec.Has(ctx, "node") {
		remember([]string{"node"}, nil)
		return []string{"node"}, nil, nil
	}
	cli, err := j.dockerClient()
	if err != nil {
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
	return nil, nil, fmt.Errorf("no node %d runs in image %s, pick a container image that ships node (%s)", ver, j.imageName(), strings.Join(errs, ", "))
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

package runner

import (
	"archive/tar"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Job dir as seen inside containers
const containerWorkRoot = "/__w"

// One job container plus service sidecars on a private network
type dockerExecutor struct {
	cli      *client.Client
	jobID    string
	jobDir   string
	image    string
	options  string
	services []*v1.TaskService
	log      *logStream
	auth     registryAuth
	// Runner level consent to the docker socket, --privileged, and host binds
	privileged bool

	networkID   string
	containerID string
	serviceIDs  []string
	user        string
}

// Pulls from the instance registry use the job token
type registryAuth struct {
	host  string
	token string
}

func newDockerClient(host string) (*client.Client, error) {
	opts := []client.Opt{client.FromEnv, client.WithAPIVersionNegotiation()}
	if host != "" {
		opts = append(opts, client.WithHost(host))
	}
	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	return cli, nil
}

func (d *dockerExecutor) Describe() string {
	return "docker " + d.image
}

func (d *dockerExecutor) Path(hostPath string) string {
	rel, err := filepath.Rel(d.jobDir, hostPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(hostPath)
	}
	return path.Join(containerWorkRoot, filepath.ToSlash(rel))
}

func (d *dockerExecutor) Start(ctx context.Context) error {
	if _, err := d.cli.Ping(ctx); err != nil {
		return fmt.Errorf("docker daemon unreachable: %w", err)
	}
	netName := "dfcli-" + shortID(d.jobID)
	net, err := d.cli.NetworkCreate(ctx, netName, network.CreateOptions{Driver: "bridge", Labels: d.labels()})
	if err != nil {
		return fmt.Errorf("create network: %w", err)
	}
	d.networkID = net.ID
	for _, svc := range d.services {
		if err := d.startService(ctx, svc); err != nil {
			return fmt.Errorf("service %s: %w", svc.Name, err)
		}
	}
	if err := d.pull(ctx, d.image); err != nil {
		return err
	}
	opts := parseContainerOptions(d.options)
	binds := d.restrict(&opts, nil, "container")
	d.user = opts.user
	cfg := &container.Config{
		Image:      d.image,
		Entrypoint: []string{"tail"},
		Cmd:        []string{"-f", "/dev/null"},
		Env:        opts.env,
		WorkingDir: containerWorkRoot,
		Labels:     d.labels(),
		User:       opts.user,
	}
	if len(opts.entrypoint) > 0 {
		cfg.Entrypoint = opts.entrypoint
		cfg.Cmd = nil
	}
	host := &container.HostConfig{
		Binds:       append([]string{d.jobDir + ":" + containerWorkRoot}, binds...),
		NetworkMode: container.NetworkMode(netName),
		Privileged:  opts.privileged,
		ExtraHosts:  opts.hosts,
		DNS:         opts.dns,
	}
	if d.privileged {
		if _, err := os.Stat("/var/run/docker.sock"); err == nil {
			host.Binds = append(host.Binds, "/var/run/docker.sock:/var/run/docker.sock")
		}
	}
	netCfg := &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{netName: {Aliases: []string{"job"}}}}
	resp, err := d.cli.ContainerCreate(ctx, cfg, host, netCfg, nil, "dfcli-job-"+shortID(d.jobID))
	if err != nil {
		return fmt.Errorf("create job container: %w", err)
	}
	d.containerID = resp.ID
	if err := d.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("start job container: %w", err)
	}
	d.log.Line("Started job container %s from %s", shortID(resp.ID), d.image)
	return nil
}

func (d *dockerExecutor) labels() map[string]string {
	return map[string]string{"dfcli.runner.job": d.jobID}
}

func (d *dockerExecutor) startService(ctx context.Context, svc *v1.TaskService) error {
	if err := d.pull(ctx, svc.Image); err != nil {
		return err
	}
	opts := parseContainerOptions(svc.Options)
	binds := d.restrict(&opts, svc.Volumes, "service "+svc.Name)
	cfg := &container.Config{Image: svc.Image, Env: append(envSlice(svc.Env), opts.env...), Labels: d.labels(), User: opts.user, ExposedPorts: nat.PortSet{}}
	if len(opts.entrypoint) > 0 {
		cfg.Entrypoint = opts.entrypoint
	}
	host := &container.HostConfig{Binds: binds, Privileged: opts.privileged, PortBindings: nat.PortMap{}, ExtraHosts: opts.hosts, DNS: opts.dns}
	for _, p := range svc.Ports {
		hostPort, cPort, found := strings.Cut(p, ":")
		if !found {
			cPort, hostPort = hostPort, ""
		}
		proto := "tcp"
		if cp, pr, ok := strings.Cut(cPort, "/"); ok {
			cPort, proto = cp, pr
		}
		port, err := nat.NewPort(proto, cPort)
		if err != nil {
			return fmt.Errorf("bad port %q: %w", p, err)
		}
		cfg.ExposedPorts[port] = struct{}{}
		host.PortBindings[port] = append(host.PortBindings[port], nat.PortBinding{HostPort: hostPort})
	}
	netName := "dfcli-" + shortID(d.jobID)
	host.NetworkMode = container.NetworkMode(netName)
	netCfg := &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{netName: {Aliases: []string{svc.Name}}}}
	resp, err := d.cli.ContainerCreate(ctx, cfg, host, netCfg, nil, fmt.Sprintf("dfcli-%s-%s", shortID(d.jobID), svc.Name))
	if err != nil {
		return err
	}
	d.serviceIDs = append(d.serviceIDs, resp.ID)
	if err := d.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return err
	}
	d.log.Line("Started service %s from %s", svc.Name, svc.Image)
	return d.waitHealthy(ctx, resp.ID, svc.Name)
}

// Waits for the healthcheck, two minutes at most
func (d *dockerExecutor) waitHealthy(ctx context.Context, id, name string) error {
	deadline := time.Now().Add(2 * time.Minute)
	for {
		insp, err := d.cli.ContainerInspect(ctx, id)
		if err != nil {
			return err
		}
		if insp.State == nil || insp.State.Health == nil {
			return nil
		}
		switch insp.State.Health.Status {
		case container.Healthy:
			d.log.Line("Service %s is healthy", name)
			return nil
		case container.Unhealthy:
			return fmt.Errorf("service %s reported unhealthy", name)
		}
		if !insp.State.Running {
			return fmt.Errorf("service %s exited", name)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("service %s never became healthy", name)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// Names docker accepts as volumes, anything else is a host path
var volumeName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// Drops --privileged and host binds unless the runner allows privileged jobs
func (d *dockerExecutor) restrict(o *containerOptions, volumes []string, what string) []string {
	if o.privileged && !d.privileged {
		o.privileged = false
		d.log.Line("##[warning]%s: --privileged ignored, register the runner with --privileged-jobs to allow it", what)
	}
	var kept []string
	for _, v := range append(append([]string{}, o.volumes...), volumes...) {
		src, _, bind := strings.Cut(v, ":")
		if d.privileged || !bind || volumeName.MatchString(src) || within(d.jobDir, src) {
			kept = append(kept, v)
			continue
		}
		d.log.Line("##[warning]%s: volume %s ignored, host paths need a runner registered with --privileged-jobs", what, v)
	}
	return kept
}

// Pulls before every job, a local copy serves only when the pull fails
func (d *dockerExecutor) pull(ctx context.Context, ref string) error {
	d.log.Line("Pulling image %s", ref)
	opts := image.PullOptions{}
	if d.auth.token != "" && d.auth.host != "" && strings.HasPrefix(ref, d.auth.host+"/") {
		raw, _ := json.Marshal(registry.AuthConfig{Username: "actions", Password: d.auth.token, ServerAddress: d.auth.host})
		opts.RegistryAuth = base64.URLEncoding.EncodeToString(raw)
	}
	rc, err := d.cli.ImagePull(ctx, ref, opts)
	if err == nil {
		err = drainJSONStream(rc, nil)
		rc.Close()
	}
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return fmt.Errorf("pull %s: %w", ref, err)
	}
	if _, inspectErr := d.cli.ImageInspect(ctx, ref); inspectErr == nil {
		d.log.Line("##[warning]Pull of %s failed (%v), using the local copy", ref, err)
		return nil
	}
	return fmt.Errorf("pull %s: %w", ref, err)
}

// One command run inside the job environment
type execSpec struct {
	Cmd []string
	Env []string
	Dir string
	Out io.Writer
}

func (d *dockerExecutor) Exec(ctx context.Context, spec execSpec) (int, error) {
	if d.containerID == "" {
		return -1, fmt.Errorf("job container is not running")
	}
	pidFile := path.Join(containerWorkRoot, "_temp", "pid-"+shortID(fmt.Sprint(time.Now().UnixNano())))
	cmd := append([]string{"/bin/sh", "-c", `echo $$ > "$0"; exec "$@"`, pidFile}, spec.Cmd...)
	created, err := d.cli.ContainerExecCreate(ctx, d.containerID, container.ExecOptions{
		Cmd: cmd, Env: spec.Env, WorkingDir: spec.Dir, AttachStdout: true, AttachStderr: true, User: d.user,
	})
	if err != nil {
		return -1, fmt.Errorf("exec create: %w", err)
	}
	resp, err := d.cli.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{})
	if err != nil {
		return -1, fmt.Errorf("exec attach: %w", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = stdcopy.StdCopy(spec.Out, spec.Out, resp.Reader)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		d.killExec(pidFile)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			d.Kill()
		}
		resp.Close()
		return -1, ctx.Err()
	}
	resp.Close()
	bg, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for {
		insp, err := d.cli.ContainerExecInspect(bg, created.ID)
		if err != nil {
			return -1, fmt.Errorf("exec inspect: %w", err)
		}
		if !insp.Running {
			return insp.ExitCode, nil
		}
		select {
		case <-bg.Done():
			return -1, fmt.Errorf("exec did not finish")
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// Kills the recorded pid, the container itself is the fallback
func (d *dockerExecutor) killExec(pidFile string) {
	bg, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	script := fmt.Sprintf(`p=$(cat "%s" 2>/dev/null); [ -n "$p" ] && kill -KILL -- -"$p" 2>/dev/null; [ -n "$p" ] && kill -KILL "$p" 2>/dev/null; true`, pidFile)
	created, err := d.cli.ContainerExecCreate(bg, d.containerID, container.ExecOptions{Cmd: []string{"/bin/sh", "-c", script}})
	if err != nil {
		return
	}
	_ = d.cli.ContainerExecStart(bg, created.ID, container.ExecStartOptions{})
}

func (d *dockerExecutor) Has(ctx context.Context, bin string) bool {
	code, err := d.Exec(ctx, execSpec{Cmd: []string{"/bin/sh", "-c", "command -v " + bin + " >/dev/null 2>&1"}, Out: io.Discard})
	return err == nil && code == 0
}

func (d *dockerExecutor) Kill() {
	if d.containerID == "" {
		return
	}
	bg, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = d.cli.ContainerKill(bg, d.containerID, "KILL")
}

func (d *dockerExecutor) Close(ctx context.Context) error {
	bg, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if d.containerID != "" {
		// Root owned files from the container would block host cleanup
		_, _ = d.Exec(bg, execSpec{Cmd: []string{"/bin/sh", "-c", "rm -rf " + containerWorkRoot + "/* " + containerWorkRoot + "/.[!.]* 2>/dev/null; true"}, Out: io.Discard})
		_ = d.cli.ContainerRemove(bg, d.containerID, container.RemoveOptions{Force: true, RemoveVolumes: true})
		d.containerID = ""
	}
	for _, id := range d.serviceIDs {
		_ = d.cli.ContainerRemove(bg, id, container.RemoveOptions{Force: true, RemoveVolumes: true})
	}
	d.serviceIDs = nil
	if d.networkID != "" {
		_ = d.cli.NetworkRemove(bg, d.networkID)
		d.networkID = ""
	}
	return nil
}

// Throwaway container spec used by docker actions
type containerRun struct {
	Image      string
	Cmd        []string
	Entrypoint []string
	Env        []string
	Dir        string
	Binds      []string
	Network    string
	Out        io.Writer
}

// Starts a throwaway container and waits for exit
func runContainer(ctx context.Context, cli *client.Client, spec containerRun) (int, error) {
	cfg := &container.Config{Image: spec.Image, Cmd: spec.Cmd, Env: spec.Env, WorkingDir: spec.Dir}
	if spec.Entrypoint != nil {
		cfg.Entrypoint = spec.Entrypoint
	}
	host := &container.HostConfig{Binds: spec.Binds}
	if spec.Network != "" {
		host.NetworkMode = container.NetworkMode(spec.Network)
	}
	resp, err := cli.ContainerCreate(ctx, cfg, host, nil, nil, "")
	if err != nil {
		return -1, fmt.Errorf("create container: %w", err)
	}
	bg, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	defer func() {
		_ = cli.ContainerRemove(bg, resp.ID, container.RemoveOptions{Force: true, RemoveVolumes: true})
	}()
	waitCh, errCh := cli.ContainerWait(ctx, resp.ID, container.WaitConditionNextExit)
	if err := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return -1, fmt.Errorf("start container: %w", err)
	}
	logDone := make(chan struct{})
	logs, err := cli.ContainerLogs(ctx, resp.ID, container.LogsOptions{ShowStdout: true, ShowStderr: true, Follow: true})
	if err == nil {
		go func() {
			defer close(logDone)
			defer logs.Close()
			_, _ = stdcopy.StdCopy(spec.Out, spec.Out, logs)
		}()
	} else {
		close(logDone)
	}
	select {
	case res := <-waitCh:
		select {
		case <-logDone:
		case <-time.After(2 * time.Second):
		}
		return int(res.StatusCode), nil
	case err := <-errCh:
		_ = cli.ContainerKill(bg, resp.ID, "KILL")
		if ctx.Err() != nil {
			return -1, ctx.Err()
		}
		return -1, err
	case <-ctx.Done():
		_ = cli.ContainerKill(bg, resp.ID, "KILL")
		return -1, ctx.Err()
	}
}

// Builds a Dockerfile directory into a tag
func buildImage(ctx context.Context, cli *client.Client, dir, dockerfile, tag string, out io.Writer) error {
	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(tarDirectory(dir, pw))
	}()
	resp, err := cli.ImageBuild(ctx, pr, build.ImageBuildOptions{Tags: []string{tag}, Dockerfile: dockerfile, Remove: true, ForceRemove: true})
	if err != nil {
		return fmt.Errorf("build %s: %w", tag, err)
	}
	defer resp.Body.Close()
	return drainJSONStream(resp.Body, out)
}

// Reads a docker progress stream, surfacing errors and stream text
func drainJSONStream(r io.Reader, out io.Writer) error {
	dec := json.NewDecoder(r)
	for {
		var msg struct {
			Stream string `json:"stream"`
			Error  string `json:"error"`
		}
		if err := dec.Decode(&msg); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if msg.Error != "" {
			return fmt.Errorf("%s", strings.TrimSpace(msg.Error))
		}
		if out != nil && msg.Stream != "" {
			io.WriteString(out, msg.Stream)
		}
	}
}

func tarDirectory(dir string, w io.Writer) error {
	tw := tar.NewWriter(w)
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil || rel == "." {
			return nil
		}
		if info.IsDir() && info.Name() == ".git" {
			return filepath.SkipDir
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			hdr.Linkname = target
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
	if err != nil {
		return err
	}
	return tw.Close()
}

// Subset of docker run flags honored from options strings
type containerOptions struct {
	privileged bool
	user       string
	volumes    []string
	env        []string
	entrypoint []string
	hosts      []string
	dns        []string
}

func parseContainerOptions(s string) containerOptions {
	var o containerOptions
	args := splitShellWords(s)
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() string {
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		flag, val, hasVal := strings.Cut(a, "=")
		get := func() string {
			if hasVal {
				return val
			}
			return next()
		}
		switch flag {
		case "--privileged":
			o.privileged = true
		case "-u", "--user":
			o.user = get()
		case "-v", "--volume":
			o.volumes = append(o.volumes, get())
		case "-e", "--env":
			o.env = append(o.env, get())
		case "--entrypoint":
			o.entrypoint = []string{get()}
		case "--add-host":
			o.hosts = append(o.hosts, get())
		case "--dns":
			o.dns = append(o.dns, get())
		default:
			if strings.HasPrefix(flag, "-") && !hasVal && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
			}
		}
	}
	return o
}

// Minimal shell word splitting with quotes and backslashes
func splitShellWords(s string) []string {
	var out []string
	var cur strings.Builder
	inWord := false
	quote := byte(0)
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case quote != 0:
			if ch == quote {
				quote = 0
			} else if ch == '\\' && quote == '"' && i+1 < len(s) {
				i++
				cur.WriteByte(s[i])
			} else {
				cur.WriteByte(ch)
			}
		case ch == '\'' || ch == '"':
			quote = ch
			inWord = true
		case ch == '\\' && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
			inWord = true
		case ch == ' ' || ch == '\t' || ch == '\n':
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteByte(ch)
			inWord = true
		}
	}
	if inWord {
		out = append(out, cur.String())
	}
	return out
}

func envSlice(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

func shortID(s string) string {
	s = strings.ReplaceAll(s, "-", "")
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// Host of a server url, used to match registry references
func registryHost(serverURL string) string {
	u, err := url.Parse(serverURL)
	if err != nil {
		return ""
	}
	return u.Host
}

func imagePullOptions() image.PullOptions {
	return image.PullOptions{}
}

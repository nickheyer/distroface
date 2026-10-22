package runner

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"
	"github.com/google/uuid"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Container paths of the job dir and tool cache
const (
	containerWorkRoot  = "/__w"
	containerToolCache = "/__t"
)

// PATH docker gives images that declare none
const defaultContainerPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// Env var marking an exec's process tree for signals
const execMarker = "DFCLI_EXEC_ID"

// Grace between terminate and kill of a cancelled step
const (
	execTermGrace = 3 * time.Second
	execKillGrace = 5 * time.Second
)

// Signals every process carrying the marker, descendants before parents
const killTreeScript = `m="$1"; s="$2"
kt() { for d in /proc/[0-9]*; do pp=; while read -r k v _; do [ "$k" = "PPid:" ] && { pp=$v; break; }; done 2>/dev/null < "$d/status"; [ "$pp" = "$1" ] && kt "${d#/proc/}" "$2"; done; kill -"$2" "$1" 2>/dev/null; }
for d in /proc/[0-9]*; do p=${d#/proc/}; [ "$p" = "$$" ] && continue; tr '\0' '\n' < "$d/environ" 2>/dev/null | grep -qxF "$m" && kt "$p" "$s"; done
exit 0`

// What the job context reports about containers
type executorInfo struct {
	containerID string
	network     string
	services    map[string]serviceInfo
}

type serviceInfo struct {
	id    string
	ports map[string]string
}

// One job container plus service sidecars on a private network
type dockerExecutor struct {
	cli      *client.Client
	jobID    string
	jobDir   string
	image    string
	options  string
	services []*v1.TaskService
	log      *logStream
	// Encoded registry auth for a reference, empty if anonymous
	auth func(ref string) string
	// Host dir of the runner's node builds, bound read only
	externals string
	// Runner level consent to the docker socket, --privileged, and host binds
	privileged bool
	// Host entries every container of the job receives
	hosts  []string
	labels map[string]string
	// Host tool cache dir shared across jobs
	toolCache string

	networkID   string
	networkName string
	containerID string
	serviceIDs  []string
	serviceInfo map[string]serviceInfo
	user        string
	// Container PATH that GITHUB_PATH entries go ahead of
	path string
	// Guards containerID between a shutdown kill and the teardown
	idMu sync.Mutex
}

func (d *dockerExecutor) container() string {
	d.idMu.Lock()
	defer d.idMu.Unlock()
	return d.containerID
}

func (d *dockerExecutor) setContainer(id string) {
	d.idMu.Lock()
	d.containerID = id
	d.idMu.Unlock()
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
	if d.toolCache != "" {
		if rel, ok := relInside(d.toolCache, hostPath); ok {
			return path.Join(containerToolCache, filepath.ToSlash(rel))
		}
	}
	rel, ok := relInside(d.jobDir, hostPath)
	if !ok {
		return filepath.ToSlash(hostPath)
	}
	return path.Join(containerWorkRoot, filepath.ToSlash(rel))
}

// Path under root, false when it escapes
func relInside(root, target string) (string, bool) {
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

func (d *dockerExecutor) Info() executorInfo {
	return executorInfo{containerID: d.container(), network: d.networkName, services: d.serviceInfo}
}

func (d *dockerExecutor) Start(ctx context.Context) error {
	if _, err := d.cli.Ping(ctx); err != nil {
		return fmt.Errorf("docker daemon unreachable: %w", err)
	}
	d.networkName = "dfcli-" + shortID(d.jobID)
	net, err := d.cli.NetworkCreate(ctx, d.networkName, network.CreateOptions{Driver: "bridge", Labels: d.labels})
	if err != nil {
		return fmt.Errorf("create network: %w", err)
	}
	d.networkID = net.ID
	d.serviceInfo = map[string]serviceInfo{}
	for _, svc := range d.services {
		if err := d.startService(ctx, svc); err != nil {
			return fmt.Errorf("service %s: %w", svc.Name, err)
		}
	}
	if err := d.pull(ctx, d.image); err != nil {
		return err
	}
	opts := parseContainerOptions(d.options)
	opts.hosts = append(opts.hosts, d.hosts...)
	binds := d.restrict(&opts, nil, "container")
	d.user = opts.user
	cfg := &container.Config{
		Image:      d.image,
		Entrypoint: []string{"tail"},
		Cmd:        []string{"-f", "/dev/null"},
		Env:        opts.env,
		WorkingDir: containerWorkRoot,
		Labels:     d.labels,
		User:       opts.user,
		Hostname:   opts.hostname,
	}
	if len(opts.entrypoint) > 0 {
		cfg.Entrypoint = opts.entrypoint
		cfg.Cmd = nil
	}
	host := &container.HostConfig{
		Binds:       append([]string{d.jobDir + ":" + containerWorkRoot}, binds...),
		NetworkMode: container.NetworkMode(d.networkName),
		Privileged:  opts.privileged,
		ExtraHosts:  opts.hosts,
		DNS:         opts.dns,
	}
	if d.toolCache != "" {
		host.Binds = append(host.Binds, d.toolCache+":"+containerToolCache)
	}
	if d.externals != "" {
		if err := os.MkdirAll(d.externals, 0o755); err == nil {
			host.Binds = append(host.Binds, d.externals+":"+containerExternals+":ro")
		}
	}
	if d.privileged {
		if sock := dockerSocket(); sock != "" {
			host.Binds = append(host.Binds, sock+":/var/run/docker.sock")
		}
	}
	netCfg := &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{d.networkName: {Aliases: []string{"job"}}}}
	resp, err := d.cli.ContainerCreate(ctx, cfg, host, netCfg, nil, "dfcli-job-"+shortID(d.jobID))
	if err != nil {
		return fmt.Errorf("create job container: %w", err)
	}
	d.setContainer(resp.ID)
	if err := d.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("start job container: %w", err)
	}
	d.path = defaultContainerPath
	if insp, err := d.cli.ContainerInspect(ctx, resp.ID); err == nil && insp.Config != nil {
		if p := envValue(insp.Config.Env, "PATH"); p != "" {
			d.path = p
		}
	}
	d.log.Line("Started job container %s from %s", shortID(resp.ID), d.image)
	return nil
}

// Value of a KEY=value entry, empty when absent
func envValue(env []string, key string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

// Env with dirs ahead of the step or base PATH
func prependPath(env, adds []string, base, sep string) []string {
	if len(adds) == 0 {
		return env
	}
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, "PATH") {
			base = v
			continue
		}
		out = append(out, kv)
	}
	value := strings.Join(adds, sep)
	if base != "" {
		value += sep + base
	}
	return append(out, "PATH="+value)
}

// Daemon socket on this host, empty when absent
func dockerSocket() string {
	for _, p := range []string{"/var/run/docker.sock", filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "docker.sock")} {
		if p == "docker.sock" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func (d *dockerExecutor) startService(ctx context.Context, svc *v1.TaskService) error {
	if err := d.pull(ctx, svc.Image); err != nil {
		return err
	}
	opts := parseContainerOptions(svc.Options)
	opts.hosts = append(opts.hosts, d.hosts...)
	binds := d.restrict(&opts, svc.Volumes, "service "+svc.Name)
	cfg := &container.Config{
		Image: svc.Image, Env: append(envSlice(svc.Env), opts.env...), Labels: d.labels, User: opts.user,
		ExposedPorts: nat.PortSet{}, Healthcheck: opts.health, Hostname: opts.hostname,
	}
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
	host.NetworkMode = container.NetworkMode(d.networkName)
	netCfg := &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{d.networkName: {Aliases: []string{svc.Name}}}}
	resp, err := d.cli.ContainerCreate(ctx, cfg, host, netCfg, nil, fmt.Sprintf("dfcli-%s-%s", shortID(d.jobID), svc.Name))
	if err != nil {
		return err
	}
	d.serviceIDs = append(d.serviceIDs, resp.ID)
	if err := d.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return err
	}
	d.log.Line("Started service %s from %s", svc.Name, svc.Image)
	info := serviceInfo{id: resp.ID, ports: map[string]string{}}
	if insp, err := d.cli.ContainerInspect(ctx, resp.ID); err == nil && insp.NetworkSettings != nil {
		for port, bindings := range insp.NetworkSettings.Ports {
			for _, b := range bindings {
				if b.HostPort != "" {
					info.ports[port.Port()] = b.HostPort
				}
			}
		}
	}
	d.serviceInfo[svc.Name] = info
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
	auth := ""
	if d.auth != nil {
		auth = d.auth(ref)
	}
	return pullImage(ctx, d.cli, ref, auth, d.log)
}

// Base64 auth config docker's pull api expects
func encodeRegistryAuth(host, user, pass string) string {
	raw, _ := json.Marshal(registry.AuthConfig{Username: user, Password: pass, ServerAddress: host})
	return base64.URLEncoding.EncodeToString(raw)
}

// Pull with an optional encoded auth, local copy on failure
func pullImage(ctx context.Context, cli *client.Client, ref, registryAuth string, out io.Writer) error {
	fmt.Fprintf(out, "Pulling image %s\n", ref)
	rc, err := cli.ImagePull(ctx, ref, image.PullOptions{RegistryAuth: registryAuth})
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
	if _, inspectErr := cli.ImageInspect(ctx, ref); inspectErr == nil {
		fmt.Fprintf(out, "##[warning]Pull of %s failed (%v), using the local copy\n", ref, err)
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
	// Overrides the job user, root for cleanup
	User string
	// GITHUB_PATH entries placed ahead of PATH, newest first
	Path []string
}

// Direct exec, dash would drop hyphenated env names
func (d *dockerExecutor) Exec(ctx context.Context, spec execSpec) (int, error) {
	id := d.container()
	if id == "" {
		return -1, fmt.Errorf("job container is not running")
	}
	marker := execMarker + "=" + uuid.NewString()
	env := append(prependPath(spec.Env, spec.Path, d.path, ":"), marker)
	user := d.user
	if spec.User != "" {
		user = spec.User
	}
	created, err := d.cli.ContainerExecCreate(ctx, id, container.ExecOptions{
		Cmd: spec.Cmd, Env: env, WorkingDir: spec.Dir, AttachStdout: true, AttachStderr: true, User: user,
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
		d.signalTree(marker, "TERM")
		select {
		case <-done:
		case <-time.After(execTermGrace):
			d.signalTree(marker, "KILL")
			select {
			case <-done:
			case <-time.After(execKillGrace):
			}
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

// Signals the step's whole process tree inside the container
func (d *dockerExecutor) signalTree(marker, sig string) {
	bg, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id := d.container()
	if id == "" {
		return
	}
	created, err := d.cli.ContainerExecCreate(bg, id, container.ExecOptions{Cmd: []string{"/bin/sh", "-c", killTreeScript, "sh", marker, sig}, User: "0"})
	if err != nil {
		return
	}
	_ = d.cli.ContainerExecStart(bg, created.ID, container.ExecStartOptions{})
}

func (d *dockerExecutor) Has(ctx context.Context, bin string) bool {
	return d.Which(ctx, bin) != ""
}

// Absolute path of a binary on the container's own PATH
func (d *dockerExecutor) Which(ctx context.Context, bin string) string {
	var buf bytes.Buffer
	code, err := d.Exec(ctx, execSpec{Cmd: []string{"/bin/sh", "-c", `command -v -- "$1"`, "sh", bin}, Out: &buf})
	if err != nil || code != 0 {
		return ""
	}
	p := strings.TrimSpace(buf.String())
	if !strings.HasPrefix(p, "/") {
		return ""
	}
	return p
}

func (d *dockerExecutor) Kill() {
	id := d.container()
	if id == "" {
		return
	}
	bg, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = d.cli.ContainerKill(bg, id, "KILL")
}

func (d *dockerExecutor) Close(ctx context.Context) error {
	bg, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if id := d.container(); id != "" {
		// Root owned files from the container would block host cleanup
		clean := execSpec{Cmd: []string{"/bin/sh", "-c", "rm -rf " + containerWorkRoot + "/* " + containerWorkRoot + "/.[!.]* 2>/dev/null; true"}, Out: io.Discard, User: "0"}
		if _, err := d.Exec(bg, clean); err != nil {
			clean.User = ""
			_, _ = d.Exec(bg, clean)
		}
		_ = d.cli.ContainerRemove(bg, id, container.RemoveOptions{Force: true, RemoveVolumes: true})
		d.setContainer("")
	}
	for _, id := range d.serviceIDs {
		_ = d.cli.ContainerRemove(bg, id, container.RemoveOptions{Force: true, RemoveVolumes: true})
	}
	d.serviceIDs = nil
	if d.networkID != "" {
		// Endpoints of force removed containers linger briefly
		for range 5 {
			if err := d.cli.NetworkRemove(bg, d.networkID); err == nil || errdefs.IsNotFound(err) {
				break
			}
			time.Sleep(time.Second)
		}
		d.networkID = ""
	}
	return nil
}

// Throwaway container spec used by docker actions and buildkit
type containerRun struct {
	Image       string
	Cmd         []string
	Entrypoint  []string
	Env         []string
	Dir         string
	User        string
	Binds       []string
	Network     string
	Hosts       []string
	SecurityOpt []string
	Devices     []container.DeviceMapping
	Labels      map[string]string
	Out         io.Writer
}

// Starts a throwaway container and waits for exit
func runContainer(ctx context.Context, cli *client.Client, spec containerRun) (int, error) {
	cfg := &container.Config{Image: spec.Image, Cmd: spec.Cmd, Env: spec.Env, WorkingDir: spec.Dir, Labels: spec.Labels, User: spec.User}
	if spec.Entrypoint != nil {
		cfg.Entrypoint = spec.Entrypoint
	}
	host := &container.HostConfig{Binds: spec.Binds, SecurityOpt: spec.SecurityOpt, ExtraHosts: spec.Hosts}
	if len(spec.Devices) > 0 {
		host.Devices = spec.Devices
	}
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
	// The log copier stops before the caller flushes
	drain := func(wait time.Duration) {
		select {
		case <-logDone:
		case <-time.After(wait):
		}
	}
	select {
	case res := <-waitCh:
		drain(10 * time.Second)
		return int(res.StatusCode), nil
	case err := <-errCh:
		_ = cli.ContainerKill(bg, resp.ID, "KILL")
		drain(5 * time.Second)
		if ctx.Err() != nil {
			return -1, ctx.Err()
		}
		return -1, err
	case <-ctx.Done():
		_ = cli.ContainerKill(bg, resp.ID, "KILL")
		drain(5 * time.Second)
		return -1, ctx.Err()
	}
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

// Subset of docker run flags honored from options strings
type containerOptions struct {
	privileged bool
	user       string
	hostname   string
	volumes    []string
	env        []string
	entrypoint []string
	hosts      []string
	dns        []string
	health     *container.HealthConfig
}

func parseContainerOptions(s string) containerOptions {
	var o containerOptions
	args := splitShellWords(s)
	health := func() *container.HealthConfig {
		if o.health == nil {
			o.health = &container.HealthConfig{}
		}
		return o.health
	}
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
		case "-h", "--hostname":
			o.hostname = get()
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
		case "--health-cmd":
			health().Test = []string{"CMD-SHELL", get()}
		case "--health-interval":
			health().Interval, _ = time.ParseDuration(get())
		case "--health-timeout":
			health().Timeout, _ = time.ParseDuration(get())
		case "--health-start-period":
			health().StartPeriod, _ = time.ParseDuration(get())
		case "--health-retries":
			health().Retries, _ = strconv.Atoi(get())
		case "--no-healthcheck":
			o.health = &container.HealthConfig{Test: []string{"NONE"}}
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

// Names resolving to loopback on the runner host map to the docker gateway
func gatewayHosts(urls ...string) (hosts, unreachable []string) {
	seen := map[string]bool{}
	for _, raw := range urls {
		u, err := url.Parse(raw)
		if err != nil {
			continue
		}
		h := strings.ToLower(u.Hostname())
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		if ip := net.ParseIP(h); ip != nil {
			if ip.IsLoopback() {
				unreachable = append(unreachable, h)
			}
			continue
		}
		if h == "localhost" {
			unreachable = append(unreachable, h)
			continue
		}
		addrs, err := net.LookupHost(h)
		if err != nil || len(addrs) == 0 {
			continue
		}
		loopback := true
		for _, a := range addrs {
			if ip := net.ParseIP(a); ip == nil || !ip.IsLoopback() {
				loopback = false
				break
			}
		}
		if loopback {
			hosts = append(hosts, h+":host-gateway")
		}
	}
	return hosts, unreachable
}

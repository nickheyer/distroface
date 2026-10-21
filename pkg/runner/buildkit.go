package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

const (
	defaultBuildKitImage = "docker.io/moby/buildkit:rootless"
	buildKitStateDir     = "/home/user/.local/share/buildkit"
	buildKitDockerDir    = "/home/user/.docker"
	buildKitConfigDir    = "/home/user/.config/buildkit"
	buildKitCAPath       = buildKitConfigDir + "/ca.pem"
	buildKitTailLines    = 25
)

// Inputs of docker/build-push-action the runner honors
type buildInput struct {
	Context       string
	File          string
	Target        string
	Tags          []string
	Labels        []string
	Annotations   []string
	BuildArgs     []string
	Platforms     []string
	CacheFrom     []string
	CacheTo       []string
	Secrets       []string
	SecretFiles   []string
	BuildContexts []string
	Outputs       []string
	AddHosts      []string
	Push          bool
	Load          bool
	NoCache       bool
	Pull          bool
	Provenance    string
	SBOM          string
	SSH           string
}

func parseBuildInput(with map[string]string) buildInput {
	get := func(k string) string { return strings.TrimSpace(with[k]) }
	return buildInput{
		Context: get("context"), File: get("file"), Target: get("target"),
		Tags: splitList(with["tags"]), Labels: splitLines(with["labels"]), Annotations: splitLines(with["annotations"]),
		BuildArgs: splitLines(with["build-args"]), Platforms: splitList(with["platforms"]),
		CacheFrom: splitLines(with["cache-from"]), CacheTo: splitLines(with["cache-to"]),
		Secrets: splitLines(with["secrets"]), SecretFiles: splitLines(with["secret-files"]), BuildContexts: splitLines(with["build-contexts"]),
		Outputs: splitLines(with["outputs"]), AddHosts: splitList(with["add-hosts"]),
		Push: boolInput(with["push"]), Load: boolInput(with["load"]), NoCache: boolInput(with["no-cache"]), Pull: boolInput(with["pull"]),
		Provenance: get("provenance"), SBOM: get("sbom"), SSH: get("ssh"),
	}
}

func boolInput(s string) bool {
	return strings.EqualFold(strings.TrimSpace(s), "true")
}

// Newline separated entries, blanks dropped
func splitLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// Newline or comma separated entries, blanks dropped
func splitList(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		for _, item := range strings.Split(line, ",") {
			if item = strings.TrimSpace(item); item != "" {
				out = append(out, item)
			}
		}
	}
	return out
}

// Build inputs resolved to the container view buildkit sees
type buildPlan struct {
	ContextDir     string
	RemoteContext  string
	DockerfileDir  string
	DockerfileName string
	MetadataFile   string
	SecretFiles    map[string]string
	LocalContexts  map[string]string
	RemoteContexts map[string]string
	InsecureHost   string
	LoadTar        string
}

// Assembles buildctl arguments, notes name what was skipped
func buildctlArgs(in buildInput, p buildPlan) ([]string, []string, error) {
	var notes []string
	args := []string{"build", "--frontend", "dockerfile.v0", "--progress", "plain"}
	if p.RemoteContext != "" {
		args = append(args, "--opt", "context="+p.RemoteContext)
	} else {
		args = append(args, "--local", "context="+p.ContextDir, "--local", "dockerfile="+p.DockerfileDir)
	}
	if p.DockerfileName != "" {
		args = append(args, "--opt", "filename="+p.DockerfileName)
	}
	if in.Target != "" {
		args = append(args, "--opt", "target="+in.Target)
	}
	for _, kv := range in.BuildArgs {
		if !strings.Contains(kv, "=") {
			notes = append(notes, "build-arg "+kv+" has no value and was skipped")
			continue
		}
		args = append(args, "--opt", "build-arg:"+kv)
	}
	for _, kv := range in.Labels {
		if !strings.Contains(kv, "=") {
			notes = append(notes, "label "+kv+" has no value and was skipped")
			continue
		}
		args = append(args, "--opt", "label:"+kv)
	}
	if len(in.Platforms) > 0 {
		args = append(args, "--opt", "platform="+strings.Join(in.Platforms, ","))
	}
	if len(in.AddHosts) > 0 {
		args = append(args, "--opt", "add-host="+strings.Join(in.AddHosts, ","))
	}
	if in.NoCache {
		args = append(args, "--no-cache")
	}
	if in.Pull {
		args = append(args, "--opt", "image-resolve-mode=pull")
	}
	for _, c := range in.CacheFrom {
		if strings.HasPrefix(c, "type=gha") {
			notes = append(notes, "cache-from type=gha is not available here, the runner's buildkit state volume caches layers instead")
			continue
		}
		args = append(args, "--import-cache", c)
	}
	for _, c := range in.CacheTo {
		if strings.HasPrefix(c, "type=gha") {
			notes = append(notes, "cache-to type=gha is not available here, the runner's buildkit state volume caches layers instead")
			continue
		}
		args = append(args, "--export-cache", c)
	}
	for _, id := range sortedKeys(p.SecretFiles) {
		args = append(args, "--secret", "id="+id+",src="+p.SecretFiles[id])
	}
	for _, name := range sortedKeys(p.LocalContexts) {
		args = append(args, "--local", name+"="+p.LocalContexts[name], "--opt", "context:"+name+"=local:"+name)
	}
	for _, name := range sortedKeys(p.RemoteContexts) {
		args = append(args, "--opt", "context:"+name+"="+p.RemoteContexts[name])
	}
	if in.Provenance != "" {
		notes = append(notes, "provenance attestations are not produced by this runner")
	}
	if in.SBOM != "" {
		notes = append(notes, "sbom attestations are not produced by this runner")
	}
	if in.SSH != "" {
		notes = append(notes, "ssh agent forwarding into the build is not supported")
	}
	if p.MetadataFile != "" {
		args = append(args, "--metadata-file", p.MetadataFile)
	}
	annotations, bad := annotationAttrs(in.Annotations)
	for _, b := range bad {
		notes = append(notes, "annotation "+b+" is not [type:]key=value and was skipped")
	}
	switch {
	case len(in.Outputs) > 0:
		for _, o := range in.Outputs {
			args = append(args, "--output", o+annotations)
		}
	case in.Push && in.Load:
		return nil, nil, errors.New("push and load cannot both be set")
	case in.Push:
		if len(in.Tags) == 0 {
			return nil, nil, errors.New("push needs at least one tag")
		}
		// Plain http instances are declared in buildkitd.toml, the insecure
		// output flag would pin the push to a tls attempt that never falls back
		args = append(args, "--output", "type=image,"+nameAttr(in.Tags)+",push=true"+annotations)
	case in.Load:
		if len(in.Platforms) > 1 {
			return nil, nil, errors.New("load supports a single platform")
		}
		out := "type=docker,dest=" + p.LoadTar
		if len(in.Tags) > 0 {
			out += "," + nameAttr(in.Tags)
		}
		args = append(args, "--output", out)
	default:
		if len(in.Tags) > 0 {
			notes = append(notes, "neither push nor load is set, the image is built and discarded")
		}
	}
	return args, notes, nil
}

// Image names as one csv field, quoted when several
func nameAttr(tags []string) string {
	if len(tags) == 1 {
		return "name=" + tags[0]
	}
	return `"name=` + strings.Join(tags, ",") + `"`
}

// Output attrs for [type:]key=value annotations, the way buildx spells them
func annotationAttrs(entries []string) (string, []string) {
	var sb strings.Builder
	var bad []string
	for _, e := range entries {
		kv := e
		typ := ""
		if t, rest, found := strings.Cut(e, ":"); found && !strings.Contains(t, "=") {
			typ, kv = t, rest
		}
		key, value, found := strings.Cut(kv, "=")
		key = strings.TrimSpace(key)
		if !found || key == "" {
			bad = append(bad, e)
			continue
		}
		switch typ {
		case "", "manifest", "index", "manifest-descriptor", "index-descriptor":
		default:
			bad = append(bad, e)
			continue
		}
		attr := "annotation"
		if typ != "" {
			attr += "-" + typ
		}
		sb.WriteString(",")
		sb.WriteString(csvField(attr + "." + key + "=" + value))
	}
	return sb.String(), bad
}

// Quotes a csv field holding commas or quotes
func csvField(s string) string {
	if !strings.ContainsAny(s, `,"`) {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// Registry host of an image reference, docker.io when unqualified
func tagHost(tag string) string {
	first, _, found := strings.Cut(tag, "/")
	if !found {
		return "docker.io"
	}
	if strings.ContainsAny(first, ".:") || first == "localhost" {
		return first
	}
	return "docker.io"
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

var remoteContextPattern = regexp.MustCompile(`^(https?://|git://|ssh://|git@|github\.com/)`)

func isRemoteContext(s string) bool {
	return remoteContextPattern.MatchString(s)
}

// Job dir path in the container view, false outside it
func containerPath(jobDir, hostPath string) (string, bool) {
	rel, err := filepath.Rel(jobDir, hostPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return path.Join(containerWorkRoot, filepath.ToSlash(rel)), true
}

// Resolver settings handed to buildkitd for the build sandboxes
type dnsConfig struct {
	nameservers []string
	search      []string
}

// Nameservers and search domains from resolv.conf text, loopback dropped
func parseResolvConf(data string) *dnsConfig {
	cfg := &dnsConfig{}
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		fields := strings.Fields(line)
		switch fields[0] {
		case "nameserver":
			if len(fields) > 1 && !isLoopbackAddr(fields[1]) {
				cfg.nameservers = append(cfg.nameservers, fields[1])
			}
		case "search":
			cfg.search = append(cfg.search, fields[1:]...)
		}
	}
	return cfg
}

func isLoopbackAddr(s string) bool {
	host := s
	if i := strings.IndexByte(s, '%'); i >= 0 {
		host = s[:i]
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Default bridge resolvers, buildkit drops the embedded 127.0.0.11
func probeDaemonDNS(ctx context.Context, cli *client.Client, image string) (*dnsConfig, error) {
	var buf bytes.Buffer
	code, err := runContainer(ctx, cli, containerRun{Image: image, Entrypoint: []string{"cat"}, Cmd: []string{"/etc/resolv.conf"}, Out: &buf})
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("resolv.conf probe exited with code %d", code)
	}
	cfg := parseResolvConf(buf.String())
	if len(cfg.nameservers) == 0 {
		return nil, errors.New("the daemon's default network lists no usable nameserver")
	}
	return cfg, nil
}

// Registry and dns blocks of buildkitd.toml, empty when nothing applies
func buildkitdConfig(host string, plainHTTP, ca bool, dns *dnsConfig) string {
	var sb strings.Builder
	if dns != nil && len(dns.nameservers) > 0 {
		sb.WriteString("[dns]\n")
		fmt.Fprintf(&sb, "  nameservers = [%s]\n", quoteList(dns.nameservers))
		if len(dns.search) > 0 {
			fmt.Fprintf(&sb, "  searchDomains = [%s]\n", quoteList(dns.search))
		}
	}
	if host != "" && (plainHTTP || ca) {
		fmt.Fprintf(&sb, "[registry.%q]\n", host)
		if plainHTTP {
			sb.WriteString("  http = true\n")
		}
		if ca {
			fmt.Fprintf(&sb, "  ca = [%q]\n", buildKitCAPath)
		}
	}
	return sb.String()
}

func quoteList(items []string) string {
	quoted := make([]string, len(items))
	for i, it := range items {
		quoted[i] = fmt.Sprintf("%q", it)
	}
	return strings.Join(quoted, ", ")
}

// Volume name docker accepts for one runner slot
func buildKitVolumeName(runner string, slot int) string {
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		}
		return '-'
	}, runner)
	clean = strings.Trim(clean, "-._")
	if clean == "" {
		clean = "runner"
	}
	return fmt.Sprintf("dfcli-buildkit-%s-%d", clean, slot)
}

// Platforms the runner's daemon can build natively or by emulation
func runnerPlatforms() []string {
	host := "linux/" + runtime.GOARCH
	out := []string{host}
	for _, p := range []string{"linux/amd64", "linux/arm64"} {
		if p != host {
			out = append(out, p)
		}
	}
	return out
}

// Keeps the last lines of a stream for error messages
type tailWriter struct {
	out     io.Writer
	max     int
	lines   []string
	partial []byte
}

func newTailWriter(out io.Writer, max int) *tailWriter {
	return &tailWriter{out: out, max: max}
}

func (t *tailWriter) Write(p []byte) (int, error) {
	n, err := t.out.Write(p)
	data := append(t.partial, p...)
	t.partial = nil
	for {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			break
		}
		t.push(string(data[:i]))
		data = data[i+1:]
	}
	if len(data) > 0 {
		t.partial = append([]byte(nil), data...)
	}
	return n, err
}

func (t *tailWriter) push(line string) {
	line = strings.TrimRight(line, "\r")
	if strings.TrimSpace(line) == "" {
		return
	}
	t.lines = append(t.lines, line)
	if len(t.lines) > t.max {
		t.lines = t.lines[len(t.lines)-t.max:]
	}
}

func (t *tailWriter) String() string {
	lines := t.lines
	if len(t.partial) > 0 {
		lines = append(append([]string(nil), lines...), string(t.partial))
	}
	return strings.Join(lines, "\n")
}

// Maps inputs onto host and container paths, returns a cleanup
func (j *job) prepareBuild(in buildInput) (buildPlan, func(), error) {
	plan := buildPlan{SecretFiles: map[string]string{}, LocalContexts: map[string]string{}, RemoteContexts: map[string]string{}}
	var scratch []string
	cleanup := func() {
		for _, p := range scratch {
			_ = j.removeAll(p)
		}
	}
	fail := func(err error) (buildPlan, func(), error) {
		cleanup()
		return plan, func() {}, err
	}
	inView := func(hostPath, what string) (string, error) {
		c, ok := containerPath(j.dir, hostPath)
		if !ok {
			return "", fmt.Errorf("%s %s must live under the job directory", what, hostPath)
		}
		return c, nil
	}

	ctxIn := in.Context
	if ctxIn == "" {
		ctxIn = "."
	}
	hostCtx := ""
	switch {
	case ctxIn == "{{defaultContext}}":
		hostCtx = j.workspace
	case isRemoteContext(ctxIn):
		plan.RemoteContext = ctxIn
	default:
		p, err := j.hostPattern(ctxIn)
		if err != nil {
			return fail(err)
		}
		hostCtx = p
	}
	if hostCtx != "" {
		info, err := j.stat(hostCtx)
		if err != nil || !info.IsDir() {
			return fail(fmt.Errorf("context %s is not a directory", ctxIn))
		}
		if entries, _ := j.readDir(hostCtx); len(entries) == 0 {
			if filepath.Clean(hostCtx) == filepath.Clean(j.workspace) {
				return fail(errors.New("the workspace is empty, checkout the repository or set context"))
			}
			return fail(fmt.Errorf("context %s is empty", ctxIn))
		}
		c, err := inView(hostCtx, "context")
		if err != nil {
			return fail(err)
		}
		plan.ContextDir = c
	}

	switch {
	case in.File == "" && plan.RemoteContext != "":
		plan.DockerfileName = "Dockerfile"
	case in.File == "":
		plan.DockerfileDir = plan.ContextDir
		plan.DockerfileName = "Dockerfile"
		if _, err := j.stat(filepath.Join(hostCtx, "Dockerfile")); err != nil {
			return fail(fmt.Errorf("no Dockerfile in context %s, set file", ctxIn))
		}
	case plan.RemoteContext != "":
		plan.DockerfileName = in.File
	default:
		hostFile, err := j.hostPattern(in.File)
		if err != nil {
			return fail(err)
		}
		if info, err := j.stat(hostFile); err != nil || info.IsDir() {
			return fail(fmt.Errorf("dockerfile %s not found", in.File))
		}
		dir, err := inView(filepath.Dir(hostFile), "dockerfile")
		if err != nil {
			return fail(err)
		}
		plan.DockerfileDir, plan.DockerfileName = dir, filepath.Base(hostFile)
	}

	meta := filepath.Join(j.tempDir, "buildkit-metadata.json")
	_ = j.remove(meta)
	scratch = append(scratch, meta)
	plan.MetadataFile, _ = containerPath(j.dir, meta)
	tar := filepath.Join(j.tempDir, "buildkit-image.tar")
	scratch = append(scratch, tar)
	plan.LoadTar, _ = containerPath(j.dir, tar)

	if len(in.Secrets) > 0 {
		dir := filepath.Join(j.tempDir, "_build_secrets")
		_ = j.removeAll(dir)
		if err := j.mkdirAll(dir); err != nil {
			return fail(err)
		}
		scratch = append(scratch, dir)
		for _, kv := range in.Secrets {
			id, value, found := strings.Cut(kv, "=")
			id = strings.TrimSpace(id)
			if !found || id == "" {
				return fail(fmt.Errorf("secret %q is not id=value", kv))
			}
			j.log.AddMask(value)
			p := filepath.Join(dir, sanitizeArtifactName(id))
			if err := j.writeFile(p, []byte(value), 0o644); err != nil {
				return fail(err)
			}
			plan.SecretFiles[id], _ = containerPath(j.dir, p)
		}
	}
	for _, kv := range in.SecretFiles {
		id, src, found := strings.Cut(kv, "=")
		id = strings.TrimSpace(id)
		if !found || id == "" {
			return fail(fmt.Errorf("secret-file %q is not id=path", kv))
		}
		hostSrc, err := j.hostPattern(strings.TrimSpace(src))
		if err != nil {
			return fail(err)
		}
		c, err := inView(hostSrc, "secret file")
		if err != nil {
			return fail(err)
		}
		plan.SecretFiles[id] = c
	}
	for _, kv := range in.BuildContexts {
		name, src, found := strings.Cut(kv, "=")
		name = strings.TrimSpace(name)
		src = strings.TrimSpace(src)
		if !found || name == "" || src == "" {
			return fail(fmt.Errorf("build-context %q is not name=source", kv))
		}
		if strings.Contains(src, "://") || isRemoteContext(src) {
			plan.RemoteContexts[name] = src
			continue
		}
		hostSrc, err := j.hostPattern(src)
		if err != nil {
			return fail(err)
		}
		c, err := inView(hostSrc, "build context")
		if err != nil {
			return fail(err)
		}
		plan.LocalContexts[name] = c
	}
	if strings.HasPrefix(strings.ToLower(j.task.ServerUrl), "http://") {
		plan.InsecureHost = registryHost(j.task.ServerUrl)
	}
	return plan, cleanup, nil
}

// Builds with rootless buildkit, pushing with the job's docker credentials
func (j *job) buildPush(ctx context.Context, with map[string]string, outputs map[string]string, out io.Writer) error {
	in := parseBuildInput(with)
	plan, cleanup, err := j.prepareBuild(in)
	if err != nil {
		return err
	}
	defer cleanup()
	args, notes, err := buildctlArgs(in, plan)
	if err != nil {
		return err
	}
	for _, n := range notes {
		fmt.Fprintf(out, "##[warning]%s\n", n)
	}
	cli, err := j.dockerClient()
	if err != nil {
		return err
	}
	if err := j.runBuildKit(ctx, cli, args, out); err != nil {
		return err
	}
	raw, err := j.readFile(filepath.Join(j.tempDir, "buildkit-metadata.json"))
	if err == nil {
		var meta map[string]any
		_ = json.Unmarshal(raw, &meta)
		digest := stringify(meta["containerimage.digest"])
		imageID := stringify(meta["containerimage.config.digest"])
		if imageID == "" {
			imageID = digest
		}
		outputs["digest"] = digest
		outputs["imageid"] = imageID
		outputs["metadata"] = string(raw)
		if in.Push {
			for _, t := range in.Tags {
				fmt.Fprintf(out, "Pushed %s@%s\n", t, digest)
			}
		}
	}
	if in.Load && len(in.Outputs) == 0 {
		if err := j.loadImage(ctx, cli, filepath.Join(j.tempDir, "buildkit-image.tar"), out); err != nil {
			return fmt.Errorf("load into the runner daemon: %w", err)
		}
	}
	return nil
}

// Builds a job dir Dockerfile into the runner daemon
func (j *job) buildLocalImage(ctx context.Context, cli *client.Client, dir, dockerfile, tag string, out io.Writer) error {
	ctxDir, ok := containerPath(j.dir, dir)
	if !ok {
		return fmt.Errorf("build directory %s must live under the job directory", dir)
	}
	tar := filepath.Join(j.tempDir, "buildkit-image.tar")
	_ = j.remove(tar)
	defer func() { _ = j.remove(tar) }()
	tarView, _ := containerPath(j.dir, tar)
	args := []string{
		"build", "--frontend", "dockerfile.v0", "--progress", "plain",
		"--local", "context=" + ctxDir, "--local", "dockerfile=" + ctxDir, "--opt", "filename=" + dockerfile,
		"--output", "type=docker,dest=" + tarView + ",name=" + tag,
	}
	if err := j.runBuildKit(ctx, cli, args, out); err != nil {
		return err
	}
	return j.loadImage(ctx, cli, tar, out)
}

// Runs one buildctl invocation in a throwaway rootless buildkit container
func (j *job) runBuildKit(ctx context.Context, cli *client.Client, args []string, out io.Writer) error {
	image := strings.TrimSpace(j.r.cfg.BuildKitImage)
	if image == "" {
		image = defaultBuildKitImage
	}
	if err := j.r.pullOnce(ctx, cli, image, out); err != nil {
		return err
	}
	if err := j.writeDockerConfig(); err != nil {
		return err
	}
	volume, release, err := j.r.acquireBuildKitVolume(ctx)
	if err != nil {
		return err
	}
	defer release()

	host := registryHost(j.task.ServerUrl)
	caFile := strings.TrimSpace(j.r.cfg.RegistryCA)
	if caFile != "" {
		if _, err := os.Stat(caFile); err != nil {
			fmt.Fprintf(out, "##[warning]registry ca %s is not readable, continuing without it\n", caFile)
			caFile = ""
		}
	}
	binds := []string{
		j.dir + ":" + containerWorkRoot,
		j.dockerConfigDir() + ":" + buildKitDockerDir + ":ro",
		volume + ":" + buildKitStateDir,
	}
	network := j.network
	dns := j.r.buildDNS(ctx, cli, image, out)
	if dns == nil && network != "" {
		// The default bridge still hands out usable resolvers
		fmt.Fprintln(out, "##[warning]Could not discover the daemon's resolvers, building on the default docker network where job services are unreachable")
		network = ""
	}
	plainHTTP := strings.HasPrefix(strings.ToLower(j.task.ServerUrl), "http://")
	// Daemonless buildkitd runs outside a user namespace and reads /etc, name the file
	daemonFlags := "--oci-worker-no-process-sandbox"
	if toml := buildkitdConfig(host, plainHTTP, caFile != "", dns); toml != "" {
		p := filepath.Join(j.tempDir, "buildkitd.toml")
		if err := j.writeFile(p, []byte(toml), 0o644); err != nil {
			return err
		}
		binds = append(binds, p+":"+buildKitConfigDir+"/buildkitd.toml:ro")
		daemonFlags += " --config " + buildKitConfigDir + "/buildkitd.toml"
	}
	if caFile != "" {
		binds = append(binds, caFile+":"+buildKitCAPath+":ro")
	}
	var devices []container.DeviceMapping
	if _, err := os.Stat("/dev/fuse"); err == nil {
		devices = append(devices, container.DeviceMapping{PathOnHost: "/dev/fuse", PathInContainer: "/dev/fuse", CgroupPermissions: "rwm"})
	}
	fmt.Fprintf(out, "Building with %s\n", image)
	fmt.Fprintf(out, "buildctl %s\n", strings.Join(args, " "))
	tail := newTailWriter(out, buildKitTailLines)
	code, err := runContainer(ctx, cli, containerRun{
		Image: image, Entrypoint: []string{"buildctl-daemonless.sh"}, Cmd: args,
		Env:   []string{"BUILDKITD_FLAGS=" + daemonFlags, "HOME=/home/user"},
		Binds: binds, Network: network, Hosts: j.hosts, SecurityOpt: []string{"seccomp=unconfined", "apparmor=unconfined"},
		Devices: devices, Labels: j.r.labels(j.task.JobId), Out: tail,
	})
	if err != nil {
		return fmt.Errorf("buildkit: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("buildkit exited with code %d\n%s", code, tail.String())
	}
	return nil
}

// Imports a job dir docker tar into the runner daemon
func (j *job) loadImage(ctx context.Context, cli *client.Client, tarPath string, out io.Writer) error {
	f, err := j.open(tarPath)
	if err != nil {
		return err
	}
	defer f.Close()
	resp, err := cli.ImageLoad(ctx, f, client.ImageLoadWithQuiet(false))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if !resp.JSON {
		_, err = io.Copy(out, resp.Body)
		return err
	}
	return drainJSONStream(resp.Body, out)
}

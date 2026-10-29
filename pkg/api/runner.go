package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/runner"
	"github.com/spf13/cobra"
)

func newRunnerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runner",
		Short: "Run Actions jobs for this instance",
		Long: `Register this machine as an Actions runner and execute jobs.

Registration tokens come from the admin, org, or repo runner settings and
carry their scope. Jobs run in docker containers chosen by the runs-on label
mapping in the runner config, or directly on this host for labels mapped to
"host" when the host executor is enabled.

Jobs execute through act, the engine behind the Gitea and Forgejo runners,
so every action from the GitHub ecosystem runs as published. ubuntu-latest
maps to ` + runner.DefaultImage + `, the image act, Gitea and Forgejo default
to, with node, git, the docker cli, curl and build tools. Point a label at
` + runner.SlimImage + ` in the images map of runner.json for a slim image.
Jobs that build images need docker, register with --privileged-jobs to hand
them the runner's docker socket.

Every option lives in runner.json and can be set at registration:

  container_options   extra docker run flags for every job container, shell
                      quoted, for example --add-host host.docker.internal:host-gateway
  valid_volumes       host paths and named volumes workflows may mount through
                      container options, glob syntax, everything else is refused
  container_network   docker network job and service containers join, empty
                      creates a private network per job
  docker_host         daemon address such as unix:///run/docker.sock or
                      ssh://user@host, exported as DOCKER_HOST so act uses it too
  insecure_skip_tls   skip certificate checks when cloning actions and workflows
                      from the instance

The runner pulls the job image and every service image before a job starts,
using the job token for images on this instance's registry and the docker
login of the runner process for everything else.`,
	}
	cmd.AddCommand(newRunnerRegisterCmd(), newRunnerStartCmd(), newRunnerStatusCmd())
	return cmd
}

func runnerVersion() string {
	if cliVersion != "" && cliVersion != "dev" {
		return cliVersion
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

// Long jobs must not trip the cli request timeout
func runnerHTTPClient() *http.Client {
	return &http.Client{Transport: client.HTTPClient.Transport}
}

// Scope as the server names it, system, org:<name>, or repo:<namespace/name>
func runnerScopeString(sc *v1.ActionsScope) string {
	switch sc.GetKind() {
	case "org", "repo":
		return sc.GetKind() + ":" + sc.GetId()
	}
	return "system"
}

// Distinct from the global --config, which names the cli config
func addRunnerConfigFlag(cmd *cobra.Command, dest *string) {
	cmd.Flags().StringVar(dest, "runner-config", "", "Runner config path (default DFCLI_RUNNER_CONFIG or ~/.dfcli/runner.json)")
}

func newRunnerRegisterCmd() *cobra.Command {
	var scope, token, name, labels, config, workDir, containerOptions, validVolumes, dockerHost, containerNetwork string
	var concurrency int
	var host, privileged, insecureSkipTLS bool

	cmd := &cobra.Command{
		Use:   "register",
		Short: "Exchange a registration token for a runner identity",
		Example: `  dfcli runner register --server https://df.example.com --token dfr_... --name build-1 --labels ubuntu-latest,linux
  dfcli runner register --server https://df.example.com --token dfr_... --labels ubuntu-latest,host --host`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(token) == "" {
				return fmt.Errorf("--token is required")
			}
			if name == "" {
				name, _ = os.Hostname()
			}
			var labelList []string
			for _, l := range strings.Split(labels, ",") {
				if l = strings.TrimSpace(l); l != "" {
					labelList = append(labelList, l)
				}
			}
			if len(labelList) == 0 {
				labelList = []string{runner.DefaultLabel}
			}
			fmt.Printf("Registering with %s\n", client.BaseURL)
			ctx, cancel := context.WithTimeout(cmd.Context(), time.Minute)
			defer cancel()
			rc := runner.NewClient(client.BaseURL, "", runnerHTTPClient())
			info, runnerToken, err := rc.Register(ctx, strings.TrimSpace(token), name, labelList, runnerVersion())
			if err != nil {
				return fmt.Errorf("registration with %s failed: %w", client.BaseURL, rpcErr(err))
			}
			images := runner.DefaultImages()
			for _, l := range labelList {
				if _, ok := images[l]; !ok {
					images[l] = runner.DefaultImage
				}
			}
			cfg := &runner.Config{
				Server: client.BaseURL, Token: runnerToken, RunnerID: info.GetId(), Name: info.GetName(), Scope: runnerScopeString(info.GetScope()),
				Labels: info.GetLabels(), Images: images, Concurrency: concurrency, HostEnabled: host, PrivilegedJobs: privileged, WorkDir: workDir,
				DockerHost: strings.TrimSpace(dockerHost), ContainerOptions: strings.TrimSpace(containerOptions), ValidVolumes: splitList(validVolumes),
				ContainerNetwork: strings.TrimSpace(containerNetwork), InsecureSkipTLS: insecureSkipTLS,
			}
			if err := cfg.Save(config); err != nil {
				return fmt.Errorf("save runner config: %w", err)
			}
			path := config
			if path == "" {
				path = runner.DefaultPath()
			}
			fmt.Printf("Registered runner %s (%s) for %s with labels %s\n", info.GetName(), info.GetId(), cfg.Scope, strings.Join(info.GetLabels(), ","))
			fmt.Printf("Config written to %s, edit the images map to change label to image mappings\n", path)
			fmt.Printf("ubuntu-latest runs %s, set it to %s for a slim image\n", runner.DefaultImage, runner.SlimImage)
			fmt.Println("Start it with: dfcli runner start")
			return nil
		},
	}

	cmd.Flags().StringVar(&token, "token", "", "One time registration token, it carries the scope")
	cmd.Flags().StringVar(&scope, "scope", "", "Ignored")
	_ = cmd.Flags().MarkDeprecated("scope", "the scope comes from the token")
	cmd.Flags().StringVar(&name, "name", "", "Runner name (default hostname)")
	cmd.Flags().StringVar(&labels, "labels", runner.DefaultLabel, "Comma separated labels this runner serves")
	addRunnerConfigFlag(cmd, &config)
	cmd.Flags().IntVar(&concurrency, "concurrency", 1, "Jobs to run at once")
	cmd.Flags().BoolVar(&host, "host", false, "Enable the host executor for labels mapped to host")
	cmd.Flags().StringVar(&workDir, "work-dir", "", "Parent of job work trees")
	cmd.Flags().BoolVar(&privileged, "privileged-jobs", false, "Hand jobs the docker socket and let them run --privileged")
	cmd.Flags().StringVar(&dockerHost, "docker-host", "", "Docker daemon address, unix://, tcp:// or ssh:// (default the environment)")
	cmd.Flags().StringVar(&containerOptions, "container-options", "", "Extra docker run flags for every job container, shell quoted")
	cmd.Flags().StringVar(&validVolumes, "valid-volumes", "", "Comma separated host paths and named volumes jobs may mount, glob syntax")
	cmd.Flags().StringVar(&containerNetwork, "container-network", "", "Docker network job containers join (default a private network per job)")
	cmd.Flags().BoolVar(&insecureSkipTLS, "insecure-skip-tls", false, "Skip certificate checks when cloning actions and workflows")
	_ = cmd.MarkFlagRequired("token")
	return cmd
}

// Comma separated flag values, blanks dropped
func splitList(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// Act reads the environment, the standalone runner owns it
func exportDockerHost(cfg *runner.Config) error {
	if cfg.DockerHost == "" {
		return nil
	}
	return os.Setenv("DOCKER_HOST", cfg.DockerHost)
}

func newRunnerStartCmd() *cobra.Command {
	var config string

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Poll for jobs and run them until interrupted",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := runner.Load(config)
			if err != nil {
				return err
			}
			if err := exportDockerHost(cfg); err != nil {
				return err
			}
			r, err := runner.New(cfg, runnerVersion(), runnerHTTPClient())
			if err != nil {
				return err
			}
			return rpcErr(r.Run(cmd.Context()))
		},
	}

	addRunnerConfigFlag(cmd, &config)
	return cmd
}

func newRunnerStatusCmd() *cobra.Command {
	var config string
	var check bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the runner config and check docker and the server",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := runner.Load(config)
			if err != nil {
				return err
			}
			fmt.Printf("Runner:      %s (%s)\n", cfg.Name, cfg.RunnerID)
			fmt.Printf("Server:      %s\n", cfg.Server)
			if cfg.Scope != "" {
				fmt.Printf("Scope:       %s\n", cfg.Scope)
			}
			fmt.Printf("Labels:      %s\n", strings.Join(cfg.Labels, ","))
			fmt.Printf("Concurrency: %d\n", cfg.Concurrency)
			fmt.Printf("Host runs:   %v\n", cfg.HostEnabled)
			fmt.Printf("Privileged:  %v\n", cfg.PrivilegedJobs)
			if cfg.ContainerOptions != "" {
				fmt.Printf("Options:     %s\n", cfg.ContainerOptions)
			}
			if len(cfg.ValidVolumes) > 0 {
				fmt.Printf("Volumes:     %s\n", strings.Join(cfg.ValidVolumes, ","))
			}
			if cfg.ContainerNetwork != "" {
				fmt.Printf("Network:     %s\n", cfg.ContainerNetwork)
			}
			if cfg.DockerHost != "" {
				fmt.Printf("Docker host: %s\n", cfg.DockerHost)
			}
			if cfg.InsecureSkipTLS {
				fmt.Printf("TLS checks:  skipped for clones\n")
			}
			fmt.Printf("Images:\n")
			keys := make([]string, 0, len(cfg.Images))
			for k := range cfg.Images {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Printf("  %-20s %s\n", k, cfg.Images[k])
			}
			r, err := runner.New(cfg, runnerVersion(), runnerHTTPClient())
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			if err := r.DockerAvailable(ctx); err != nil {
				fmt.Printf("Docker:      unavailable (%v)\n", err)
			} else {
				fmt.Printf("Docker:      ok\n")
			}
			if check {
				if err := r.Heartbeat(ctx); err != nil {
					return fmt.Errorf("server check against %s failed: %w", cfg.Server, rpcErr(err))
				}
				fmt.Printf("Server:      ok, runner token accepted\n")
			}
			return nil
		},
	}

	addRunnerConfigFlag(cmd, &config)
	cmd.Flags().BoolVar(&check, "check", false, "Send a heartbeat to verify the runner token (marks the runner online)")
	return cmd
}

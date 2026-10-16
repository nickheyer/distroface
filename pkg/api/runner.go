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

	"github.com/nickheyer/distroface/pkg/runner"
	"github.com/spf13/cobra"
)

func newRunnerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runner",
		Short: "Run Actions jobs for this instance",
		Long: `Register this machine as an Actions runner and execute jobs.

Registration tokens come from the admin, org, or repo runner settings.
Jobs run in docker containers chosen by the runs-on label mapping in
the runner config, or directly on this host for labels mapped to "host"
when the host executor is enabled.`,
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

func parseRunnerScope(s string) (string, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "system":
		return s, nil
	case strings.HasPrefix(s, "org:") && len(s) > 4:
		return s, nil
	case strings.HasPrefix(s, "repo:") && strings.Contains(s[5:], "/"):
		return s, nil
	}
	return "", fmt.Errorf("scope must be system, org:<name>, or repo:<namespace/name>")
}

func newRunnerRegisterCmd() *cobra.Command {
	var scope, token, name, labels, config string
	var concurrency int
	var host bool

	cmd := &cobra.Command{
		Use:   "register",
		Short: "Exchange a registration token for a runner identity",
		Example: `  dfcli runner register --scope system --token dfr_... --name build-1 --labels ubuntu-latest,linux
  dfcli runner register --scope repo:acme/widgets --token dfr_... --labels ubuntu-latest,host --host`,
		RunE: func(cmd *cobra.Command, args []string) error {
			scope, err := parseRunnerScope(scope)
			if err != nil {
				return err
			}
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
			ctx, cancel := context.WithTimeout(cmd.Context(), time.Minute)
			defer cancel()
			rc := runner.NewClient(client.BaseURL, "", runnerHTTPClient())
			info, runnerToken, err := rc.Register(ctx, strings.TrimSpace(token), name, labelList, runnerVersion())
			if err != nil {
				return fmt.Errorf("registration failed: %w", rpcErr(err))
			}
			images := runner.DefaultImages()
			for _, l := range labelList {
				if _, ok := images[l]; !ok {
					images[l] = runner.DefaultImage
				}
			}
			cfg := &runner.Config{
				Server: client.BaseURL, Token: runnerToken, RunnerID: info.GetId(), Name: info.GetName(), Scope: scope,
				Labels: info.GetLabels(), Images: images, Concurrency: concurrency, HostEnabled: host,
			}
			if err := cfg.Save(config); err != nil {
				return fmt.Errorf("save runner config: %w", err)
			}
			path := config
			if path == "" {
				path = runner.DefaultPath()
			}
			fmt.Printf("Registered runner %s (%s) with labels %s\n", info.GetName(), info.GetId(), strings.Join(info.GetLabels(), ","))
			fmt.Printf("Config written to %s, edit the images map to change label to image mappings\n", path)
			fmt.Println("Start it with: dfcli runner start")
			return nil
		},
	}

	cmd.Flags().StringVar(&scope, "scope", "", "Scope the token was issued for: system, org:<name>, or repo:<namespace/name>")
	cmd.Flags().StringVar(&token, "token", "", "One time registration token")
	cmd.Flags().StringVar(&name, "name", "", "Runner name (default hostname)")
	cmd.Flags().StringVar(&labels, "labels", runner.DefaultLabel, "Comma separated labels this runner serves")
	cmd.Flags().StringVar(&config, "config", "", "Runner config path (default ~/.dfcli/runner.json)")
	cmd.Flags().IntVar(&concurrency, "concurrency", 1, "Jobs to run at once")
	cmd.Flags().BoolVar(&host, "host", false, "Enable the host executor for labels mapped to host")
	_ = cmd.MarkFlagRequired("scope")
	_ = cmd.MarkFlagRequired("token")
	return cmd
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
			r, err := runner.New(cfg, runnerVersion(), runnerHTTPClient())
			if err != nil {
				return err
			}
			return r.Run(cmd.Context())
		},
	}

	cmd.Flags().StringVar(&config, "config", "", "Runner config path (default ~/.dfcli/runner.json)")
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
					return fmt.Errorf("server check failed: %w", rpcErr(err))
				}
				fmt.Printf("Server:      ok, runner token accepted\n")
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&config, "config", "", "Runner config path (default ~/.dfcli/runner.json)")
	cmd.Flags().BoolVar(&check, "check", false, "Send a heartbeat to verify the runner token (marks the runner online)")
	return cmd
}

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/nickheyer/distroface/pkg/config"
	"github.com/nickheyer/distroface/pkg/migrate"
)

//	report    - dry-run analysis of the v1 data: what would migrate, what is broken
//	users     - import v1 users (bcrypt hashes port directly) + map groups -> v2 roles
//	orgs      - create the legacy org + orgs for v1 two-level namespaces
//	images    - push-replay images into v2 via the registry API (webhooks suppressed)
//	artifacts - analyze v1 artifact repos (import blocked until the v2 artifact backend exists)
//	verify    - digest/tag parity report v1 vs v2
//	all       - users + orgs + images + verify

func bindFlags(fs *flag.FlagSet) *config.MigrateConfig {
	cfg := &config.MigrateConfig{}
	fs.StringVar(&cfg.V1DB, "v1-db", "distro.db", "path to v1 distro.db")
	fs.StringVar(&cfg.V1Root, "v1-root", "", "path to v1 storage root directory")
	fs.StringVar(&cfg.V2DB, "v2-db", "", "path to v2 sqlite database")
	fs.StringVar(&cfg.Registry, "v2-url", "", "v2 registry host[:port], e.g. registry.example.com:8080")
	fs.StringVar(&cfg.User, "v2-user", "", "v2 username for registry pushes")
	fs.StringVar(&cfg.Pass, "v2-pass", "", "v2 password or df_ token (defaults to $V2_PASSWORD)")
	fs.BoolVar(&cfg.PlainHTTP, "plain-http", false, "use plain http for the v2 registry")
	fs.StringVar(&cfg.LegacyNS, "legacy-ns", "legacy", "org namespace for flat v1 image names")
	fs.BoolVar(&cfg.DryRun, "dry-run", false, "print planned actions without writing anything")
	fs.IntVar(&cfg.Jobs, "jobs", 1, "concurrent repository pushes")
	fs.BoolVar(&cfg.Verbose, "v", false, "verbose logging")
	return cfg
}

func usage() {
	fmt.Fprintf(os.Stderr, `usage: migrate <command> [flags]

commands:
  report     dry-run analysis of v1 data (db + storage cross-check, mapping plan)
  users      import v1 users into v2 (bcrypt hashes preserved, groups -> roles)
  orgs       create legacy org + two-level namespace orgs in v2
  images     push-replay v1 images into the v2 registry
  artifacts  analyze v1 artifact repos (live vs orphaned)
  verify     tag/digest parity report v1 vs v2
  all        users + orgs + images + verify

run 'migrate <command> -h' for flags
`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	cfg := bindFlags(fs)
	if err := fs.Parse(os.Args[2:]); err != nil {
		os.Exit(2)
	}
	if cfg.Pass == "" {
		cfg.Pass = os.Getenv("V2_PASSWORD")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch cmd {
	case "report":
		err = migrate.CmdReport(ctx, cfg)
	case "users":
		err = migrate.CmdUsers(ctx, cfg)
	case "orgs":
		err = migrate.CmdOrgs(ctx, cfg)
	case "images":
		err = migrate.CmdImages(ctx, cfg)
	case "artifacts":
		err = migrate.CmdArtifacts(ctx, cfg)
	case "verify":
		err = migrate.CmdVerify(ctx, cfg)
	case "all":
		err = cmdAll(ctx, cfg)
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate %s: %v\n", cmd, err)
		os.Exit(1)
	}
}

func cmdAll(ctx context.Context, cfg *config.MigrateConfig) error {
	if err := migrate.CmdUsers(ctx, cfg); err != nil {
		return fmt.Errorf("users: %w", err)
	}
	if err := migrate.CmdOrgs(ctx, cfg); err != nil {
		return fmt.Errorf("orgs: %w", err)
	}
	if err := migrate.CmdImages(ctx, cfg); err != nil {
		return fmt.Errorf("images: %w", err)
	}
	return migrate.CmdVerify(ctx, cfg)
}

package config

import (
	"testing"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/spf13/viper"
	"google.golang.org/protobuf/proto"
)

func TestHostnameOverride(t *testing.T) {
	v := viper.New()
	v.Set("server.hostname", "legacy.example.com")
	cfg := &Config{}
	applyHostnameOverride(v, cfg)
	if got := cfg.Overrides.GetServer().GetPublicHostname(); got != "legacy.example.com" {
		t.Fatalf("legacy key not folded, got %q", got)
	}

	// Canonical key beats the legacy key
	v.Set("server.public_hostname", "reg.example.com")
	cfg = &Config{}
	applyHostnameOverride(v, cfg)
	if got := cfg.Overrides.GetServer().GetPublicHostname(); got != "reg.example.com" {
		t.Fatalf("canonical key not folded, got %q", got)
	}

	// An explicit overrides block is never clobbered
	cfg = &Config{Overrides: &v1.Settings{
		Server: &v1.ServerSettings{PublicHostname: proto.String("pinned.example.com")},
	}}
	applyHostnameOverride(v, cfg)
	if got := cfg.Overrides.GetServer().GetPublicHostname(); got != "pinned.example.com" {
		t.Fatalf("explicit override clobbered, got %q", got)
	}
}

func TestHostnameEnvAliases(t *testing.T) {
	for _, env := range []string{
		"DISTROFACE_SERVER_PUBLIC_HOSTNAME",
		"DISTROFACE_PUBLIC_HOSTNAME",
		"DISTROFACE_SERVER_HOSTNAME",
		"DISTROFACE_HOSTNAME",
	} {
		t.Run(env, func(t *testing.T) {
			t.Setenv(env, "env.example.com")
			cfg, err := Load(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.Overrides.GetServer().GetPublicHostname(); got != "env.example.com" {
				t.Fatalf("env %s not applied, got %q", env, got)
			}
		})
	}
}

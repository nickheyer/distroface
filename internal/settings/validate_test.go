package settings

import (
	"strings"
	"testing"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"google.golang.org/protobuf/proto"
)

func tlsCase(mut func(s *v1.Settings)) *v1.Settings {
	s := Defaults()
	s.Server.PublicHostname = proto.String("registry.example.com")
	mut(s)
	return s
}

func TestTLSRules(t *testing.T) {
	sys := v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM
	org := v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_ORG
	acmeOn := func(s *v1.Settings) {
		s.Acme.Enabled = proto.Bool(true)
		s.Acme.Email = proto.String("ops@example.com")
	}
	cases := []struct {
		name  string
		scope v1.SettingsScopeType
		paths []string
		eff   *v1.Settings
		want  string
	}{
		{"enable needs email", sys, []string{"acme.enabled"},
			tlsCase(func(s *v1.Settings) { s.Acme.Enabled = proto.Bool(true) }), "account email"},
		{"enable with email passes", sys, []string{"acme.enabled"}, tlsCase(acmeOn), ""},
		{"clearing email while on", sys, []string{"acme.email"},
			tlsCase(func(s *v1.Settings) { s.Acme.Enabled = proto.Bool(true) }), "turn it off"},
		{"unrelated save ignores seeded inconsistency", sys, []string{"tls.mode"},
			tlsCase(func(s *v1.Settings) { s.Acme.Enabled = proto.Bool(true) }), ""},
		{"acme source needs acme on", sys, []string{"tls.primary_source"},
			tlsCase(func(s *v1.Settings) { s.Tls.PrimarySource = v1.CertSource_CERT_SOURCE_ACME.Enum() }),
			"turn on automatic issuance"},
		{"acme off while primary is acme", sys, []string{"acme.enabled"},
			tlsCase(func(s *v1.Settings) { s.Tls.PrimarySource = v1.CertSource_CERT_SOURCE_ACME.Enum() }),
			"choose another source"},
		{"acme source needs issuable host", sys, []string{"tls.primary_source"},
			tlsCase(func(s *v1.Settings) {
				acmeOn(s)
				s.Tls.PrimarySource = v1.CertSource_CERT_SOURCE_ACME.Enum()
				s.Server.PublicHostname = proto.String("localhost:8080")
			}), "cannot receive a public certificate"},
		{"hostname change under acme source", sys, []string{"server.public_hostname"},
			tlsCase(func(s *v1.Settings) {
				acmeOn(s)
				s.Tls.PrimarySource = v1.CertSource_CERT_SOURCE_ACME.Enum()
				s.Server.PublicHostname = proto.String("10.0.0.5")
			}), "cannot receive a public certificate"},
		{"acme source with issuable host passes", sys, []string{"tls.primary_source"},
			tlsCase(func(s *v1.Settings) {
				acmeOn(s)
				s.Tls.PrimarySource = v1.CertSource_CERT_SOURCE_ACME.Enum()
			}), ""},
		{"self directory at org scope", org, []string{"acme.directory_url"},
			tlsCase(func(s *v1.Settings) {
				s.Acme.DirectoryUrl = proto.String("https://registry.example.com/acme/directory")
			}), "points at this instance"},
		{"relative directory", sys, []string{"acme.directory_url"},
			tlsCase(func(s *v1.Settings) { s.Acme.DirectoryUrl = proto.String("acme/directory") }), "absolute"},
		{"external directory passes", org, []string{"acme.directory_url"},
			tlsCase(func(s *v1.Settings) {
				s.Acme.DirectoryUrl = proto.String("https://acme-staging-v02.api.letsencrypt.org/directory")
			}), ""},
		{"challenge port is the bind port", sys, []string{"acme.challenge_port"},
			tlsCase(func(s *v1.Settings) {
				acmeOn(s)
				s.Acme.ChallengePort = proto.String("8080")
			}), "main listener port"},
		{"enable with challenge port on bind port", sys, []string{"acme.enabled"},
			tlsCase(func(s *v1.Settings) {
				acmeOn(s)
				s.Acme.ChallengePort = proto.String("8080")
			}), "main listener port"},
		{"challenge port is the ssh port", sys, []string{"acme.challenge_port"},
			tlsCase(func(s *v1.Settings) {
				acmeOn(s)
				s.Git.SshEnabled = proto.Bool(true)
				s.Acme.ChallengePort = proto.String("22")
			}), "git ssh port"},
		{"empty challenge port passes", sys, []string{"acme.challenge_port"},
			tlsCase(func(s *v1.Settings) {
				acmeOn(s)
				s.Acme.ChallengePort = proto.String("")
			}), ""},
		{"bad email", sys, []string{"acme.email"},
			tlsCase(func(s *v1.Settings) { s.Acme.Email = proto.String("nope") }), "not an email"},
	}
	check := TLSRules("8080")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := check(t.Context(), tc.scope, "", tc.paths, tc.eff)
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.want != "" && err == nil:
				t.Fatalf("expected error containing %q", tc.want)
			case tc.want != "" && !strings.Contains(err.Error(), tc.want):
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestValidatorBlocksSave(t *testing.T) {
	store := newMemStore()
	r := NewResolver(store, nil)
	r.AddValidator(TLSRules("8080"))
	ctx := t.Context()
	sys := v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM

	enable := &v1.Settings{Acme: &v1.ACMESettings{Enabled: proto.Bool(true)}}
	if _, err := r.Update(ctx, sys, "", enable, []string{"acme.enabled"}); err == nil {
		t.Fatal("enabling acme without an email must fail")
	}
	if r.System(ctx).GetAcme().GetEnabled() {
		t.Fatal("rejected update must not persist")
	}
	if _, found, _ := store.GetSettingsValue(ctx, sys, ""); found {
		t.Fatal("rejected update must not write a row")
	}

	email := &v1.Settings{Acme: &v1.ACMESettings{Email: proto.String("ops@example.com")}}
	if _, err := r.Update(ctx, sys, "", email, []string{"acme.email"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Update(ctx, sys, "", enable, []string{"acme.enabled"}); err != nil {
		t.Fatalf("enable after email: %v", err)
	}
	if !r.System(ctx).GetAcme().GetEnabled() {
		t.Fatal("accepted update must persist")
	}

	// Lower tiers are judged on the merged result
	host := &v1.Settings{Server: &v1.ServerSettings{PublicHostname: proto.String("registry.example.com")}}
	if _, err := r.Update(ctx, sys, "", host, []string{"server.public_hostname"}); err != nil {
		t.Fatal(err)
	}
	self := &v1.Settings{Acme: &v1.ACMESettings{DirectoryUrl: proto.String("https://registry.example.com/acme/directory")}}
	_, err := r.Update(ctx, v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_ORG, "o1", self, []string{"acme.directory_url"})
	if err == nil || !strings.Contains(err.Error(), "points at this instance") {
		t.Fatalf("expected self directory rejection, got %v", err)
	}
}

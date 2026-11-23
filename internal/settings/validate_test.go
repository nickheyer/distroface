package settings

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"strings"
	"testing"

	"github.com/go-jose/go-jose/v4"
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
		{"renewal interval floor", sys, []string{"acme.renew_interval_minutes"},
			tlsCase(func(s *v1.Settings) { s.Acme.RenewIntervalMinutes = proto.Int32(0) }), "at least one minute"},
		{"bad email", sys, []string{"acme.email"},
			tlsCase(func(s *v1.Settings) { s.Acme.Email = proto.String("nope") }), "not an email"},
		{"dns-01 needs a provider", sys, []string{"acme.challenge"},
			tlsCase(func(s *v1.Settings) { s.Acme.Challenge = v1.ACMEChallengeType_ACME_CHALLENGE_TYPE_DNS_01.Enum() }), "pick a dns provider"},
		{"rfc2136 needs a nameserver", org, []string{"acme.dns.provider"},
			tlsCase(func(s *v1.Settings) {
				s.Acme.Challenge = v1.ACMEChallengeType_ACME_CHALLENGE_TYPE_DNS_01.Enum()
				s.Acme.Dns.Provider = v1.DNSProvider_DNS_PROVIDER_RFC2136.Enum()
			}), "nameserver"},
		{"rfc2136 needs a tsig key", sys, []string{"acme.dns.server"},
			tlsCase(func(s *v1.Settings) {
				s.Acme.Challenge = v1.ACMEChallengeType_ACME_CHALLENGE_TYPE_DNS_01.Enum()
				s.Acme.Dns.Provider = v1.DNSProvider_DNS_PROVIDER_RFC2136.Enum()
				s.Acme.Dns.Server = proto.String("ns1.corp.example")
			}), "tsig key name and secret"},
		{"rfc2136 complete passes", sys, []string{"acme.dns"},
			tlsCase(func(s *v1.Settings) {
				s.Acme.Challenge = v1.ACMEChallengeType_ACME_CHALLENGE_TYPE_DNS_01.Enum()
				s.Acme.Dns.Provider = v1.DNSProvider_DNS_PROVIDER_RFC2136.Enum()
				s.Acme.Dns.Server = proto.String("ns1.corp.example")
				s.Acme.Dns.TsigKeyName = proto.String("distroface")
				s.Acme.Dns.TsigSecret = proto.String("c2VjcmV0")
			}), ""},
		{"cloudflare needs a token", sys, []string{"acme.dns.provider"},
			tlsCase(func(s *v1.Settings) {
				s.Acme.Challenge = v1.ACMEChallengeType_ACME_CHALLENGE_TYPE_DNS_01.Enum()
				s.Acme.Dns.Provider = v1.DNSProvider_DNS_PROVIDER_CLOUDFLARE.Enum()
			}), "api token"},
		{"dns block ignored without the dns-01 challenge", sys, []string{"acme.dns.provider"},
			tlsCase(func(s *v1.Settings) {
				s.Acme.Dns.Provider = v1.DNSProvider_DNS_PROVIDER_CLOUDFLARE.Enum()
			}), ""},
		{"account key must parse", org, []string{"acme.account_key"},
			tlsCase(func(s *v1.Settings) { s.Acme.AccountKey = proto.String("MIIJJwIBAAKCAgEA") }), "pem, base64 der or jwk"},
		{"http-01 needs the challenge port", org, []string{"acme.challenge"},
			tlsCase(func(s *v1.Settings) {
				s.Acme.Challenge = v1.ACMEChallengeType_ACME_CHALLENGE_TYPE_HTTP_01.Enum()
				s.Acme.ChallengePort = proto.String("")
			}), "needs the challenge port"},
		{"tsig secret must be base64", sys, []string{"acme.dns"},
			tlsCase(func(s *v1.Settings) {
				s.Acme.Challenge = v1.ACMEChallengeType_ACME_CHALLENGE_TYPE_DNS_01.Enum()
				s.Acme.Dns.Provider = v1.DNSProvider_DNS_PROVIDER_RFC2136.Enum()
				s.Acme.Dns.Server = proto.String("ns1.corp.example")
				s.Acme.Dns.TsigKeyName = proto.String("distroface")
				s.Acme.Dns.TsigSecret = proto.String("not base64!")
			}), "base64 key"},
		{"binding needs both halves", sys, []string{"acme.eab_key_id"},
			tlsCase(func(s *v1.Settings) { s.Acme.EabKeyId = proto.String("kid-1") }), "both the key id and the hmac key"},
		{"ca bundle must hold a certificate", sys, []string{"acme.ca_bundle"},
			tlsCase(func(s *v1.Settings) { s.Acme.CaBundle = proto.String("not pem") }), "no pem certificate"},
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

func TestACMEAccountKeyPEM(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pkcs1 := x509.MarshalPKCS1PrivateKey(key)
	pkcs1PEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: pkcs1})
	std := base64.StdEncoding.EncodeToString(pkcs1)
	for name, in := range map[string]string{
		"pem":             string(pkcs1PEM),
		"base64 der":      std,
		"wrapped base64":  std[:64] + "\n" + std[64:] + "\n",
		"url base64":      base64.RawURLEncoding.EncodeToString(pkcs1),
		"base64 of a pem": base64.StdEncoding.EncodeToString(pkcs1PEM),
	} {
		out, err := ACMEAccountKeyPEM(in)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		block, _ := pem.Decode([]byte(out))
		if block == nil || block.Type != "PRIVATE KEY" {
			t.Fatalf("%s: not canonical pem: %q", name, out)
		}
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil || !key.Equal(parsed) {
			t.Fatalf("%s: key changed in conversion: %v", name, err)
		}
	}
	jwk, err := (&jose.JSONWebKey{Key: key}).MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if out, err := ACMEAccountKeyPEM(string(jwk)); err != nil || !strings.Contains(out, "PRIVATE KEY") {
		t.Fatalf("jwk: %q %v", out, err)
	}
	public, _ := (&jose.JSONWebKey{Key: &key.PublicKey}).MarshalJSON()
	for _, bad := range []string{"", "not a key", string(public), base64.StdEncoding.EncodeToString([]byte("not der"))} {
		if _, err := ACMEAccountKeyPEM(bad); err == nil {
			t.Errorf("%q accepted as an account key", bad)
		}
	}
}

package settings

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Rejects an update by its would be effective result
type Validator func(ctx context.Context, scope v1.SettingsScopeType, scopeID string, paths []string, eff *v1.Settings) error

// Lowercases a host and drops any port
func BareHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

// Dotted dns name, not an ip, not localhost
func IssuableHost(host string) bool {
	host = BareHost(host)
	if host == "" || host == "localhost" || !strings.Contains(host, ".") {
		return false
	}
	return net.ParseIP(host) == nil
}

// Whether a masked path covers any of the fields
func Touched(paths []string, fields ...string) bool {
	for _, p := range paths {
		if pathCovered(p, fields) {
			return true
		}
	}
	return false
}

// Each rule fires only for the fields the update touches
func TLSRules(bindPort string) Validator {
	return func(_ context.Context, scope v1.SettingsScopeType, _ string, paths []string, eff *v1.Settings) error {
		acme := eff.GetAcme()
		host := eff.GetServer().GetPublicHostname()
		if dir := strings.TrimSpace(acme.GetDirectoryUrl()); dir != "" {
			u, err := url.Parse(dir)
			if Touched(paths, "acme.directory_url") && (err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https")) {
				return fmt.Errorf("acme directory url must be an absolute http or https url, leave it empty for lets encrypt production")
			}
			if err == nil && Touched(paths, "acme.directory_url", "server.public_hostname") && strings.EqualFold(u.Hostname(), BareHost(host)) {
				return fmt.Errorf("acme directory url %s points at this instance, which cannot enroll with its own ca, use the instance ca or org ca certificate source instead", dir)
			}
		}
		if email := strings.TrimSpace(acme.GetEmail()); Touched(paths, "acme.email") && email != "" && !strings.Contains(email, "@") {
			return fmt.Errorf("acme account email %q is not an email address", email)
		}
		if scope != v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM {
			return nil
		}

		if acme.GetEnabled() && strings.TrimSpace(acme.GetEmail()) == "" {
			if Touched(paths, "acme.enabled") {
				return fmt.Errorf("set an acme account email under ACME client before turning on automatic issuance")
			}
			if Touched(paths, "acme.email") {
				return fmt.Errorf("automatic issuance is on and needs an account email, turn it off before clearing the address")
			}
		}
		if eff.GetTls().GetPrimarySource() == v1.CertSource_CERT_SOURCE_ACME {
			if !acme.GetEnabled() {
				if Touched(paths, "tls.primary_source") {
					return fmt.Errorf("turn on automatic issuance under ACME client before choosing ACME as the certificate source")
				}
				if Touched(paths, "acme.enabled") {
					return fmt.Errorf("the primary certificate source is ACME, choose another source before turning automatic issuance off")
				}
			}
			if Touched(paths, "tls.primary_source", "server.public_hostname") && !IssuableHost(host) {
				return fmt.Errorf("public hostname %q cannot receive a public certificate, set a dns name that is not localhost or an ip before using ACME", host)
			}
		}
		port := strings.TrimSpace(acme.GetChallengePort())
		if Touched(paths, "acme.challenge_port", "acme.enabled") && acme.GetEnabled() && port != "" {
			if port == strings.TrimSpace(bindPort) {
				return fmt.Errorf("acme challenge port %s is the main listener port, pick another port or leave it empty to validate over tls-alpn-01 on port 443", port)
			}
			if git := eff.GetGit(); git.GetEnabled() && git.GetSshEnabled() && fmt.Sprint(git.GetSshPort()) == port {
				return fmt.Errorf("acme challenge port %s is the git ssh port, pick another port", port)
			}
		}
		return nil
	}
}

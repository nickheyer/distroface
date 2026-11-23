package settings

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/go-jose/go-jose/v4"
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
		if err := acmeAccountRules(paths, acme); err != nil {
			return err
		}
		if err := acmeChallengeRules(paths, acme); err != nil {
			return err
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
		if Touched(paths, "acme.renew_interval_minutes") && acme.GetRenewIntervalMinutes() < 1 {
			return fmt.Errorf("acme renewal interval must be at least one minute")
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

// Canonical pem from a pem, base64 der or jwk key
func ACMEAccountKeyPEM(raw string) (string, error) {
	invalid := fmt.Errorf("acme account key must be a private key as pem, base64 der or jwk")
	der := []byte(strings.TrimSpace(raw))
	if block, _ := pem.Decode(der); block != nil {
		der = block.Bytes
	} else if bytes.HasPrefix(der, []byte("{")) {
		var jwk jose.JSONWebKey
		if err := jwk.UnmarshalJSON(der); err != nil || jwk.IsPublic() {
			return "", invalid
		}
		return pkcs8PEM(jwk.Key, invalid)
	} else {
		compact := strings.NewReplacer("-", "+", "_", "/", "=", "").Replace(strings.Join(strings.Fields(raw), ""))
		decoded, err := base64.RawStdEncoding.DecodeString(compact)
		if err != nil {
			return "", invalid
		}
		// Some exports base64 the whole pem file
		if block, _ := pem.Decode(decoded); block != nil {
			decoded = block.Bytes
		}
		der = decoded
	}
	var key any
	var err error
	if key, err = x509.ParsePKCS8PrivateKey(der); err != nil {
		if key, err = x509.ParsePKCS1PrivateKey(der); err != nil {
			if key, err = x509.ParseECPrivateKey(der); err != nil {
				return "", invalid
			}
		}
	}
	return pkcs8PEM(key, invalid)
}

func pkcs8PEM(key any, invalid error) (string, error) {
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", invalid
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})), nil
}

// Account material must parse and binding halves travel together
func acmeAccountRules(paths []string, acme *v1.ACMESettings) error {
	if key := strings.TrimSpace(acme.GetAccountKey()); Touched(paths, "acme.account_key") && key != "" {
		if _, err := ACMEAccountKeyPEM(key); err != nil {
			return err
		}
	}
	if bundle := strings.TrimSpace(acme.GetCaBundle()); Touched(paths, "acme.ca_bundle") && bundle != "" {
		if !x509.NewCertPool().AppendCertsFromPEM([]byte(bundle)) {
			return fmt.Errorf("acme ca bundle holds no pem certificate")
		}
	}
	if Touched(paths, "acme.eab_key_id", "acme.eab_hmac_key", "acme.directory_url") {
		id, key := strings.TrimSpace(acme.GetEabKeyId()), strings.TrimSpace(acme.GetEabHmacKey())
		if (id == "") != (key == "") {
			return fmt.Errorf("external account binding needs both the key id and the hmac key from the ca")
		}
	}
	return nil
}

// A pinned challenge needs what answers it
func acmeChallengeRules(paths []string, acme *v1.ACMESettings) error {
	switch acme.GetChallenge() {
	case v1.ACMEChallengeType_ACME_CHALLENGE_TYPE_HTTP_01:
		if Touched(paths, "acme.challenge", "acme.challenge_port") && strings.TrimSpace(acme.GetChallengePort()) == "" {
			return fmt.Errorf("the http-01 challenge needs the challenge port, set it or pick another challenge")
		}
	case v1.ACMEChallengeType_ACME_CHALLENGE_TYPE_DNS_01:
		if Touched(paths, "acme.challenge", "acme.dns") {
			return acmeDNSRules(acme.GetDns())
		}
	}
	return nil
}

// Provider credentials dns-01 publishes with
func acmeDNSRules(dns *v1.ACMEDNSSettings) error {
	switch dns.GetProvider() {
	case v1.DNSProvider_DNS_PROVIDER_RFC2136:
		if strings.TrimSpace(dns.GetServer()) == "" {
			return fmt.Errorf("rfc2136 dns needs the nameserver that accepts dynamic updates")
		}
		secret := strings.TrimSpace(dns.GetTsigSecret())
		if strings.TrimSpace(dns.GetTsigKeyName()) == "" || secret == "" {
			return fmt.Errorf("rfc2136 dns needs the tsig key name and secret that sign updates")
		}
		if _, err := base64.StdEncoding.DecodeString(secret); err != nil {
			return fmt.Errorf("rfc2136 tsig secret must be the base64 key the nameserver holds")
		}
	case v1.DNSProvider_DNS_PROVIDER_CLOUDFLARE:
		if strings.TrimSpace(dns.GetApiToken()) == "" {
			return fmt.Errorf("cloudflare dns needs an api token with dns edit on the zone")
		}
	default:
		return fmt.Errorf("pick a dns provider before choosing the dns-01 challenge")
	}
	for _, r := range dns.GetResolvers() {
		if strings.TrimSpace(r) == "" || strings.ContainsAny(r, " /") {
			return fmt.Errorf("dns resolver %q must be a host or host:port", r)
		}
	}
	if dns.GetPropagationTimeoutSeconds() < 0 {
		return fmt.Errorf("dns propagation timeout cannot be negative")
	}
	return nil
}

package mirror

import (
	"errors"
	"fmt"
	"github.com/nickheyer/distroface/internal/auth"
	"google.golang.org/protobuf/proto"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"syscall"
	"time"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// Caller errors that map to InvalidArgument
var ErrInvalid = errors.New("invalid mirror configuration")

// ParseConfig decodes a stored protojson mirror config
func ParseConfig(raw string) (*v1.MirrorConfig, error) {
	cfg := &v1.MirrorConfig{}
	if strings.TrimSpace(raw) == "" {
		return cfg, nil
	}
	if err := protojson.Unmarshal([]byte(raw), cfg); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return cfg, nil
}

// EncodeConfig serializes a mirror config for storage
func EncodeConfig(cfg *v1.MirrorConfig) (string, error) {
	if cfg == nil {
		return "", nil
	}
	b, err := protojson.Marshal(cfg)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Stored tokens carry this prefix once sealed, older rows are plain
const sealedTokenPrefix = "enc:v1:"

// SealToken wraps a credential for storage, empty stays empty
func (m *Monitor) SealToken(plain string) string {
	if plain == "" || m.sealer == nil {
		return plain
	}
	sealed, err := m.sealer.Seal(plain)
	if err != nil {
		m.log.Error("mirror: sealing a token: %v", err)
		return ""
	}
	return sealedTokenPrefix + sealed
}

// Plaintext of a stored token, rows from before sealing pass through
func (m *Monitor) openToken(stored string) string {
	if !strings.HasPrefix(stored, sealedTokenPrefix) {
		return stored
	}
	body := strings.TrimPrefix(stored, sealedTokenPrefix)
	for _, s := range []*auth.Sealer{m.sealer, m.legacySealer} {
		if s == nil {
			continue
		}
		if plain, err := s.Open(body); err == nil {
			return plain
		}
	}
	return ""
}

// Config parses a stored mirror config with its token opened for use
func (m *Monitor) Config(raw string) (*v1.MirrorConfig, error) {
	cfg, err := ParseConfig(raw)
	if err != nil {
		return nil, err
	}
	if cfg.AuthToken != nil {
		cfg.AuthToken = proto.String(m.openToken(cfg.GetAuthToken()))
	}
	return cfg, nil
}

// EncodeConfig stores a freshly supplied config with its token sealed
func (m *Monitor) EncodeConfig(cfg *v1.MirrorConfig) (string, error) {
	if cfg == nil {
		return "", nil
	}
	out := proto.Clone(cfg).(*v1.MirrorConfig)
	if out.GetAuthToken() != "" {
		out.AuthToken = proto.String(m.SealToken(out.GetAuthToken()))
	}
	return EncodeConfig(out)
}

// ApplyUpdate merges an incoming config over the stored one, a kept token never follows a new host
func (m *Monitor) ApplyUpdate(prevRaw string, next *v1.MirrorConfig) (string, error) {
	if next == nil {
		return prevRaw, nil
	}
	merged := &v1.MirrorConfig{
		Upstream:            next.GetUpstream(),
		Username:            next.GetUsername(),
		Pattern:             next.GetPattern(),
		IncludePrereleases:  next.GetIncludePrereleases(),
		SyncDepth:           next.GetSyncDepth(),
		SyncIntervalMinutes: next.GetSyncIntervalMinutes(),
		Paused:              next.GetPaused(),
	}
	switch {
	case next.GetAuthToken() != "":
		merged.AuthToken = proto.String(m.SealToken(next.GetAuthToken()))
	case next.AuthToken != nil:
		merged.AuthToken = next.AuthToken
	default:
		prev, err := ParseConfig(prevRaw)
		if err == nil && prev.GetAuthToken() != "" && upstreamHost(prev.GetUpstream()) == upstreamHost(next.GetUpstream()) {
			merged.AuthToken = prev.AuthToken
		}
	}
	return EncodeConfig(merged)
}

// Host an upstream names, empty for forge shorthand whose host the type implies
func upstreamHost(upstream string) string {
	s := strings.TrimSpace(upstream)
	if strings.Contains(s, "://") {
		if u, err := url.Parse(s); err == nil {
			return strings.ToLower(u.Hostname())
		}
		return s
	}
	// Colon before any slash and no scheme marks scp form
	if i := strings.IndexByte(s, ':'); i > 0 && (strings.IndexByte(s, '/') < 0 || i < strings.IndexByte(s, '/')) {
		host := s[:i]
		if at := strings.LastIndexByte(host, '@'); at >= 0 {
			host = host[at+1:]
		}
		return strings.ToLower(host)
	}
	first, _, _ := strings.Cut(s, "/")
	if strings.Contains(first, ".") {
		return strings.ToLower(first)
	}
	return ""
}

// Redacted returns a copy safe for API responses
func Redacted(raw string) *v1.MirrorConfig {
	cfg, err := ParseConfig(raw)
	if err != nil || (cfg.GetUpstream() == "" && cfg.AuthToken == nil) {
		return nil
	}
	cfg.AuthTokenSet = cfg.GetAuthToken() != ""
	cfg.AuthToken = nil
	return cfg
}

// Rejects empty upstreams, bad globs, and negative knobs
func validateCommon(cfg *v1.MirrorConfig) error {
	if cfg == nil || strings.TrimSpace(cfg.GetUpstream()) == "" {
		return fmt.Errorf("%w: upstream is required", ErrInvalid)
	}
	if p := cfg.GetPattern(); p != "" {
		if _, err := path.Match(p, "probe"); err != nil {
			return fmt.Errorf("%w: pattern %q is not a valid glob", ErrInvalid, p)
		}
	}
	if cfg.GetSyncDepth() < 0 {
		return fmt.Errorf("%w: sync depth must not be negative", ErrInvalid)
	}
	if cfg.GetSyncIntervalMinutes() < 0 {
		return fmt.Errorf("%w: sync interval must not be negative", ErrInvalid)
	}
	return nil
}

func matchesPattern(pattern, name string) bool {
	if pattern == "" {
		return true
	}
	ok, err := path.Match(pattern, name)
	return err == nil && ok
}

// Vets ips at dial time so dns tricks fail, shared by http, ssh, and git
func safeDialer(allowPrivate func() bool) *net.Dialer {
	return &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("mirror: invalid dial address %q: %w", address, err)
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("mirror: dial address %q is not an IP", host)
			}
			return checkMirrorIP(ip, allowPrivate())
		},
	}
}

func safeTransport(allowPrivate func() bool) http.RoundTripper {
	dialer := safeDialer(allowPrivate)
	return &http.Transport{
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// Reject ips in forbidden ranges
func checkMirrorIP(ip net.IP, allowPrivate bool) error {
	switch {
	case ip.IsLoopback():
		return fmt.Errorf("mirror: upstream %s is a loopback address", ip)
	case ip.IsUnspecified():
		return fmt.Errorf("mirror: upstream %s is unspecified", ip)
	case ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast():
		return fmt.Errorf("mirror: upstream %s is link-local (metadata range)", ip)
	case ip.IsMulticast():
		return fmt.Errorf("mirror: upstream %s is multicast", ip)
	case ip.IsPrivate() && !allowPrivate:
		return fmt.Errorf("mirror: upstream %s is in a private range (enable mirror.allow_private_networks to permit)", ip)
	}
	return nil
}

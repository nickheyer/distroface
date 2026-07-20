package mirror

import (
	"errors"
	"fmt"
	"net"
	"net/http"
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

// ApplyUpdate merges an incoming config over the stored one, absent token keeps the old
func ApplyUpdate(prevRaw string, next *v1.MirrorConfig) (string, error) {
	if next == nil {
		return prevRaw, nil
	}
	merged := &v1.MirrorConfig{
		Upstream:            next.GetUpstream(),
		AuthToken:           next.AuthToken,
		Username:            next.GetUsername(),
		Pattern:             next.GetPattern(),
		IncludePrereleases:  next.GetIncludePrereleases(),
		SyncDepth:           next.GetSyncDepth(),
		SyncIntervalMinutes: next.GetSyncIntervalMinutes(),
		Paused:              next.GetPaused(),
	}
	if merged.AuthToken == nil {
		prev, err := ParseConfig(prevRaw)
		if err == nil && prev.GetAuthToken() != "" {
			merged.AuthToken = prev.AuthToken
		}
	}
	return EncodeConfig(merged)
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

// Vet ips at dial time so dns tricks fail
func safeTransport(allowPrivate func() bool) http.RoundTripper {
	dialer := &net.Dialer{
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

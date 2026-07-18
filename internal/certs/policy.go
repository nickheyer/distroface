package certs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/pkg/config"
)

// Decides whether a hostname may be claimed by a portal or issued a certificate
type HostnamePolicy struct {
	cfg   *config.Config
	store *stores.Store
}

func NewHostnamePolicy(cfg *config.Config, store *stores.Store) *HostnamePolicy {
	return &HostnamePolicy{cfg: cfg, store: store}
}

// Exact hostname or *.suffix wildcard
func matchesPattern(host, pattern string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if pattern == "" {
		return false
	}
	if after, ok := strings.CutPrefix(pattern, "*."); ok {
		return strings.HasSuffix(host, "."+after)
	}
	return host == pattern
}

// Blocked hostname patterns from admin settings
func (p *HostnamePolicy) Blacklist(ctx context.Context) []string {
	v, err := p.store.GetSystemSetting(ctx, storage.SettingHostnameBlacklist)
	if err != nil || v == "" {
		return nil
	}
	var patterns []string
	if json.Unmarshal([]byte(v), &patterns) != nil {
		return nil
	}
	return patterns
}

func (p *HostnamePolicy) RequireApproval(ctx context.Context) bool {
	v, err := p.store.GetSystemSetting(ctx, storage.SettingRequireApproval)
	return err == nil && v == "true"
}

// Blacklist check alone, used when registering hostnames
func (p *HostnamePolicy) Blocked(ctx context.Context, host string) error {
	host = bareHost(host)
	for _, pattern := range p.Blacklist(ctx) {
		if matchesPattern(host, pattern) {
			return fmt.Errorf("hostname %q is blocked by the instance blacklist", host)
		}
	}
	return nil
}

func (p *HostnamePolicy) allowed(ctx context.Context, host, orgID string, forIssuance bool) error {
	host = bareHost(host)
	if host == "" {
		return nil
	}
	for _, d := range p.cfg.TLS.ACME.Domains {
		if bareHost(d) == host {
			return nil
		}
	}
	if bareHost(p.cfg.Server.Hostname) == host {
		return nil
	}
	if err := p.Blocked(ctx, host); err != nil {
		return err
	}

	entry, err := p.store.GetCertificateDomainByName(ctx, host)
	if err != nil {
		return err
	}
	if entry != nil && entry.OrgID != nil && orgID != "" && *entry.OrgID != orgID {
		return fmt.Errorf("hostname %q is registered to another organization", host)
	}
	if forIssuance && p.RequireApproval(ctx) {
		if entry == nil {
			return fmt.Errorf("hostname %q is not registered for approval", host)
		}
		if !entry.Approved {
			return fmt.Errorf("hostname %q is awaiting administrator approval", host)
		}
	}
	return nil
}

// Issuance gate, approval must have happened when the toggle is on,
// empty orgID means app context which skips org tier checks
func (p *HostnamePolicy) Allowed(ctx context.Context, host, orgID string) error {
	return p.allowed(ctx, host, orgID, true)
}

// Claiming gate for portal saves, an unapproved hostname may be claimed
// and sit pending, blacklist and cross org exclusivity still apply
func (p *HostnamePolicy) AllowedClaim(ctx context.Context, host, orgID string) error {
	return p.allowed(ctx, host, orgID, false)
}

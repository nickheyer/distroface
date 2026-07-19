package certs

import (
	"context"
	"fmt"
	"strings"

	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/settings"
)

// Decides whether a hostname may be claimed by a portal or issued a certificate
type HostnamePolicy struct {
	store *stores.Store
	res   *settings.Resolver
}

func NewHostnamePolicy(store *stores.Store, res *settings.Resolver) *HostnamePolicy {
	return &HostnamePolicy{store: store, res: res}
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
	return p.res.System(ctx).GetPortals().GetHostnameBlacklist()
}

func (p *HostnamePolicy) RequireApproval(ctx context.Context) bool {
	return p.res.System(ctx).GetPortals().GetRequireHostnameApproval()
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
	if bareHost(p.res.System(ctx).GetServer().GetPublicHostname()) == host {
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

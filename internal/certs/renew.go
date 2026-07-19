package certs

import (
	"context"
	"time"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Certs inside this window get proactively reissued
const renewWindow = 30 * 24 * time.Hour

// Hostnames rarely handshaked still renew on this cadence
const renewInterval = 12 * time.Hour

// ScheduleRenewal proactively reissues acme certs before expiry so
// rarely hit hostnames never lapse waiting on a handshake
func (e *Engine) ScheduleRenewal(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(renewInterval)
		defer ticker.Stop()
		e.renewSweep(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				e.renewSweep(ctx)
			}
		}
	}()
}

// Reissues every acme served hostname nearing expiry
func (e *Engine) renewSweep(ctx context.Context) {
	if !e.ACMEEnabled(ctx) {
		return
	}
	for _, host := range e.renewableHosts(ctx) {
		info, err := e.CertificateInfo(ctx, host)
		if err != nil {
			continue
		}
		if info.GetIssued() && time.Until(info.GetNotAfter().AsTime()) > renewWindow {
			continue
		}
		if _, err := e.EnsureCertificate(ctx, host); err != nil {
			e.log.Warn("renewal for %s deferred: %v", host, err)
			continue
		}
		e.log.Info("renewed acme certificate for %s", host)
	}
}

// Union of acme primary host, acme portals, and approved domains
func (e *Engine) renewableHosts(ctx context.Context) []string {
	seen := map[string]bool{}
	var hosts []string
	add := func(h string) {
		h = bareHost(h)
		if h != "" && !seen[h] {
			seen[h] = true
			hosts = append(hosts, h)
		}
	}

	if e.PrimarySource(ctx) == v1.CertSource_CERT_SOURCE_ACME {
		add(e.primaryHost(ctx))
	}
	if portals, err := e.store.ListRegistryPortals(ctx); err == nil {
		for _, p := range portals {
			if p.Enabled && p.CertSource == v1.CertSource_CERT_SOURCE_ACME {
				add(p.Hostname)
			}
		}
	}
	if domains, err := e.store.ListApprovedCertificateDomains(ctx); err == nil {
		for _, d := range domains {
			add(d.Domain)
		}
	}
	return hosts
}

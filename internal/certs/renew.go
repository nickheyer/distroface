package certs

import (
	"context"
	"time"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Failed hosts retry hourly, under ca validation limits
const renewRetryBackoff = time.Hour

// Wait between sweeps from live settings
func (e *Engine) renewInterval(ctx context.Context) time.Duration {
	minutes := e.res.System(ctx).GetAcme().GetRenewIntervalMinutes()
	if minutes <= 0 {
		return 10 * time.Minute
	}
	return time.Duration(minutes) * time.Minute
}

// ScheduleRenewal proactively reissues acme certs before expiry so
// rarely hit hostnames never lapse waiting on a handshake
func (e *Engine) ScheduleRenewal(ctx context.Context) {
	go func() {
		for {
			e.renewSweep(ctx)
			wait := time.NewTimer(e.renewInterval(ctx))
			select {
			case <-ctx.Done():
				wait.Stop()
				return
			case <-e.renewWake:
				wait.Stop()
			case <-wait.C:
			}
		}
	}()
}

// One instance at a time orders missing or due certs
func (e *Engine) renewSweep(ctx context.Context) {
	if !e.ACMEEnabled(ctx) {
		return
	}
	lock, ok, err := e.sharedCache().TryLock(ctx, "cert-renewal", 10*time.Minute)
	if err != nil {
		e.log.Warn("renewal lease: %v", err)
	} else if !ok {
		return
	}
	stop := lock.KeepAlive(ctx)
	defer func() {
		stop()
		_ = lock.Unlock(ctx)
	}()
	e.importLegacyCerts(ctx)
	for _, host := range e.renewableHosts(ctx) {
		m := e.managerForHost(ctx, host)
		if m == nil || !e.renewalDue(ctx, m, host) {
			continue
		}
		if f, failed := e.lastIssueFailure(host); failed && time.Since(f.at) < renewRetryBackoff {
			continue
		}
		if _, err := e.EnsureCertificate(ctx, host); err != nil {
			e.recordIssueFailure(host, err)
			e.log.Warn("renewal for %s deferred: %v", host, err)
			continue
		}
		e.log.Info("acme certificate for %s is current again", host)
	}
}

// Missing cert or one inside its renewal window
func (e *Engine) renewalDue(ctx context.Context, m *acmeManager, host string) bool {
	if m.err != nil {
		return true
	}
	loaded := e.magic.AllMatchingCertificates(e.certSubject(ctx, m, host))
	if len(loaded) == 0 {
		cert, err := m.cfg.CacheManagedCertificate(ctx, host)
		if err != nil {
			return true
		}
		return cert.NeedsRenewal(m.cfg)
	}
	for _, cert := range loaded {
		if !cert.NeedsRenewal(m.cfg) {
			return false
		}
	}
	return true
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

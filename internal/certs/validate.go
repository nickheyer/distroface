package certs

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/nickheyer/distroface/internal/settings"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Rejects system saves needing material or ports we lack
func (e *Engine) ValidateSettings(ctx context.Context, scope v1.SettingsScopeType, _ string, paths []string, eff *v1.Settings) error {
	if scope != v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM {
		return nil
	}
	needsRoot := settings.Touched(paths, "ca.acme_enabled") && eff.GetCa().GetAcmeEnabled()
	wantsRootSource := settings.Touched(paths, "tls.primary_source") &&
		eff.GetTls().GetPrimarySource() == v1.CertSource_CERT_SOURCE_APP_CA
	if needsRoot || wantsRootSource {
		row, err := e.store.GetTLSCertificate(ctx, v1.TLSScope_TLS_SCOPE_APP_CA, "", "")
		if err != nil {
			return err
		}
		if row == nil && needsRoot {
			return fmt.Errorf("generate or upload the instance ca on the certificate authority page first, the built in acme server signs from it")
		}
		if row == nil {
			return fmt.Errorf("generate or upload the instance ca on the certificate authority page before choosing it as the certificate source")
		}
	}
	acmeEff := eff.GetAcme()
	port := strings.TrimSpace(acmeEff.GetChallengePort())
	if settings.Touched(paths, "acme.challenge_port", "acme.enabled") && acmeEff.GetEnabled() && port != "" {
		portals, err := e.store.ListRegistryPortals(ctx)
		if err != nil {
			return err
		}
		for _, p := range portals {
			if p.Enabled && p.Port > 0 && strconv.Itoa(p.Port) == port {
				return fmt.Errorf("acme challenge port %s is the listener of portal %q, pick another port", port, p.Name)
			}
		}
	}
	return nil
}

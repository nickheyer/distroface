package certs

import (
	"context"
	"net/http"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// TrustBundlePEM returns the instance root ca, the single anchor that
// validates every certificate distroface issues, empty when none exists
func (e *Engine) TrustBundlePEM(ctx context.Context) string {
	row, err := e.store.GetTLSCertificate(ctx, v1.TLSScope_TLS_SCOPE_APP_CA, "", "")
	if err != nil || row == nil {
		return ""
	}
	return row.CertPEM
}

// TrustBundleHandler serves the instance root ca for clients that need
// to trust self issued certs, public so docker and dfcli can fetch it
func (e *Engine) TrustBundleHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pem := e.TrustBundlePEM(r.Context())
		if pem == "" {
			http.Error(w, "no instance ca configured", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/x-pem-file")
		w.Header().Set("Content-Disposition", `attachment; filename="distroface-ca.pem"`)
		_, _ = w.Write([]byte(pem))
	})
}

// ClientCertMiddleware attaches a verified mtls identity to the request
// context so downstream auth and audit can read it
func ClientCertMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := VerifiedIdentity(r.TLS); id != nil {
			r = r.WithContext(WithClientIdentity(r.Context(), id))
		}
		next.ServeHTTP(w, r)
	})
}

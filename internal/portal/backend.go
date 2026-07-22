package portal

import (
	"crypto/tls"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/nickheyer/distroface/internal/certs"
	"github.com/nickheyer/distroface/pkg/logger"
)

// Marks hops through this server for loop detection
const viaToken = "1.1 distroface"

// Verified mtls identity travels to the backend in this header
const clientCertHeader = "X-Client-Cert-Cn"

// Reports true after proxying to a configured backend
func (p *Portal) ServeBackend(w http.ResponseWriter, r *http.Request) bool {
	if p.backendProxy == nil {
		return false
	}
	if strings.Contains(r.Header.Get("Via"), viaToken) {
		http.Error(w, "proxy loop detected", http.StatusLoopDetected)
		return true
	}
	p.backendProxy.ServeHTTP(w, r)
	return true
}

// Edge owns forwarding headers, client supplied values never pass
func newBackendProxy(target *url.URL, rewriteHost, insecure bool, log *logger.Logger) http.Handler {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			for _, h := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", clientCertHeader} {
				pr.Out.Header.Del(h)
			}
			pr.SetURL(target)
			pr.SetXForwarded()
			if !rewriteHost {
				pr.Out.Host = pr.In.Host
			}
			pr.Out.Header.Add("Via", viaToken)
			if id := certs.VerifiedIdentity(pr.In.TLS); id != nil {
				pr.Out.Header.Set(clientCertHeader, id.CommonName)
			}
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Error("portal backend %s: %s %s: %v", target.Host, r.Method, r.URL.Path, err)
			http.Error(w, "backend unavailable", http.StatusBadGateway)
		},
	}
}

// Broken stored url serves errors instead of registry fallback
func brokenBackend(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "backend misconfigured", http.StatusBadGateway)
}

package webhook

import (
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// Vet ips at dial time so dns tricks fail
func newSafeTransport(allowPrivate func() bool) http.RoundTripper {
	dialer := &net.Dialer{
		Timeout:   requestTimeout,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("webhook: invalid dial address %q: %w", address, err)
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("webhook: dial address %q is not an IP", host)
			}
			return checkWebhookIP(ip, allowPrivate())
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
func checkWebhookIP(ip net.IP, allowPrivate bool) error {
	switch {
	case ip.IsLoopback():
		return fmt.Errorf("webhook: destination %s is a loopback address", ip)
	case ip.IsUnspecified():
		return fmt.Errorf("webhook: destination %s is unspecified", ip)
	case ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast():
		return fmt.Errorf("webhook: destination %s is link-local (metadata range)", ip)
	case ip.IsMulticast():
		return fmt.Errorf("webhook: destination %s is multicast", ip)
	case ip.IsPrivate() && !allowPrivate:
		return fmt.Errorf("webhook: destination %s is in a private range (set webhooks.allow_private_networks to permit)", ip)
	}
	return nil
}

package llm

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"syscall"
	"time"
)

// Largest provider response body read into memory

// Dials vet the resolved ip so dns tricks cannot reach forbidden ranges
func newHTTPClient(cfg Config) *http.Client {
	// Zero waits as long as the model needs
	timeout := time.Duration(max(cfg.TimeoutSeconds, 0)) * time.Second
	allow := cfg.AllowPrivate
	if allow == nil {
		allow = func() bool { return true }
	}
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("llm: invalid dial address %q: %w", address, err)
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("llm: dial address %q is not an ip", host)
			}
			return checkIP(ip, allow())
		},
	}
	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          8,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: time.Second,
		ResponseHeaderTimeout: timeout,
	}
	if cfg.SkipTLSVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // Operator opted in per connector
	}
	return &http.Client{Timeout: timeout, Transport: transport}
}

// Private, loopback, and link local ranges need the operator's permission
func checkIP(ip net.IP, allowPrivate bool) error {
	switch {
	case ip.IsUnspecified():
		return fmt.Errorf("llm: destination %s is unspecified", ip)
	case ip.IsMulticast():
		return fmt.Errorf("llm: destination %s is multicast", ip)
	case ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast():
		if !allowPrivate {
			return fmt.Errorf("llm: destination %s is link local (set git.agents.allow_private_networks to permit)", ip)
		}
	case ip.IsLoopback() || ip.IsPrivate():
		if !allowPrivate {
			return fmt.Errorf("llm: destination %s is on a private network (set git.agents.allow_private_networks to permit)", ip)
		}
	}
	return nil
}

// Posts json and decodes json, non 2xx answers become classified errors
func postJSON(ctx context.Context, httpc *http.Client, cfg Config, url string, auth func(*http.Request), body any, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	applyHeaders(req, cfg)
	if auth != nil {
		auth(req)
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return &APIError{Message: err.Error(), class: ErrUnavailable}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return &APIError{Message: err.Error(), class: ErrUnavailable}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return newAPIError(resp.StatusCode, string(raw))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return &APIError{Status: resp.StatusCode, Message: fmt.Sprintf("invalid json from provider: %v", err), class: ErrUnavailable}
	}
	return nil
}

// Gets json, non 2xx answers become classified errors
func getJSON(ctx context.Context, httpc *http.Client, cfg Config, url string, auth func(*http.Request), out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	applyHeaders(req, cfg)
	if auth != nil {
		auth(req)
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return &APIError{Message: err.Error(), class: ErrUnavailable}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return &APIError{Message: err.Error(), class: ErrUnavailable}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return newAPIError(resp.StatusCode, string(raw))
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return &APIError{Status: resp.StatusCode, Message: fmt.Sprintf("invalid json from provider: %v", err), class: ErrUnavailable}
	}
	return nil
}

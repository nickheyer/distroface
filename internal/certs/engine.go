package certs

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/pkg/config"
	"github.com/nickheyer/distroface/pkg/logger"
	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

// Terminates tls for primary server + portal listeners - certs from acme and/or pem files
type Engine struct {
	cfg     *config.Config
	store   *stores.Store
	log     *logger.Logger
	manager *autocert.Manager
	manual  *tls.Certificate
}

func NewEngine(cfg *config.Config, store *stores.Store, log *logger.Logger) (*Engine, error) {
	e := &Engine{cfg: cfg, store: store, log: log}

	if cfg.TLS.CertFile != "" || cfg.TLS.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.TLS.CertFile, cfg.TLS.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("loading manual tls keypair: %w", err)
		}
		e.manual = &cert
	}

	if cfg.TLS.ACME.Enabled {
		e.manager = &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			Email:      cfg.TLS.ACME.Email,
			Cache:      dbCache{store: store},
			HostPolicy: e.hostPolicy,
		}
		if cfg.TLS.ACME.DirectoryURL != "" {
			e.manager.Client = &acme.Client{DirectoryURL: cfg.TLS.ACME.DirectoryURL}
		}
	}

	if e.manual == nil && e.manager == nil {
		return nil, fmt.Errorf("tls enabled but neither acme nor cert_file/key_file configured")
	}
	return e, nil
}

func (e *Engine) ACMEEnabled() bool {
	return e != nil && e.manager != nil
}

func (e *Engine) ManualCertLoaded() bool {
	return e != nil && e.manual != nil
}

// Bare lowercase host from a host or host port string
func bareHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

// Dotted dns name, not an ip, not localhost
func issuableHost(host string) bool {
	if host == "" || host == "localhost" || !strings.Contains(host, ".") {
		return false
	}
	return net.ParseIP(host) == nil
}

// Union of config domains, primary hostname, and db registered domains
func (e *Engine) hostPolicy(ctx context.Context, host string) error {
	host = bareHost(host)
	for _, d := range e.cfg.TLS.ACME.Domains {
		if bareHost(d) == host {
			return nil
		}
	}
	if primary := bareHost(e.cfg.Server.Hostname); primary == host && issuableHost(host) {
		return nil
	}
	d, err := e.store.GetCertificateDomainByName(ctx, host)
	if err != nil {
		return err
	}
	if d != nil && d.IssuanceAllowed() {
		return nil
	}
	return fmt.Errorf("host %q not allowed by certificate policy", host)
}

// Shared server config, sni picks the right cert per hostname
func (e *Engine) TLSConfig() *tls.Config {
	cfg := &tls.Config{
		MinVersion:     tls.VersionTLS12,
		NextProtos:     []string{"h2", "http/1.1"},
		GetCertificate: e.getCertificate,
	}
	if e.manager != nil {
		cfg.NextProtos = append(cfg.NextProtos, acme.ALPNProto)
	}
	return cfg
}

// Acme first for allowed hosts, manual pair as fallback
func (e *Engine) getCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	if e.manager != nil {
		cert, err := e.manager.GetCertificate(hello)
		if err == nil {
			return cert, nil
		}
		if e.manual == nil {
			return nil, err
		}
	}
	return e.manual, nil
}

// Handler for the cleartext port, answers http-01 then redirects
func (e *Engine) HTTPChallengeHandler() http.Handler {
	var fallback http.Handler
	if e.cfg.TLS.Enabled && e.cfg.TLS.ACME.RedirectHTTP {
		fallback = nil // Autocert default redirects to https
	} else {
		fallback = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "https required", http.StatusForbidden)
		})
	}
	if e.manager != nil {
		return e.manager.HTTPHandler(fallback)
	}
	if fallback == nil {
		fallback = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			target := "https://" + bareHost(r.Host) + r.URL.RequestURI()
			http.Redirect(w, r, target, http.StatusFound)
		})
	}
	return fallback
}

// Modern ecdsa capable hello so the cache key matches real clients
func syntheticHello(domain string) *tls.ClientHelloInfo {
	return &tls.ClientHelloInfo{
		ServerName:        domain,
		SupportedProtos:   []string{"h2", "http/1.1"},
		SupportedVersions: []uint16{tls.VersionTLS13, tls.VersionTLS12},
		CipherSuites:      []uint16{tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256},
		SupportedCurves:   []tls.CurveID{tls.CurveP256},
		SignatureSchemes:  []tls.SignatureScheme{tls.ECDSAWithP256AndSHA256},
	}
}

// Blocks until issued or failed, autocert caps this at five minutes
func (e *Engine) EnsureCertificate(ctx context.Context, domain string) (*CertInfo, error) {
	if e.manager == nil {
		return nil, fmt.Errorf("acme is not enabled")
	}
	domain = bareHost(domain)
	if !issuableHost(domain) {
		return nil, fmt.Errorf("domain %q is not a publicly issuable hostname", domain)
	}
	if _, err := e.manager.GetCertificate(syntheticHello(domain)); err != nil {
		return nil, err
	}
	return e.CertificateInfo(ctx, domain)
}

type CertInfo struct {
	Domain    string
	Issued    bool
	Issuer    string
	NotBefore time.Time
	NotAfter  time.Time
	SANs      []string
}

// Reads the cached chain without triggering issuance
func (e *Engine) CertificateInfo(ctx context.Context, domain string) (*CertInfo, error) {
	domain = bareHost(domain)
	info := &CertInfo{Domain: domain}
	if e.store == nil {
		return info, nil
	}
	data, err := e.store.GetACMECacheEntry(ctx, domain)
	if err != nil {
		return nil, err
	}
	if data == nil {
		if data, err = e.store.GetACMECacheEntry(ctx, domain+"+rsa"); err != nil || data == nil {
			return info, err
		}
	}
	leaf := firstCertificate(data)
	if leaf == nil {
		return info, nil
	}
	info.Issued = true
	info.Issuer = leaf.Issuer.CommonName
	info.NotBefore = leaf.NotBefore
	info.NotAfter = leaf.NotAfter
	info.SANs = leaf.DNSNames
	return info, nil
}

// Leaf cert from autocert's key then chain pem layout
func firstCertificate(data []byte) *x509.Certificate {
	for len(data) > 0 {
		block, rest := pem.Decode(data)
		if block == nil {
			return nil
		}
		data = rest
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil
		}
		return cert
	}
	return nil
}

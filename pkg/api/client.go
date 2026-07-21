package api

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"github.com/spf13/viper"
)

type TokenManager struct {
	mu        sync.RWMutex
	token     string
	expiresAt time.Time
}

func NewTokenManager(token string, expiresAt time.Time) *TokenManager {
	return &TokenManager{token: token, expiresAt: expiresAt}
}

func (tm *TokenManager) GetToken() string {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.token
}

func (tm *TokenManager) SetToken(token string, expiresAt time.Time) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.token = token
	tm.expiresAt = expiresAt
}

// False for empty tokens and PATs
func (tm *TokenManager) IsExpired() bool {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	if tm.token == "" || strings.HasPrefix(tm.token, patPrefix) {
		return false
	}
	return time.Now().After(tm.expiresAt)
}

func (tm *TokenManager) IsPAT() bool {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return strings.HasPrefix(tm.token, patPrefix)
}

// Connect rpc control plane plus http data plane for bytes
type Client struct {
	BaseURL    string
	Username   string
	Tokens     *TokenManager
	HTTPClient *http.Client
}

var client *Client

func initClient() error {
	config, err := readAuthConfig()
	if err != nil {
		return err
	}

	// DFCLI_TOKEN env overrides the stored token
	if envToken := os.Getenv("DFCLI_TOKEN"); envToken != "" {
		config.Token = envToken
		config.ExpiresAt = time.Now().Add(24 * 365 * time.Hour)
	}

	serverURL := viper.GetString("server")
	if serverURL == defaultServerURL && config.Server != "" {
		serverURL = config.Server
	}
	if !strings.HasPrefix(serverURL, "http://") && !strings.HasPrefix(serverURL, "https://") {
		serverURL = detectScheme(serverURL) + "://" + serverURL
		debugf("No scheme on server URL, using %s", serverURL)
	}

	timeout := viper.GetDuration("timeout")
	if timeout == 0 {
		timeout = 5 * time.Minute
	}

	client = &Client{
		BaseURL:    strings.TrimRight(serverURL, "/"),
		Username:   config.Username,
		Tokens:     NewTokenManager(config.Token, config.ExpiresAt),
		HTTPClient: &http.Client{Timeout: timeout, Transport: newTransport()},
	}
	return nil
}

// Bare hosts probe tls first, cleartext is the fallback
func detectScheme(host string) string {
	addr := host
	if _, _, err := net.SplitHostPort(host); err != nil {
		addr = net.JoinHostPort(host, "443")
	}
	dialer := &net.Dialer{Timeout: 3 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{InsecureSkipVerify: true})
	if err == nil {
		conn.Close()
		return "https"
	}
	return "http"
}

// Installed instance CA joins the system roots
func newTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	pem, err := os.ReadFile(caPath())
	if err != nil {
		return t
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if pool.AppendCertsFromPEM(pem) {
		debugf("Trusting instance CA from %s", caPath())
		t.TLSClientConfig = &tls.Config{RootCAs: pool}
	}
	return t
}

// ── RPC plumbing ─────────────────────────────────────────────────────────

func (c *Client) Artifacts() distrofacev1connect.ArtifactServiceClient {
	return distrofacev1connect.NewArtifactServiceClient(c.HTTPClient, c.BaseURL, c.rpcOpts()...)
}

func (c *Client) Auth() distrofacev1connect.AuthServiceClient {
	return distrofacev1connect.NewAuthServiceClient(c.HTTPClient, c.BaseURL, c.rpcOpts()...)
}

func (c *Client) Repositories() distrofacev1connect.RepositoryServiceClient {
	return distrofacev1connect.NewRepositoryServiceClient(c.HTTPClient, c.BaseURL, c.rpcOpts()...)
}

func (c *Client) rpcOpts() []connect.ClientOption {
	return []connect.ClientOption{connect.WithInterceptors(c.authInterceptor())}
}

// Bearer header plus proactive and reactive session refresh
func (c *Client) authInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if c.Tokens.IsExpired() {
				if err := c.refreshToken(ctx); err != nil {
					return nil, err
				}
			}
			if token := c.Tokens.GetToken(); token != "" {
				req.Header().Set("Authorization", "Bearer "+token)
			}

			resp, err := next(ctx, req)
			if err == nil || connect.CodeOf(err) != connect.CodeUnauthenticated || c.Tokens.GetToken() == "" {
				return resp, err
			}

			// Single retry after a refresh, PATs never refresh
			debugf("Received unauthenticated, refreshing token and retrying...")
			if rerr := c.refreshToken(ctx); rerr != nil {
				return nil, rerr
			}
			req.Header().Set("Authorization", "Bearer "+c.Tokens.GetToken())
			return next(ctx, req)
		}
	}
}

// Trades the current session for a fresh one
func (c *Client) refreshToken(ctx context.Context) error {
	if c.Tokens.IsPAT() {
		return fmt.Errorf("personal access token was rejected - it may be expired or revoked (create a new one and run 'dfcli login --token ...')")
	}
	token := c.Tokens.GetToken()
	if token == "" {
		return fmt.Errorf("not logged in - run 'dfcli login'")
	}

	debugf("Refreshing session token...")

	auth := distrofacev1connect.NewAuthServiceClient(c.HTTPClient, c.BaseURL)
	req := connect.NewRequest(&v1.RefreshSessionRequest{})
	req.Header().Set("Authorization", "Bearer "+token)
	resp, err := auth.RefreshSession(ctx, req)
	if err != nil {
		return fmt.Errorf("session refresh failed: %v - run 'dfcli login'", rpcErr(err))
	}

	expiresAt := time.Unix(resp.Msg.ExpiresAt, 0)
	c.Tokens.SetToken(resp.Msg.SessionToken, expiresAt)
	return saveConfig(AuthConfig{Token: resp.Msg.SessionToken, ExpiresAt: expiresAt, Server: c.BaseURL})
}

// Friendly text for surfaced connect errors
func rpcErr(err error) error {
	var ce *connect.Error
	if errors.As(err, &ce) {
		return hintTLS(fmt.Errorf("server returned %s: %s", ce.Code(), ce.Message()))
	}
	return hintTLS(err)
}

// Cert failures point at the trust command
func hintTLS(err error) error {
	if err != nil && strings.Contains(err.Error(), "x509:") {
		return fmt.Errorf("%w\nThe server's certificate is not trusted - run 'dfcli trust install' to trust the instance CA", err)
	}
	return err
}

// ── HTTP data plane ──────────────────────────────────────────────────────

// Raw byte transfers, streams never retry on auth
func (c *Client) doData(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	retriable := body == nil

	var resp *http.Response
	for attempt := range 2 {
		if attempt == 0 && c.Tokens.IsExpired() {
			if err := c.refreshToken(ctx); err != nil {
				return nil, err
			}
		}

		req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		if token := c.Tokens.GetToken(); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}

		resp, err = c.HTTPClient.Do(req)
		if err != nil {
			return nil, hintTLS(fmt.Errorf("request failed: %w", err))
		}
		if resp.StatusCode != http.StatusUnauthorized || !retriable || attempt > 0 {
			break
		}

		resp.Body.Close()
		debugf("Received 401 Unauthorized, refreshing token and retrying...")
		if err := c.refreshToken(ctx); err != nil {
			return nil, err
		}
	}

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return resp, nil
}

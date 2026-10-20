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

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
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

// An explicit --server always wins over the server of the last login
func initClient(serverFlagSet bool) error {
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
	if !serverFlagSet && serverURL == defaultServerURL && config.Server != "" {
		serverURL = config.Server
	}
	debugf("Using server %s", serverURL)
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

// Shared client with bearer injection and session refresh
func (c *Client) rpc() *connect.Client {
	return connect.NewClient(connecthttp.NewTransport(c.HTTPClient, c.BaseURL), c.authInterceptor())
}

// Stored credentials never ride along, login and refresh use this
func (c *Client) bareRPC() *connect.Client {
	return connect.NewClient(connecthttp.NewTransport(c.HTTPClient, c.BaseURL))
}

func (c *Client) Artifacts() distrofacev1connect.ArtifactServiceClient {
	return distrofacev1connect.NewArtifactServiceClient(c.rpc())
}

func (c *Client) Auth() distrofacev1connect.AuthServiceClient {
	return distrofacev1connect.NewAuthServiceClient(c.rpc())
}

func (c *Client) Repositories() distrofacev1connect.RepositoryServiceClient {
	return distrofacev1connect.NewRepositoryServiceClient(c.rpc())
}

func (c *Client) Git() distrofacev1connect.GitServiceClient {
	return distrofacev1connect.NewGitServiceClient(c.rpc())
}

func (c *Client) GitRefs() distrofacev1connect.GitRefServiceClient {
	return distrofacev1connect.NewGitRefServiceClient(c.rpc())
}

func (c *Client) GitFiles() distrofacev1connect.GitFileServiceClient {
	return distrofacev1connect.NewGitFileServiceClient(c.rpc())
}

func (c *Client) Releases() distrofacev1connect.ReleaseServiceClient {
	return distrofacev1connect.NewReleaseServiceClient(c.rpc())
}

func (c *Client) Issues() distrofacev1connect.IssueServiceClient {
	return distrofacev1connect.NewIssueServiceClient(c.rpc())
}

func (c *Client) Pulls() distrofacev1connect.PullRequestServiceClient {
	return distrofacev1connect.NewPullRequestServiceClient(c.rpc())
}

func (c *Client) Keys() distrofacev1connect.KeyServiceClient {
	return distrofacev1connect.NewKeyServiceClient(c.rpc())
}

func (c *Client) Organizations() distrofacev1connect.OrganizationServiceClient {
	return distrofacev1connect.NewOrganizationServiceClient(c.rpc())
}

func (c *Client) APITokens() distrofacev1connect.TokenServiceClient {
	return distrofacev1connect.NewTokenServiceClient(c.rpc())
}

func (c *Client) Actions() distrofacev1connect.ActionsServiceClient {
	return distrofacev1connect.NewActionsServiceClient(c.rpc())
}

func (c *Client) Runner() distrofacev1connect.RunnerServiceClient {
	return distrofacev1connect.NewRunnerServiceClient(c.rpc())
}

func (c *Client) Users() distrofacev1connect.UserServiceClient {
	return distrofacev1connect.NewUserServiceClient(c.rpc())
}

// Bearer header plus proactive and reactive session refresh
func (c *Client) authInterceptor() connect.ClientInterceptor {
	return func(next connect.ClientFunc) connect.ClientFunc {
		return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
			if c.Tokens.IsExpired() {
				if err := c.refreshToken(ctx); err != nil {
					return nil, err
				}
			}
			info, _ := connect.CallInfoForClientContext(ctx)
			if token := c.Tokens.GetToken(); token != "" && info != nil {
				info.RequestHeader().Set("Authorization", "Bearer "+token)
			}
			stream, err := next(ctx, spec)
			if err != nil {
				return nil, err
			}
			return &refreshingStream{ClientStream: stream, ctx: ctx, spec: spec, next: next, info: info, client: c}, nil
		}
	}
}

// Reopens the call once after a refresh when the server rejects the session
type refreshingStream struct {
	connect.ClientStream
	ctx      context.Context
	spec     connect.Spec
	next     connect.ClientFunc
	info     *connect.CallInfo
	client   *Client
	sent     []any
	closed   bool
	received int
	retried  bool
}

func (r *refreshingStream) Send(msg any) error {
	r.sent = append(r.sent, msg)
	return r.ClientStream.Send(msg)
}

func (r *refreshingStream) CloseSend() error {
	r.closed = true
	return r.ClientStream.CloseSend()
}

func (r *refreshingStream) Receive(msg any) error {
	err := r.ClientStream.Receive(msg)
	if err == nil {
		r.received++
		return nil
	}
	if r.retried || r.received > 0 || connect.CodeOf(err) != connect.CodeUnauthenticated || r.client.Tokens.GetToken() == "" {
		return err
	}
	r.retried = true

	// Single retry after a refresh, PATs never refresh
	debugf("Received unauthenticated, refreshing token and retrying...")
	if rerr := r.client.refreshToken(r.ctx); rerr != nil {
		return rerr
	}
	if r.info != nil {
		r.info.RequestHeader().Set("Authorization", "Bearer "+r.client.Tokens.GetToken())
	}
	_ = r.Close()

	stream, err := r.next(r.ctx, r.spec)
	if err != nil {
		return err
	}
	r.ClientStream = stream
	for _, m := range r.sent {
		if err := stream.Send(m); err != nil {
			return err
		}
	}
	if r.closed {
		if err := stream.CloseSend(); err != nil {
			return err
		}
	}
	if err := stream.Receive(msg); err != nil {
		return err
	}
	r.received++
	return nil
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

	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer "+token)
	resp, err := distrofacev1connect.NewAuthServiceClient(c.bareRPC()).RefreshSession(ctx, &v1.RefreshSessionRequest{})
	if err != nil {
		return fmt.Errorf("session refresh failed: %w - run 'dfcli login'", rpcErr(err))
	}

	expiresAt := time.Unix(resp.ExpiresAt, 0)
	c.Tokens.SetToken(resp.SessionToken, expiresAt)
	return saveConfig(AuthConfig{Token: resp.SessionToken, ExpiresAt: expiresAt, Server: c.BaseURL})
}

// Friendly text for surfaced connect errors
func rpcErr(err error) error {
	var ce *connect.Error
	if errors.As(err, &ce) {
		if ce.Code() == connect.CodeUnimplemented {
			server := "The server"
			if client != nil {
				server = client.BaseURL
			}
			return hintTLS(fmt.Errorf("%s does not expose this call (%s)\nIt may be an older release or have the feature disabled, check --server", server, ce.Message()))
		}
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

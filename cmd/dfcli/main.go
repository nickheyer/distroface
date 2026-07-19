// DistroFace command line client ported from v1
package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"connectrpc.com/connect"
	distrofacev1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/term"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Set at build time via ldflags
var Version = "dev"

const (
	defaultServerURL = "http://localhost:8080"
	patPrefix        = "df_"
)

// Persisted config json shape same as v1
type AuthConfig struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	Username  string    `json:"username"`
	Server    string    `json:"server"`
}

// Mirrors the v1 api artifact JSON
type Artifact struct {
	ID         string            `json:"id"`
	RepoID     int64             `json:"repo_id"`
	Name       string            `json:"name"`
	Path       string            `json:"path"`
	UploadID   string            `json:"upload_id"`
	Version    string            `json:"version"`
	Size       int64             `json:"size"`
	MimeType   string            `json:"mime_type"`
	Metadata   string            `json:"metadata"`
	Properties map[string]string `json:"properties"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

// Mirrors the v1 api repo JSON
type ArtifactRepository struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Owner       string    `json:"owner"`
	Private     bool      `json:"private"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Mirrors the v1 search endpoint JSON
type SearchResponse struct {
	Results []Artifact `json:"results"`
	Total   int        `json:"total"`
	Limit   int        `json:"limit"`
	Offset  int        `json:"offset"`
	Sort    string     `json:"sort"`
	Order   string     `json:"order"`
}

// ── Token management ─────────────────────────────────────────────────────

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

type APIClient struct {
	BaseURL      string
	TokenManager *TokenManager
	HTTPClient   *http.Client
}

var (
	cfgFile string
	client  *APIClient
)

func main() {
	rootCmd := &cobra.Command{
		Use:           "dfcli",
		Short:         "DistroFace CLI",
		Long:          `Command line interface for DistroFace registry and artifact management`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return initClient()
		},
	}

	viper.SetDefault("server", defaultServerURL)
	viper.SetDefault("timeout", "5m")

	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is ~/.dfcli/config.json)")
	rootCmd.PersistentFlags().String("server", defaultServerURL, "DistroFace server URL")
	rootCmd.PersistentFlags().String("timeout", "5m", "Request timeout (30s, 5m, 1h, etc.)")
	rootCmd.PersistentFlags().Bool("debug", false, "Enable debug output")

	_ = viper.BindPFlag("server", rootCmd.PersistentFlags().Lookup("server"))
	_ = viper.BindPFlag("timeout", rootCmd.PersistentFlags().Lookup("timeout"))
	_ = viper.BindPFlag("debug", rootCmd.PersistentFlags().Lookup("debug"))

	rootCmd.AddCommand(newLoginCmd())
	rootCmd.AddCommand(newLogoutCmd())
	rootCmd.AddCommand(newTrustCmd())

	imageCmd := &cobra.Command{
		Use:   "image",
		Short: "Manage container images",
	}
	imageCmd.AddCommand(
		newImageListCmd(),
		newImageTagsCmd(),
	)
	rootCmd.AddCommand(imageCmd)

	artifactCmd := &cobra.Command{
		Use:   "artifact",
		Short: "Manage artifacts and artifact repositories",
	}
	artifactCmd.AddCommand(
		newArtifactRepoCreateCmd(),
		newArtifactRepoListCmd(),
		newArtifactUploadCmd(),
		newArtifactDownloadCmd(),
		newArtifactDeleteCmd(),
		newArtifactSearchCmd(),
	)
	rootCmd.AddCommand(artifactCmd)

	rootCmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("dfcli version %s\n", Version)
		},
	})

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// ── Config and client setup ───────────────────────────────────────────────────────────────────────────────────────────

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		home := getHomeDir()
		configDir := filepath.Join(home, ".dfcli")
		if err := os.MkdirAll(configDir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create config directory: %v\n", err)
			os.Exit(1)
		}
		viper.AddConfigPath(configDir)
		viper.SetConfigName("config")
		viper.SetConfigType("json")
	}

	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			fmt.Fprintf(os.Stderr, "Failed to read config file: %v\n", err)
			os.Exit(1)
		}
	}
}

func initClient() error {
	var config AuthConfig
	data, err := os.ReadFile(getConfigPath())
	if err == nil {
		if err := json.Unmarshal(data, &config); err != nil {
			return fmt.Errorf("failed to parse config: %v", err)
		}
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
		serverURL = "http://" + serverURL
	}

	clientTimeout := viper.GetDuration("timeout")
	if clientTimeout == 0 {
		clientTimeout = 5 * time.Minute
	}

	client = &APIClient{
		BaseURL:      strings.TrimRight(serverURL, "/"),
		TokenManager: NewTokenManager(config.Token, config.ExpiresAt),
		HTTPClient:   &http.Client{Timeout: clientTimeout},
	}
	return nil
}

func getHomeDir() string {
	if runtime.GOOS == "windows" {
		home := os.Getenv("HOMEDRIVE") + os.Getenv("HOMEPATH")
		if home == "" {
			home = os.Getenv("USERPROFILE")
		}
		return home
	}
	return os.Getenv("HOME")
}

func getConfigPath() string {
	if cfgFile != "" {
		return cfgFile
	}
	return filepath.Join(getHomeDir(), ".dfcli", "config.json")
}

func saveConfig(config AuthConfig) error {
	configPath := getConfigPath()
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %v", err)
	}

	// Preserve fields not being overwritten
	var existing AuthConfig
	if data, err := os.ReadFile(configPath); err == nil {
		if json.Unmarshal(data, &existing) == nil {
			if config.Username == "" {
				config.Username = existing.Username
			}
		}
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %v", err)
	}
	return os.WriteFile(configPath, data, 0600)
}

// ── HTTP plumbing ────────────────────────────────────────────────────────

func (c *APIClient) refreshToken() error {
	if c.TokenManager.IsPAT() {
		return fmt.Errorf("personal access token was rejected - it may be expired or revoked (create a new one and run 'dfcli login --token ...')")
	}
	token := c.TokenManager.GetToken()
	if token == "" {
		return fmt.Errorf("not logged in - run 'dfcli login'")
	}

	if viper.GetBool("debug") {
		fmt.Println("Refreshing access token...")
	}

	payload, _ := json.Marshal(map[string]string{"refresh_token": token})
	req, err := http.NewRequest(http.MethodPost, c.BaseURL+"/api/v1/auth/refresh", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("refresh failed with status %d: %s - run 'dfcli login'", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var result struct {
		Token     string `json:"token"`
		ExpiresIn int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to parse refresh response: %v", err)
	}
	if result.Token == "" {
		return fmt.Errorf("no token in refresh response")
	}

	expiresAt := time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)
	c.TokenManager.SetToken(result.Token, expiresAt)
	return saveConfig(AuthConfig{Token: result.Token, ExpiresAt: expiresAt, Server: c.BaseURL})
}

func (c *APIClient) doRequest(method, path string, body interface{}) (*http.Response, error) {
	// Streaming uploads pass the reader straight through
	if method == http.MethodPatch && body != nil {
		if reader, ok := body.(io.Reader); ok {
			req, err := http.NewRequest(method, c.BaseURL+path, reader)
			if err != nil {
				return nil, fmt.Errorf("failed to create PATCH request: %w", err)
			}
			if token := c.TokenManager.GetToken(); token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			resp, err := c.HTTPClient.Do(req)
			if err != nil {
				return nil, err
			}
			if resp.StatusCode >= 400 {
				respBody, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(respBody))
			}
			return resp, nil
		}
	}

	var bodyBytes []byte
	if body != nil {
		var err error
		switch v := body.(type) {
		case io.Reader:
			bodyBytes, err = io.ReadAll(v)
		default:
			bodyBytes, err = json.Marshal(body)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to prepare request body: %w", err)
		}
	}

	var resp *http.Response
	for retries := 0; retries < 2; retries++ {
		var bodyReader io.Reader
		if bodyBytes != nil {
			bodyReader = bytes.NewReader(bodyBytes)
		}
		req, err := http.NewRequest(method, c.BaseURL+path, bodyReader)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		// Sessions refresh proactively, PATs and anonymous never do
		if retries == 0 && c.TokenManager.IsExpired() {
			if err := c.refreshToken(); err != nil {
				return nil, fmt.Errorf("token refresh failed: %w", err)
			}
		}

		if token := c.TokenManager.GetToken(); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err = c.HTTPClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("request failed: %w", err)
		}

		if resp.StatusCode != http.StatusUnauthorized || retries > 0 {
			break
		}

		// Single 401 retry, sessions refresh and PATs fail
		resp.Body.Close()
		if viper.GetBool("debug") {
			fmt.Println("Received 401 Unauthorized, refreshing token and retrying...")
		}
		if err := c.refreshToken(); err != nil {
			return nil, err
		}
	}

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(respBody))
	}
	return resp, nil
}

// ── Auth commands ────────────────────────────────────────────────────────

func login(server, username, password string) (string, time.Time, error) {
	httpClient := &http.Client{Timeout: 30 * time.Second}

	payload, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		return "", time.Time{}, err
	}

	req, err := http.NewRequest(http.MethodPost, server+"/api/v1/auth/login", bytes.NewReader(payload))
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", time.Time{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to read response: %v", err)
	}
	if viper.GetBool("debug") {
		fmt.Printf("Login response: %s\n", string(body))
	}
	if resp.StatusCode != http.StatusOK {
		return "", time.Time{}, fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var result struct {
		Token     string `json:"token"`
		ExpiresIn int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", time.Time{}, fmt.Errorf("failed to parse response: %v", err)
	}
	if result.Token == "" {
		return "", time.Time{}, fmt.Errorf("no token in response")
	}
	return result.Token, time.Now().Add(time.Duration(result.ExpiresIn) * time.Second), nil
}

func newLoginCmd() *cobra.Command {
	var username, password, patToken string

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in to DistroFace server",
		Long: `Log in with a username/password, or store a personal access token:

  dfcli login --token df_xxxxxxxx

Personal access tokens never require refreshing and are the recommended
credential for CI. The DFCLI_TOKEN environment variable overrides the
stored token without touching the config file.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			server := client.BaseURL

			// PAT login stores and verifies, no session dance
			if patToken != "" {
				if !strings.HasPrefix(patToken, patPrefix) {
					return fmt.Errorf("token must start with %q - create one under Settings → Tokens", patPrefix)
				}
				config := AuthConfig{
					Token:     patToken,
					ExpiresAt: time.Now().Add(24 * 365 * 10 * time.Hour), // PATs carry their own expiry server-side
					Server:    server,
				}
				if err := saveConfig(config); err != nil {
					return fmt.Errorf("failed to save config: %v", err)
				}
				fmt.Println("Personal access token stored")
				return nil
			}

			if username == "" {
				fmt.Print("Username: ")
				fmt.Scanln(&username)
			}
			if password == "" {
				fmt.Print("Password: ")
				bytePassword, err := term.ReadPassword(int(syscall.Stdin))
				if err != nil {
					return fmt.Errorf("failed to read password: %v", err)
				}
				password = string(bytePassword)
				fmt.Println()
			}

			token, expiry, err := login(server, username, password)
			if err != nil {
				return fmt.Errorf("login failed: %v", err)
			}

			config := AuthConfig{
				Token:     token,
				Username:  username,
				Server:    server,
				ExpiresAt: expiry,
			}
			if err := saveConfig(config); err != nil {
				return fmt.Errorf("failed to save config: %v", err)
			}

			fmt.Printf("Successfully logged in as %s\n", username)
			return nil
		},
	}

	cmd.Flags().StringVarP(&username, "username", "u", "", "Username (optional, will prompt if not provided)")
	cmd.Flags().StringVarP(&password, "password", "p", "", "Password (optional, will prompt if not provided)")
	cmd.Flags().StringVar(&patToken, "token", "", "Personal access token (df_...) to store instead of a session")

	return cmd
}

func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Log out from DistroFace server",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := os.Remove(getConfigPath()); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("failed to remove config: %v", err)
			}
			fmt.Println("Successfully logged out")
			return nil
		},
	}
}

// ── Trust distribution ────────────────────────────────────────────────────

func newTrustCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "trust",
		Short: "Trust the DistroFace instance certificate authority",
		Long: `Fetch and install the instance root CA that signs every certificate
DistroFace issues, so clients trust its self-issued TLS.`,
	}
	cmd.AddCommand(newTrustShowCmd(), newTrustInstallCmd())
	return cmd
}

// Downloads the public instance root CA pem, - fall back to cleartext if possible
func fetchInstanceCA() ([]byte, error) {
	c := &http.Client{
		Timeout: client.HTTPClient.Timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	path := "/.well-known/distroface/ca.pem"
	resp, err := c.Get(client.BaseURL + path)
	if err != nil && strings.HasPrefix(client.BaseURL, "https://") {
		// No serving cert yet, retry the always cleartext anchor
		httpURL := "http://" + strings.TrimPrefix(client.BaseURL, "https://") + path
		fmt.Fprintf(os.Stderr, "https fetch failed (%v), retrying over http\n", err)
		resp, err = c.Get(httpURL)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to reach the instance CA endpoint: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("the instance has no root CA yet - an admin can generate one in Settings → PKI")
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("fetch CA failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return io.ReadAll(resp.Body)
}

// SHA-256 fingerprint of the leaf cert for out of band verification
func caFingerprint(pemData []byte) string {
	block, _ := pem.Decode(pemData)
	if block == nil {
		return "unparseable"
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "unparseable"
	}
	sum := sha256.Sum256(cert.Raw)
	var parts []string
	for _, b := range sum {
		parts = append(parts, hex.EncodeToString([]byte{b}))
	}
	return strings.ToUpper(strings.Join(parts, ":"))
}

func newTrustShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print the instance root CA to stdout",
		RunE: func(cmd *cobra.Command, args []string) error {
			pem, err := fetchInstanceCA()
			if err != nil {
				return err
			}
			_, err = os.Stdout.Write(pem)
			return err
		},
	}
}

func newTrustInstallCmd() *cobra.Command {
	var host string
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Trust the instance CA so docker pull works over its TLS",
		Long: `Install the instance root CA into Docker's per-registry trust store
(/etc/docker/certs.d/<host>/ca.crt) so 'docker pull' trusts the registry
over its self-issued TLS. Docker picks it up with no daemon restart.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			pem, err := fetchInstanceCA()
			if err != nil {
				return err
			}
			// Print to stderr so the user can verify before trusting it
			fmt.Fprintf(os.Stderr, "Fetched instance CA, SHA-256 fingerprint:\n  %s\nVerify this matches the server's PKI page before trusting.\n\n", caFingerprint(pem))

			if host == "" {
				u, err := url.Parse(client.BaseURL)
				if err != nil {
					return fmt.Errorf("could not parse server URL %q: %w", client.BaseURL, err)
				}
				host = u.Host
			}

			// Docker's certs.d trust store is Linux and dockerd specific
			if runtime.GOOS != "linux" {
				tmp := filepath.Join(os.TempDir(), "distroface-ca.pem")
				if werr := os.WriteFile(tmp, pem, 0644); werr != nil {
					return werr
				}
				fmt.Printf("Docker's certs.d trust store is Linux/dockerd only.\n")
				fmt.Printf("Saved the instance CA to %s\n", tmp)
				fmt.Printf("On Docker Desktop add it to the host trust store, or point your client at it.\n")
				return nil
			}

			dir := filepath.Join("/etc/docker/certs.d", host)
			dest := filepath.Join(dir, "ca.crt")
			if err := os.MkdirAll(dir, 0755); err != nil {
				return trustPermissionHint(err, pem, dest)
			}
			if err := os.WriteFile(dest, pem, 0644); err != nil {
				return trustPermissionHint(err, pem, dest)
			}
			fmt.Printf("Installed instance CA to %s\n", dest)
			fmt.Printf("docker now trusts %s over its self-issued TLS, no daemon restart needed.\n", host)
			return nil
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "Registry host[:port] for the certs.d directory (defaults to the server host)")
	return cmd
}

// On permission errors stage a copy and print the sudo command
func trustPermissionHint(cause error, pem []byte, dest string) error {
	if !os.IsPermission(cause) {
		return cause
	}
	tmp := filepath.Join(os.TempDir(), "distroface-ca.pem")
	if werr := os.WriteFile(tmp, pem, 0644); werr != nil {
		return fmt.Errorf("permission denied writing %s, and could not stage a copy: %w", dest, werr)
	}
	fmt.Printf("Permission denied writing %s\n", dest)
	fmt.Printf("Staged the CA at %s, install it with:\n", tmp)
	fmt.Printf("  sudo mkdir -p %s && sudo cp %s %s\n", filepath.Dir(dest), tmp, dest)
	return nil
}

// ── Image commands ────────────────────────────────────────────────────────

type bearerTransport struct {
	tm   *TokenManager
	base http.RoundTripper
}

func (t *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if token := t.tm.GetToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return t.base.RoundTrip(req)
}

func (c *APIClient) rpcHTTPClient() *http.Client {
	return &http.Client{
		Timeout:   c.HTTPClient.Timeout,
		Transport: &bearerTransport{tm: c.TokenManager, base: http.DefaultTransport},
	}
}

func printProtoJSON(messages []proto.Message) error {
	marshaler := protojson.MarshalOptions{EmitUnpopulated: true}
	out := make([]json.RawMessage, len(messages))
	for i, m := range messages {
		raw, err := marshaler.Marshal(m)
		if err != nil {
			return err
		}
		out[i] = raw
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func newImageListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List image repositories",
		RunE: func(cmd *cobra.Command, args []string) error {
			if client.TokenManager.IsExpired() {
				if err := client.refreshToken(); err != nil {
					return err
				}
			}
			rpc := distrofacev1connect.NewRepositoryServiceClient(client.rpcHTTPClient(), client.BaseURL)
			resp, err := rpc.ListRepositories(context.Background(), connect.NewRequest(&distrofacev1.ListRepositoriesRequest{
				Page: &distrofacev1.PageRequest{PageSize: 100},
			}))
			if err != nil {
				return err
			}
			msgs := make([]proto.Message, len(resp.Msg.Repositories))
			for i, r := range resp.Msg.Repositories {
				msgs[i] = r
			}
			return printProtoJSON(msgs)
		},
	}
}

func newImageTagsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tags [namespace/image]",
		Short: "List tags for an image (name must include its namespace)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			namespace, name, ok := strings.Cut(args[0], "/")
			if !ok {
				return fmt.Errorf("image must be qualified as namespace/name (e.g. myorg/app)")
			}
			if client.TokenManager.IsExpired() {
				if err := client.refreshToken(); err != nil {
					return err
				}
			}
			rpc := distrofacev1connect.NewRepositoryServiceClient(client.rpcHTTPClient(), client.BaseURL)
			resp, err := rpc.ListTags(context.Background(), connect.NewRequest(&distrofacev1.ListTagsRequest{
				Namespace: namespace,
				Name:      name,
				Page:      &distrofacev1.PageRequest{PageSize: 100},
			}))
			if err != nil {
				return err
			}
			msgs := make([]proto.Message, len(resp.Msg.Tags))
			for i, t := range resp.Msg.Tags {
				msgs[i] = t
			}
			return printProtoJSON(msgs)
		},
	}
}

// ── Artifact operations ──────────────────────────────────────────────────

func (c *APIClient) createArtifactRepo(name, description string, private bool) error {
	payload := map[string]interface{}{
		"name":        name,
		"description": description,
		"private":     private,
	}
	resp, err := c.doRequest(http.MethodPost, "/api/v1/artifacts/repos", payload)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (c *APIClient) listArtifactRepos() ([]ArtifactRepository, error) {
	resp, err := c.doRequest(http.MethodGet, "/api/v1/artifacts/repos", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var repos []ArtifactRepository
	return repos, json.NewDecoder(resp.Body).Decode(&repos)
}

func (c *APIClient) uploadArtifact(repo, filePath, version, artifactPath string, properties map[string]string) error {
	resp, err := c.doRequest(http.MethodPost, fmt.Sprintf("/api/v1/artifacts/%s/upload", repo), nil)
	if err != nil {
		return err
	}
	resp.Body.Close()

	location := resp.Header.Get("Location")
	if location == "" {
		return fmt.Errorf("server did not return an upload location")
	}

	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	resp, err = c.doRequest(http.MethodPatch, location, file)
	if err != nil {
		return err
	}
	resp.Body.Close()

	uploadURL := fmt.Sprintf("%s?version=%s&path=%s", location, url.QueryEscape(version), url.QueryEscape(artifactPath))
	if properties == nil {
		properties = map[string]string{}
	}
	resp, err = c.doRequest(http.MethodPut, uploadURL, properties)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (c *APIClient) downloadArtifacts(repo string, q url.Values, outputPath string, unpack, flat bool, format string) error {
	endpoint := fmt.Sprintf("/api/v1/artifacts/%s/query", repo)
	if len(q) > 0 {
		endpoint += "?" + q.Encode()
	}

	resp, err := c.doRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	tempFile, err := os.CreateTemp("", "dfcli-download-*")
	if err != nil {
		return err
	}
	defer os.Remove(tempFile.Name())

	if _, err := io.Copy(tempFile, resp.Body); err != nil {
		return err
	}
	tempFile.Close()

	if err := os.MkdirAll(outputPath, 0755); err != nil {
		return err
	}

	if !unpack {
		finalPath := outputPath
		if info, err := os.Stat(outputPath); err == nil && info.IsDir() {
			finalPath = filepath.Join(outputPath, fmt.Sprintf("%s_artifacts.%s", repo, format))
		}
		return moveFile(tempFile.Name(), finalPath)
	}

	if err := recursivelyUnpack(tempFile.Name(), outputPath, flat); err != nil {
		return fmt.Errorf("failed to unpack archive: %w", err)
	}
	return nil
}

func (c *APIClient) deleteArtifact(repo, version, path string) error {
	resp, err := c.doRequest(http.MethodDelete, fmt.Sprintf("/api/v1/artifacts/%s/%s/%s", repo, version, path), nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (c *APIClient) searchArtifacts(q url.Values) (*SearchResponse, error) {
	endpoint := "/api/v1/artifacts/search"
	if len(q) > 0 {
		endpoint += "?" + q.Encode()
	}
	resp, err := c.doRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		// V1 behavior, search errors degrade to empty results
		if viper.GetBool("debug") {
			fmt.Fprintf(os.Stderr, "search error: %v\n", err)
		}
		return &SearchResponse{Results: []Artifact{}}, nil
	}
	defer resp.Body.Close()

	var search SearchResponse
	return &search, json.NewDecoder(resp.Body).Decode(&search)
}

// ── Artifact commands ────────────────────────────────────────────────────

func newArtifactRepoCreateCmd() *cobra.Command {
	var description string
	var private bool

	cmd := &cobra.Command{
		Use:   "create [name]",
		Short: "Create a new artifact repository",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return client.createArtifactRepo(args[0], description, private)
		},
	}

	cmd.Flags().StringVarP(&description, "description", "d", "", "Repository description")
	cmd.Flags().BoolVarP(&private, "private", "p", false, "Make repository private")
	return cmd
}

func newArtifactRepoListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List artifact repositories",
		RunE: func(cmd *cobra.Command, args []string) error {
			repos, err := client.listArtifactRepos()
			if err != nil {
				return err
			}
			return printJSON(repos)
		},
	}
}

func newArtifactUploadCmd() *cobra.Command {
	var version, path string
	var properties map[string]string

	cmd := &cobra.Command{
		Use:   "upload [repo] [file]",
		Short: "Upload an artifact",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo := args[0]
			file := args[1]

			if version == "" {
				version = sanitizeVersion(filepath.Base(file))
			} else {
				version = sanitizeVersion(version)
			}
			if path == "" {
				path = sanitizeFilePath(filepath.Base(file))
			} else {
				path = sanitizeFilePath(path)
			}

			fmt.Printf("Uploading %s to %s (version: %s, path: %s)\n", file, repo, version, path)
			if err := client.uploadArtifact(repo, file, version, path, properties); err != nil {
				return fmt.Errorf("upload failed: %v", err)
			}
			fmt.Println("Upload successful")
			return nil
		},
	}

	cmd.Flags().StringVarP(&version, "version", "v", "", "Artifact version")
	cmd.Flags().StringVarP(&path, "path", "p", "", "Artifact path in repository")
	cmd.Flags().StringToStringVar(&properties, "property", nil, "Properties (key=value,key=value,...)")
	return cmd
}

func newArtifactDownloadCmd() *cobra.Command {
	var (
		version string
		artPath string
		output  string
		props   map[string]string
		num     int
		sortBy  string
		order   string
		format  string
		unpack  bool
		flat    bool
	)

	cmd := &cobra.Command{
		Use:   "download [repo]",
		Short: "Download artifacts via query",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo := args[0]

			q := make(url.Values)
			if version != "" {
				q.Set("version", version)
			}
			if artPath != "" {
				q.Set("path", artPath)
			}
			if num > 0 {
				q.Set("num", strconv.Itoa(num))
			}
			if sortBy != "" {
				q.Set("sort", sortBy)
			}
			if order != "" {
				q.Set("order", order)
			}
			if format != "" {
				q.Set("format", format)
			}
			if flat {
				q.Set("flat", "1")
			}
			for key, value := range props {
				q.Set(key, value)
			}

			if output == "" {
				output = "."
			}
			return client.downloadArtifacts(repo, q, output, unpack, flat, format)
		},
	}

	cmd.Flags().StringVarP(&version, "version", "v", "", "Artifact version filter")
	cmd.Flags().StringVarP(&artPath, "path", "p", "", "Path inside artifact version")
	cmd.Flags().StringToStringVar(&props, "property", nil, "Properties (key=value)")
	cmd.Flags().StringVarP(&output, "output", "o", "", "Output path (file or directory)")
	cmd.Flags().IntVar(&num, "num", 1, "Number of matching artifacts")
	cmd.Flags().StringVar(&sortBy, "sort", "", "Sort field")
	cmd.Flags().StringVar(&order, "order", "", "Sort order (ASC/DESC)")
	cmd.Flags().StringVar(&format, "format", "zip", "Archive format (zip/tar.gz)")
	cmd.Flags().BoolVar(&unpack, "unpack", false, "Unpack downloaded archives")
	cmd.Flags().BoolVar(&flat, "flat", false, "Flatten directory structure")
	return cmd
}

func newArtifactDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete [repo] [version] [path]",
		Short: "Delete an artifact",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := client.deleteArtifact(args[0], args[1], args[2]); err != nil {
				return fmt.Errorf("failed to delete artifact: %v", err)
			}
			fmt.Println("Artifact deleted successfully")
			return nil
		},
	}
}

func newArtifactSearchCmd() *cobra.Command {
	var (
		repo    string
		version string
		artPath string
		props   map[string]string
		num     int
		offset  int
		sortBy  string
		order   string
		table   bool
	)

	cmd := &cobra.Command{
		Use:   "search",
		Short: "Search for artifacts (via query)",
		RunE: func(cmd *cobra.Command, args []string) error {
			q := make(url.Values)
			if repo != "" {
				q.Set("repo", repo)
			}
			if version != "" {
				q.Set("version", version)
			}
			if artPath != "" {
				q.Set("path", artPath)
			}
			if num > 0 {
				q.Set("num", strconv.Itoa(num))
			}
			if offset > 0 {
				q.Set("offset", strconv.Itoa(offset))
			}
			if sortBy != "" {
				q.Set("sort", sortBy)
			}
			if order != "" {
				q.Set("order", order)
			}
			for key, value := range props {
				q.Set(key, value)
			}

			search, err := client.searchArtifacts(q)
			if err != nil {
				return fmt.Errorf("search failed: %v", err)
			}

			if table {
				w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
				fmt.Fprintln(w, "Total Matches:", search.Total)
				fmt.Fprintln(w, "\nREPOSITORY\tNAME\tVERSION\tSIZE\tUPDATED")
				for _, a := range search.Results {
					fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\n",
						a.RepoID, a.Name, a.Version, formatSize(a.Size), a.UpdatedAt.Format(time.RFC3339))
				}
				return w.Flush()
			}
			return printJSON(search)
		},
	}

	cmd.Flags().StringVarP(&repo, "repo", "r", "", "Artifact repository name")
	cmd.Flags().StringVarP(&version, "version", "v", "", "Artifact version filter")
	cmd.Flags().StringVarP(&artPath, "path", "p", "", "Path inside artifact version")
	cmd.Flags().StringToStringVar(&props, "property", nil, "Properties (key=value,key=value,...)")
	cmd.Flags().IntVar(&num, "num", 0, "Max number of matching artifacts to retrieve (default 1)")
	cmd.Flags().IntVar(&offset, "offset", 0, "Result offset for pagination")
	cmd.Flags().StringVar(&sortBy, "sort", "", "Sort field (default created_at)")
	cmd.Flags().StringVar(&order, "order", "", "Sort order (ASC or DESC)")
	cmd.Flags().BoolVarP(&table, "table", "t", false, "Format results as a table")
	return cmd
}

// ── Helpers ──────────────────────────────────────────────────────────────

func printJSON(v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func formatSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(size)/float64(div), "KMGTPE"[exp])
}

var (
	nonWordPattern    = regexp.MustCompile(`[^\w\-\.]`)
	multiUnderscore   = regexp.MustCompile(`_+`)
	multiSlashPattern = regexp.MustCompile(`/+`)
)

func sanitizeVersion(version string) string {
	return strings.TrimSpace(nonWordPattern.ReplaceAllString(version, "_"))
}

func sanitizeFilePath(path string) string {
	path = filepath.ToSlash(path)
	path = strings.TrimSpace(path)
	path = multiSlashPattern.ReplaceAllString(path, "/")
	path = strings.Trim(path, "/")

	parts := strings.Split(path, "/")
	for i, part := range parts {
		part = nonWordPattern.ReplaceAllString(part, "_")
		part = multiUnderscore.ReplaceAllString(part, "_")
		parts[i] = part
	}
	return strings.Join(parts, "/")
}

// Rename with copy fallback for cross device moves
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Remove(src)
}

// ── Archive unpacking ────────────────────────────────────────────────────

func unpackZip(zipPath, destPath string, flat bool) error {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer reader.Close()

	for _, f := range reader.File {
		var targetPath string
		if flat {
			targetPath = filepath.Join(destPath, filepath.Base(f.Name))
		} else {
			targetPath = filepath.Join(destPath, f.Name)
		}
		if !strings.HasPrefix(filepath.Clean(targetPath), filepath.Clean(destPath)) {
			continue // Zip slip guard
		}

		if f.FileInfo().IsDir() {
			if !flat {
				_ = os.MkdirAll(targetPath, 0755)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return err
		}
		outFile, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			outFile.Close()
			return err
		}
		_, err = io.Copy(outFile, rc)
		rc.Close()
		outFile.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func unpackTarGz(tarPath, destPath string, flat bool) error {
	file, err := os.Open(tarPath)
	if err != nil {
		return err
	}
	defer file.Close()

	gzr, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		var targetPath string
		if flat {
			targetPath = filepath.Join(destPath, filepath.Base(header.Name))
		} else {
			targetPath = filepath.Join(destPath, header.Name)
		}
		if !strings.HasPrefix(filepath.Clean(targetPath), filepath.Clean(destPath)) {
			continue // Tar slip guard
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if !flat {
				if err := os.MkdirAll(targetPath, 0755); err != nil {
					return err
				}
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return err
			}
			outFile, err := os.Create(targetPath)
			if err != nil {
				return err
			}
			if _, err := io.Copy(outFile, tr); err != nil {
				outFile.Close()
				return err
			}
			outFile.Close()
		}
	}
	return nil
}

func isZipMagic(magic []byte) bool {
	return len(magic) >= 4 && magic[0] == 0x50 && magic[1] == 0x4B && magic[2] == 0x03 && magic[3] == 0x04
}

func isGzipMagic(magic []byte) bool {
	return len(magic) >= 2 && magic[0] == 0x1F && magic[1] == 0x8B
}

func readMagic(path string) []byte {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	magic := make([]byte, 4)
	n, _ := io.ReadFull(file, magic)
	return magic[:n]
}

// Recursive unpacks nested archives in place
func recursivelyUnpack(archivePath, destPath string, flat bool) error {
	magic := readMagic(archivePath)
	switch {
	case isZipMagic(magic):
		if err := unpackZip(archivePath, destPath, flat); err != nil {
			return err
		}
	case isGzipMagic(magic):
		if err := unpackTarGz(archivePath, destPath, flat); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported archive format")
	}

	var files []string
	err := filepath.Walk(destPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return err
	}

	for _, path := range files {
		magic := readMagic(path)
		if !isZipMagic(magic) && !isGzipMagic(magic) {
			continue
		}

		tempDir, err := os.MkdirTemp("", "dfcli-nested-*")
		if err != nil {
			continue
		}
		if err := recursivelyUnpack(path, tempDir, flat); err != nil {
			os.RemoveAll(tempDir)
			continue
		}

		err = filepath.Walk(tempDir, func(srcPath string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			var targetPath string
			if flat {
				targetPath = filepath.Join(destPath, filepath.Base(srcPath))
			} else {
				rel, _ := filepath.Rel(tempDir, srcPath)
				targetPath = filepath.Join(filepath.Dir(path), rel)
			}
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return err
			}
			return moveFile(srcPath, targetPath)
		})
		os.RemoveAll(tempDir)
		if err != nil {
			continue
		}
		os.Remove(path)
	}
	return nil
}

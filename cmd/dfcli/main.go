package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/nickheyer/distroface/internal/models"
	"github.com/nickheyer/distroface/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/term"
)

type TokenManager struct {
	mu        sync.RWMutex
	token     string
	expiresAt time.Time
}

func NewTokenManager(token string, expiresAt time.Time) *TokenManager {
	return &TokenManager{
		token:     token,
		expiresAt: expiresAt,
	}
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

func (tm *TokenManager) IsExpired() bool {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return time.Now().After(tm.expiresAt)
}

type APIClient struct {
	BaseURL      string
	TokenManager *TokenManager
	HTTPClient   *http.Client
}

const (
	defaultConfigFile = "~/.dfcli/config.json"
	defaultServerURL  = "http://localhost:8668"
)

var (
	cfgFile string
	client  *APIClient
)

// INIT
func main() {
	cobra.OnInitialize(initConfig)

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

	// SET DEFAULTS BEFORE INIT
	viper.SetDefault("server", defaultServerURL)
	viper.SetDefault("timeout", "5m")

	// INIT CONFIG AFTER DEFAULTS
	cobra.OnInitialize(initConfig)

	// GLOBAL FLAGS
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is ~/.dfcli/config.json)")
	rootCmd.PersistentFlags().String("server", defaultServerURL, "DistroFace server URL")
	rootCmd.PersistentFlags().String("timeout", "5m", "Request timeout (30s, 5m, 1h, etc.)")

	// BIND FLAGS TO VIPER
	viper.BindPFlag("server", rootCmd.PersistentFlags().Lookup("server"))
	viper.BindPFlag("timeout", rootCmd.PersistentFlags().Lookup("timeout"))

	// AUTH COMMANDS
	rootCmd.AddCommand(newLoginCmd())
	rootCmd.AddCommand(newLogoutCmd())

	// IMAGE COMMANDS
	imageCmd := &cobra.Command{
		Use:   "image",
		Short: "Manage container images",
	}
	imageCmd.AddCommand(
		newImageListCmd(),
		newImageTagsCmd(),
		newImageDeleteCmd(),
		newImageVisibilityCmd(),
	)
	rootCmd.AddCommand(imageCmd)

	// ARTIFACT COMMANDS
	artifactCmd := &cobra.Command{
		Use:   "artifact",
		Short: "Manage artifacts",
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

	// USER COMMANDS
	userCmd := &cobra.Command{
		Use:   "user",
		Short: "Manage users",
	}
	userCmd.AddCommand(
		newUserListCmd(),
		newUserCreateCmd(),
		newUserDeleteCmd(),
		newUserGroupsCmd(),
	)
	rootCmd.AddCommand(userCmd)

	// GROUP COMMANDS
	groupCmd := &cobra.Command{
		Use:   "group",
		Short: "Manage groups",
	}
	groupCmd.AddCommand(
		newGroupListCmd(),
		newGroupCreateCmd(),
		newGroupUpdateCmd(),
		newGroupDeleteCmd(),
	)
	rootCmd.AddCommand(groupCmd)

	// ROLE COMMANDS
	roleCmd := &cobra.Command{
		Use:   "role",
		Short: "Manage roles",
	}
	roleCmd.AddCommand(
		newRoleListCmd(),
		newRoleUpdateCmd(),
		newRoleDeleteCmd(),
	)
	rootCmd.AddCommand(roleCmd)

	// SETTINGS COMMANDS
	settingsCmd := &cobra.Command{
		Use:   "settings",
		Short: "Manage settings",
	}
	settingsCmd.AddCommand(
		newSettingsGetCmd(),
		newSettingsUpdateCmd(),
		newSettingsResetCmd(),
	)
	rootCmd.AddCommand(settingsCmd)

	// VERSION COMMAND
	rootCmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("dfcli version 1.0.0")
		},
	})

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

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

	// THIS OCCASIONALLY FAILS TO UNMARSHALL JSON CONFIG IN PARALLEL USAGE
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if err := viper.ReadInConfig(); err != nil {
			if _, ok := err.(viper.ConfigFileNotFoundError); ok {
				return
			}
			lastErr = err
			time.Sleep(time.Duration(100) * time.Millisecond)
			continue
		}
		return
	}

	// EXIT IF ALL RETRIES FAILED
	if lastErr != nil {
		fmt.Fprintf(os.Stderr, "Failed to read config file after %d attempts: %v\n", 3, lastErr)
		os.Exit(1)
	}
}

func initClient() error {
	var config models.AuthConfig
	data, err := os.ReadFile(getConfigPath())
	if err == nil {
		if err := json.Unmarshal(data, &config); err != nil {
			return fmt.Errorf("failed to parse config: %v", err)
		}
	}

	// GET SERVER URL FROM VIPER
	serverURL := viper.GetString("server")

	// FORCE FORMATTING OF SERVER URL
	if !strings.HasPrefix(serverURL, "http://") && !strings.HasPrefix(serverURL, "https://") {
		serverURL = "http://" + serverURL
	}

	clientTimeout := viper.GetDuration("timeout")
	if clientTimeout == 0 {
		clientTimeout = 5 * time.Minute
	}

	client = &APIClient{
		BaseURL:      serverURL,
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
	return filepath.Join(getHomeDir(), ".dfcli", "config.json")
}

// HELPERS
func (c *APIClient) refreshToken() error {
	req := struct {
		RefreshToken string `json:"refresh_token"`
	}{
		RefreshToken: c.TokenManager.GetToken(), // We use the same token for refresh
	}

	jsonData, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal refresh request: %v", err)
	}

	resp, err := c.HTTPClient.Post(c.BaseURL+"/api/v1/auth/refresh", "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("refresh request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("refresh failed with status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read refresh response: %v", err)
	}

	var result struct {
		Token     string    `json:"token"`
		ExpiresIn int       `json:"expires_in"`
		IssuedAt  time.Time `json:"issued_at"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("failed to parse refresh response: %v", err)
	}

	if result.Token == "" {
		return fmt.Errorf("no token in refresh response")
	}

	c.TokenManager.mu.Lock()
	c.TokenManager.token = result.Token
	c.TokenManager.expiresAt = time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)
	c.TokenManager.mu.Unlock()

	return saveConfig(models.AuthConfig{
		Token:     result.Token,
		ExpiresAt: c.TokenManager.expiresAt,
		Username:  "", // Will be preserved from existing config
		Server:    c.BaseURL,
	})
}

func (c *APIClient) doRequest(method, path string, body interface{}) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		var bodyBytes []byte
		var err error
		switch v := body.(type) {
		case io.Reader:
			bodyReader = v
		default:
			bodyBytes, err = json.Marshal(body)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal request body: %w", err)
			}
			bodyReader = bytes.NewBuffer(bodyBytes)
		}
	}

	// HANDLE TOKEN AND RETRIES
	var resp *http.Response
	for retries := 0; retries < 2; retries++ {
		req, err := http.NewRequest(method, c.BaseURL+path, bodyReader)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		if token := c.TokenManager.GetToken(); token != "" {
			if retries == 0 && c.TokenManager.IsExpired() {
				if err := c.refreshToken(); err != nil {
					return nil, fmt.Errorf("token refresh failed: %w", err)
				}
				token = c.TokenManager.GetToken()
			}
			req.Header.Set("Authorization", "Bearer "+token)
		}

		if body != nil && method != "PATCH" {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err = c.HTTPClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("request failed: %w", err)
		}

		// IF NOT UNAUTHORIZED OR SECOND TRY, BREAK
		if resp.StatusCode != http.StatusUnauthorized || retries > 0 {
			break
		}

		// ON FIRST 401, TRY REFRESH AND RETRY
		resp.Body.Close()
		if err := c.refreshToken(); err != nil {
			return nil, fmt.Errorf("token refresh failed: %w", err)
		}
	}

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}

	return resp, nil
}

func printJSON(v interface{}) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func formatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func saveConfig(config models.AuthConfig) error {
	configDir := filepath.Join(getHomeDir(), ".dfcli")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %v", err)
	}

	configPath := filepath.Join(configDir, "config.json")
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %v", err)
	}

	if err := os.WriteFile(configPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write config file: %v", err)
	}

	return nil
}

// IMAGE OPERATIONS
func (c *APIClient) listImages() ([]models.ImageRepository, error) {
	resp, err := c.doRequest("GET", "/api/v1/repositories", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var repos []models.ImageRepository
	return repos, json.NewDecoder(resp.Body).Decode(&repos)
}

func (c *APIClient) listImageTags(name string) ([]models.ImageTag, error) {
	resp, err := c.doRequest("GET", fmt.Sprintf("/v2/%s/tags/list", name), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var tags []models.ImageTag
	return tags, json.NewDecoder(resp.Body).Decode(&tags)
}

func (c *APIClient) deleteImageTag(name, tag string) error {
	_, err := c.doRequest("DELETE", fmt.Sprintf("/api/v1/repositories/%s/tags/%s", name, tag), nil)
	return err
}

func (c *APIClient) updateImageVisibility(name string, private bool) error {
	payload := map[string]interface{}{
		"private": private,
	}
	_, err := c.doRequest("POST", fmt.Sprintf("/api/v1/repositories/%s/visibility", name), payload)
	return err
}

// ARTIFACT OPERATIONS
func (c *APIClient) createArtifactRepo(name string, description string, private bool) error {
	payload := map[string]interface{}{
		"name":        name,
		"description": description,
		"private":     private,
	}
	_, err := c.doRequest("POST", "/api/v1/artifacts/repos", payload)
	return err
}

func (c *APIClient) listArtifactRepos() ([]models.ArtifactRepository, error) {
	resp, err := c.doRequest("GET", "/api/v1/artifacts/repos", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var repos []models.ArtifactRepository
	return repos, json.NewDecoder(resp.Body).Decode(&repos)
}

func (c *APIClient) uploadArtifact(repo, filePath, version, artifactPath string, properties map[string]string) error {
	// 1. INIT UPLOAD
	resp, err := c.doRequest("POST", fmt.Sprintf("/api/v1/artifacts/%s/upload", repo), nil)
	if err != nil {
		return err
	}

	location := resp.Header.Get("Location")

	// 2. READ AND UPLOAD FILE
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	// 3. UPLOAD CHUNKS
	_, err = c.doRequest("PATCH", location, file)
	if err != nil {
		return err
	}

	// 4. COMPLETE UPLOAD
	uploadURL := fmt.Sprintf("%s?version=%s&path=%s",
		location, version, artifactPath)
	_, err = c.doRequest("PUT", uploadURL, properties)
	return err
}

func (c *APIClient) downloadArtifacts(repo string, q url.Values, outputPath string, unpack bool, flat bool, format string) error {
	endpoint := fmt.Sprintf("/api/v1/artifacts/%s/query", repo)
	if len(q) > 0 {
		endpoint = endpoint + "?" + q.Encode()
	}

	resp, err := c.doRequest("GET", endpoint, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// CREATE TEMP FILE FOR DOWNLOAD
	tempFile, err := os.CreateTemp("", "dfcli-download-*")
	if err != nil {
		return err
	}
	defer os.Remove(tempFile.Name())

	if _, err := io.Copy(tempFile, resp.Body); err != nil {
		return err
	}
	tempFile.Close()

	// ENSURE OUTPUT DIR EXISTS
	if err := os.MkdirAll(outputPath, 0755); err != nil {
		return err
	}

	if !unpack {
		// JUST MOVE TO FINAL LOCATION IF NOT UNPACKING
		finalPath := outputPath
		if info, err := os.Stat(outputPath); err == nil && info.IsDir() {
			finalPath = filepath.Join(outputPath, fmt.Sprintf("%s_artifacts.%s", repo, format))
		}
		return os.Rename(tempFile.Name(), finalPath)
	}

	// UNPACK ARCHIVE AND HANDLE NESTED ARCHIVES
	if err := recursivelyUnpack(tempFile.Name(), outputPath, flat); err != nil {
		return fmt.Errorf("failed to unpack archive: %w", err)
	}

	return nil
}

func (c *APIClient) deleteArtifact(repo, version, path string) error {
	_, err := c.doRequest("DELETE", fmt.Sprintf("/api/v1/artifacts/%s/%s/%s", repo, version, path), nil)
	return err
}

func (c *APIClient) searchArtifacts(q url.Values) (*models.SearchResponse, error) {
	endpoint := "/api/v1/artifacts/search"
	if len(q) > 0 {
		endpoint = endpoint + "?" + q.Encode()
	}
	var search *models.SearchResponse
	resp, err := c.doRequest("GET", endpoint, nil)
	if err != nil {
		return &models.SearchResponse{
			Results: []models.Artifact{},
			Total:   0,
		}, nil
	}
	defer resp.Body.Close()
	return search, json.NewDecoder(resp.Body).Decode(&search)
}

// USER OPERATIONS
func (c *APIClient) listUsers() ([]models.User, error) {
	resp, err := c.doRequest("GET", "/api/v1/users", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var users []models.User
	return users, json.NewDecoder(resp.Body).Decode(&users)
}

func (c *APIClient) createUser(username, password string, groups []string) error {
	payload := map[string]interface{}{
		"username": username,
		"password": password,
		"groups":   groups,
	}
	_, err := c.doRequest("POST", "/api/v1/users", payload)
	return err
}

func (c *APIClient) deleteUser(username string) error {
	_, err := c.doRequest("DELETE", fmt.Sprintf("/api/v1/users/%s", username), nil)
	return err
}

func (c *APIClient) updateUserGroups(username string, groups []string) error {
	payload := map[string]interface{}{
		"username": username,
		"groups":   groups,
	}
	_, err := c.doRequest("PUT", "/api/v1/users/groups", payload)
	return err
}

// GROUP OPERATIONS
func (c *APIClient) listGroups() ([]models.Group, error) {
	resp, err := c.doRequest("GET", "/api/v1/groups", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var groups []models.Group
	return groups, json.NewDecoder(resp.Body).Decode(&groups)
}

func (c *APIClient) createGroup(name, description string, roles []string) error {
	payload := map[string]interface{}{
		"name":        name,
		"description": description,
		"roles":       roles,
	}
	_, err := c.doRequest("POST", "/api/v1/groups", payload)
	return err
}

func (c *APIClient) updateGroup(name string, description string, roles []string) error {
	payload := map[string]interface{}{
		"description": description,
		"roles":       roles,
	}
	_, err := c.doRequest("PUT", fmt.Sprintf("/api/v1/groups/%s", name), payload)
	return err
}

func (c *APIClient) deleteGroup(name string) error {
	_, err := c.doRequest("DELETE", fmt.Sprintf("/api/v1/groups/%s", name), nil)
	return err
}

// ROLE OPERATIONS
func (c *APIClient) listRoles() ([]models.Role, error) {
	resp, err := c.doRequest("GET", "/api/v1/roles", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var roles []models.Role
	return roles, json.NewDecoder(resp.Body).Decode(&roles)
}

func (c *APIClient) updateRole(name string, description string, permissions []models.Permission) error {
	payload := map[string]interface{}{
		"description": description,
		"permissions": permissions,
	}
	_, err := c.doRequest("PUT", fmt.Sprintf("/api/v1/roles/%s", name), payload)
	return err
}

func (c *APIClient) deleteRole(name string) error {
	_, err := c.doRequest("DELETE", fmt.Sprintf("/api/v1/roles/%s", name), nil)
	return err
}

// SETTINGS OPERATIONS
func (c *APIClient) getSettings(section string) (map[string]interface{}, error) {
	resp, err := c.doRequest("GET", fmt.Sprintf("/api/v1/settings/%s", section), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var settings map[string]interface{}
	return settings, json.NewDecoder(resp.Body).Decode(&settings)
}

func (c *APIClient) updateSettings(section string, settings map[string]interface{}) error {
	_, err := c.doRequest("PUT", fmt.Sprintf("/api/v1/settings/%s", section), settings)
	return err
}

func (c *APIClient) resetSettings(section string) error {
	_, err := c.doRequest("POST", fmt.Sprintf("/api/v1/settings/%s/reset", section), nil)
	return err
}

// USER AUTH OPERATION(S)
func login(server, username, password string) (string, time.Time, error) {
	client := &http.Client{Timeout: 30 * time.Second}

	payload := map[string]string{
		"username": username,
		"password": password,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", time.Time{}, err
	}

	resolvedServer := fmt.Sprintf("%s/api/v1/auth/login", server)
	req, err := http.NewRequest("POST", resolvedServer, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", time.Time{}, err
	}

	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
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

	var result struct {
		Token     string    `json:"token"`
		ExpiresIn int       `json:"expires_in"`
		IssuedAt  time.Time `json:"issued_at"`
		Username  string    `json:"username"`
		Groups    []string  `json:"groups"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return "", time.Time{}, fmt.Errorf("failed to parse response: %v", err)
	}

	if result.Token == "" {
		return "", time.Time{}, fmt.Errorf("no token in response")
	}

	expiresAt := time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)
	return result.Token, expiresAt, nil
}

// CONSTRUCTORS
func newLoginCmd() *cobra.Command {
	var username, password string

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in to DistroFace server",
		RunE: func(cmd *cobra.Command, args []string) error {
			// PROMPT FOR USERNAME IF NOT PROVIDED
			if username == "" {
				fmt.Print("Username: ")
				fmt.Scanln(&username)
			}

			// PROMPT FOR PASSWORD IF NOT PROVIDED
			if password == "" {
				fmt.Print("Password: ")
				bytePassword, err := term.ReadPassword(int(syscall.Stdin))
				if err != nil {
					return fmt.Errorf("failed to read password: %v", err)
				}
				password = string(bytePassword)
				fmt.Println()
			}

			server := viper.GetString("server")
			token, expiry, err := login(server, username, password)
			if err != nil {
				return fmt.Errorf("login failed: %v", err)
			}

			config := models.AuthConfig{
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

	// MAKE FLAGS OPTIONAL SINCE WE'LL PROMPT IF THEY'RE NOT PROVIDED
	cmd.Flags().StringVarP(&username, "username", "u", "", "Username (optional, will prompt if not provided)")
	cmd.Flags().StringVarP(&password, "password", "p", "", "Password (optional, will prompt if not provided)")

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

func newImageTagsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tags [image]",
		Short: "List tags for an image",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tags, err := client.listImageTags(args[0])
			if err != nil {
				return err
			}
			return printJSON(tags)
		},
	}
}

func newImageDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete [image] [tag]",
		Short: "Delete an image tag",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return client.deleteImageTag(args[0], args[1])
		},
	}
}

func newImageVisibilityCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "visibility [image] [public|private]",
		Short: "Update image visibility",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return client.updateImageVisibility(args[0], args[1] == "private")
		},
	}
}

func newImageListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List container images",
		RunE: func(cmd *cobra.Command, args []string) error {
			repos, err := client.listImages()
			if err != nil {
				return fmt.Errorf("failed to list images: %v", err)
			}

			// FORMAT OUTPUT AS A TABLE
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tTAGS\tSIZE\tPRIVATE\tOWNER")
			for _, repo := range repos {
				tags := len(repo.Tags)
				fmt.Fprintf(w, "%s\t%d\t%s\t%v\t%s\n",
					repo.Name,
					tags,
					formatSize(repo.Size),
					repo.Private,
					repo.Owner,
				)
			}
			return w.Flush()
		},
	}
}

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

			// BUILD QUERY
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

			// ADD PROPS
			for key, value := range props {
				q.Set(key, value)
			}

			// HANDLE OUTPUT PATH
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

			// ADD PROPS
			for key, value := range props {
				q.Set(key, value)
			}

			search, err := client.searchArtifacts(q)
			if table {
				if err != nil {
					return fmt.Errorf("search failed: %v", err)
				}

				// FORMAT ARTIFACTS AS A TABLE (IF TABLE FLAG)
				artifacts := search.Results
				w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
				fmt.Fprintln(w, "Total Matches:", search.Total)
				fmt.Fprintln(w, "\nREPOSITORY\tNAME\tVERSION\tSIZE\tUPDATED")
				for _, a := range artifacts {
					fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\n",
						a.RepoID,
						a.Name,
						a.Version,
						formatSize(a.Size),
						a.UpdatedAt.Format(time.RFC3339),
					)
				}
				return w.Flush()
			} else {
				// FORMAT AS JSON BY DEFAULT
				return printJSON(search)
			}
		},
	}
	cmd.Flags().StringVarP(&repo, "repo", "r", "", "Artifact repository name")
	cmd.Flags().StringVarP(&version, "version", "v", "", "Artifact version filter")
	cmd.Flags().StringVarP(&artPath, "path", "p", "", "Path inside artifact version")
	cmd.Flags().StringToStringVar(&props, "property", nil, "Properties (key=value,key=value,...)")
	cmd.Flags().IntVar(&num, "num", 0, "Max number of matching artifacts to retrieve (default 1)")
	cmd.Flags().StringVar(&sortBy, "sort", "", "Sort field (default created_at)")
	cmd.Flags().StringVar(&order, "order", "", "Sort order (ASC or DESC)")
	cmd.Flags().BoolVarP(&table, "table", "t", false, "Format results as a table")

	return cmd
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

			// IF NO VERSION PROVIDED, USE SANITIZED FILENAME
			if version == "" {
				version = utils.SanitizeVersion(filepath.Base(file))
			} else {
				version = utils.SanitizeVersion(version)
			}

			// IF NO PATH PROVIDED, USE SANITIZED FILENAME
			if path == "" {
				path = utils.SanitizeFilePath(filepath.Base(file))
			} else {
				path = utils.SanitizeFilePath(path)
			}

			fmt.Printf("Uploading %s to %s (version: %s, path: %s)\n",
				file, repo, version, path)

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

func newArtifactDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete [repo] [version] [path]",
		Short: "Delete an artifact",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo := args[0]
			version := args[1]
			path := args[2]

			if err := client.deleteArtifact(repo, version, path); err != nil {
				return fmt.Errorf("failed to delete artifact: %v", err)
			}

			fmt.Println("Artifact deleted successfully")
			return nil
		},
	}
}

func newUserListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List users",
		RunE: func(cmd *cobra.Command, args []string) error {
			users, err := client.listUsers()
			if err != nil {
				return err
			}
			return printJSON(users)
		},
	}
}

func newUserCreateCmd() *cobra.Command {
	var password string
	var groups []string

	cmd := &cobra.Command{
		Use:   "create [username]",
		Short: "Create a new user",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return client.createUser(args[0], password, groups)
		},
	}

	cmd.Flags().StringVarP(&password, "password", "p", "", "User password")
	cmd.Flags().StringSliceVarP(&groups, "groups", "g", nil, "User groups")
	cmd.MarkFlagRequired("password")

	return cmd
}

func newUserDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete [username]",
		Short: "Delete a user",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return client.deleteUser(args[0])
		},
	}
}

func newUserGroupsCmd() *cobra.Command {
	var groups []string

	cmd := &cobra.Command{
		Use:   "groups [username]",
		Short: "Update user groups",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return client.updateUserGroups(args[0], groups)
		},
	}

	cmd.Flags().StringSliceVarP(&groups, "groups", "g", nil, "User groups")
	cmd.MarkFlagRequired("groups")

	return cmd
}

func newGroupListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List groups",
		RunE: func(cmd *cobra.Command, args []string) error {
			groups, err := client.listGroups()
			if err != nil {
				return err
			}
			return printJSON(groups)
		},
	}
}

func newGroupCreateCmd() *cobra.Command {
	var description string
	var roles []string

	cmd := &cobra.Command{
		Use:   "create [name]",
		Short: "Create a new group",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return client.createGroup(args[0], description, roles)
		},
	}

	cmd.Flags().StringVarP(&description, "description", "d", "", "Group description")
	cmd.Flags().StringSliceVarP(&roles, "roles", "r", nil, "Group roles")
	cmd.MarkFlagRequired("roles")

	return cmd
}

func newGroupUpdateCmd() *cobra.Command {
	var description string
	var roles []string

	cmd := &cobra.Command{
		Use:   "update [name]",
		Short: "Update a group",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return client.updateGroup(args[0], description, roles)
		},
	}

	cmd.Flags().StringVarP(&description, "description", "d", "", "Group description")
	cmd.Flags().StringSliceVarP(&roles, "roles", "r", nil, "Group roles")

	return cmd
}

func newGroupDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete [name]",
		Short: "Delete a group",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return client.deleteGroup(args[0])
		},
	}
}

func newRoleListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List roles",
		RunE: func(cmd *cobra.Command, args []string) error {
			roles, err := client.listRoles()
			if err != nil {
				return err
			}
			return printJSON(roles)
		},
	}
}

func newRoleUpdateCmd() *cobra.Command {
	var description string
	var addPerms, removePerms []string

	cmd := &cobra.Command{
		Use:   "update [name]",
		Short: "Update a role",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// GET CURRENT ROLE FIRST
			roles, err := client.listRoles()
			if err != nil {
				return err
			}

			var currentRole *models.Role
			for _, r := range roles {
				if r.Name == args[0] {
					currentRole = &r
					break
				}
			}

			if currentRole == nil {
				return fmt.Errorf("role not found: %s", args[0])
			}

			// UPDATE PERMISSIONS
			permissions := currentRole.Permissions

			// ADD NEW PERMISSIONS
			for _, p := range addPerms {
				parts := strings.Split(p, ":")
				if len(parts) != 2 {
					return fmt.Errorf("invalid permission format: %s", p)
				}

				permissions = append(permissions, models.Permission{
					Action:   models.Action(parts[0]),
					Resource: models.Resource(parts[1]),
				})
			}

			// REMOVE PERMISSIONS
			if len(removePerms) > 0 {
				var filtered []models.Permission
				for _, p := range permissions {
					remove := false
					pStr := fmt.Sprintf("%s:%s", p.Action, p.Resource)
					for _, rp := range removePerms {
						if pStr == rp {
							remove = true
							break
						}
					}
					if !remove {
						filtered = append(filtered, p)
					}
				}
				permissions = filtered
			}

			return client.updateRole(args[0], description, permissions)
		},
	}

	cmd.Flags().StringVarP(&description, "description", "d", "", "Role description")
	cmd.Flags().StringSliceVar(&addPerms, "add", nil, "Add permissions (ACTION:RESOURCE)")
	cmd.Flags().StringSliceVar(&removePerms, "remove", nil, "Remove permissions (ACTION:RESOURCE)")

	return cmd
}

func newRoleDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete [name]",
		Short: "Delete a role",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return client.deleteRole(args[0])
		},
	}
}

func newSettingsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get [section]",
		Short: "Get settings for a section",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			settings, err := client.getSettings(args[0])
			if err != nil {
				return err
			}
			return printJSON(settings)
		},
	}
}

func newSettingsUpdateCmd() *cobra.Command {
	var values []string

	cmd := &cobra.Command{
		Use:   "update [section]",
		Short: "Update settings for a section",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			settings := make(map[string]interface{})
			for _, v := range values {
				parts := strings.Split(v, "=")
				if len(parts) != 2 {
					return fmt.Errorf("invalid setting format: %s", v)
				}
				settings[parts[0]] = parts[1]
			}
			return client.updateSettings(args[0], settings)
		},
	}

	cmd.Flags().StringSliceVarP(&values, "set", "s", nil, "Settings to update (key=value)")
	cmd.MarkFlagRequired("set")

	return cmd
}

func newSettingsResetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reset [section]",
		Short: "Reset settings for a section to defaults",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return client.resetSettings(args[0])
		},
	}
}

// HELPERS
func unpackZip(zipPath string, destPath string, flat bool) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		// DETERMINE OUTPUT PATH
		var targetPath string
		if flat {
			targetPath = filepath.Join(destPath, filepath.Base(f.Name))
		} else {
			targetPath = filepath.Join(destPath, f.Name)
		}

		if f.FileInfo().IsDir() {
			if !flat {
				os.MkdirAll(targetPath, 0755)
			}
			continue
		}

		// CREATE PARENT DIRS
		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return err
		}

		// EXTRACT FILE
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

func unpackTarGz(tarPath string, destPath string, flat bool) error {
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

		// DETERMINE OUTPUT PATH
		var targetPath string
		if flat {
			targetPath = filepath.Join(destPath, filepath.Base(header.Name))
		} else {
			targetPath = filepath.Join(destPath, header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if !flat {
				if err := os.MkdirAll(targetPath, 0755); err != nil {
					return err
				}
			}
		case tar.TypeReg:
			// CREATE PARENT DIRS
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return err
			}

			// EXTRACT FILE
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

func recursivelyUnpack(archivePath string, destPath string, flat bool) error {
	// DETECT ARCHIVE TYPE
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()

	// READ MAGIC NUMBERS
	magic := make([]byte, 4)
	if _, err := file.Read(magic); err != nil {
		return err
	}

	// RESET FILE POINTER
	if _, err := file.Seek(0, 0); err != nil {
		return err
	}

	isZip := magic[0] == 0x50 && magic[1] == 0x4B && magic[2] == 0x03 && magic[3] == 0x04
	isGzip := magic[0] == 0x1F && magic[1] == 0x8B

	// FIRST LEVEL UNPACKING
	switch {
	case isZip:
		if err := unpackZip(archivePath, destPath, flat); err != nil {
			return err
		}
	case isGzip:
		if err := unpackTarGz(archivePath, destPath, flat); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported archive format")
	}

	// FIND AND UNPACK NESTED ARCHIVES
	files := []string{}
	err = filepath.Walk(destPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return err
	}

	// PROCESS EACH FILE FOR NESTED ARCHIVES
	for _, path := range files {
		file, err := os.Open(path)
		if err != nil {
			continue
		}

		magic := make([]byte, 4)
		if _, err := file.Read(magic); err != nil {
			file.Close()
			continue
		}
		file.Close()

		isNested := (magic[0] == 0x50 && magic[1] == 0x4B && magic[2] == 0x03 && magic[3] == 0x04) || // ZIP
			(magic[0] == 0x1F && magic[1] == 0x8B) // GZIP

		if isNested {
			// CREATE TEMP DIR FOR NESTED CONTENTS
			tempDir, err := os.MkdirTemp("", "dfcli-nested-*")
			if err != nil {
				continue
			}

			// UNPACK NESTED ARCHIVE
			if err := recursivelyUnpack(path, tempDir, flat); err != nil {
				os.RemoveAll(tempDir)
				continue
			}

			// MOVE CONTENTS TO FINAL DESTINATION
			err = filepath.Walk(tempDir, func(srcPath string, info os.FileInfo, err error) error {
				if err != nil || info.IsDir() {
					return nil
				}

				// DETERMINE TARGET PATH
				var targetPath string
				if flat {
					targetPath = filepath.Join(destPath, filepath.Base(srcPath))
				} else {
					rel, _ := filepath.Rel(tempDir, srcPath)
					parentDir := filepath.Dir(path)
					targetPath = filepath.Join(parentDir, rel)
				}

				// ENSURE TARGET DIR EXISTS
				if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
					return err
				}

				// MOVE FILE
				return os.Rename(srcPath, targetPath)
			})

			os.RemoveAll(tempDir)
			if err != nil {
				continue
			}

			// REMOVE ORIGINAL ARCHIVE
			os.Remove(path)
		}
	}

	return nil
}

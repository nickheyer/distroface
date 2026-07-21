package api

import (
	"context"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// Stored tokens must not leak into login, use a bare client
func bareAuthClient() distrofacev1connect.AuthServiceClient {
	return distrofacev1connect.NewAuthServiceClient(client.HTTPClient, client.BaseURL)
}

// Exchanges credentials for a session with a known expiry
func login(ctx context.Context, username, password string) (token string, canonical string, expiry time.Time, err error) {
	auth := bareAuthClient()

	resp, err := auth.Login(ctx, connect.NewRequest(&v1.LoginRequest{
		Identifier: username,
		Password:   password,
	}))
	if err != nil {
		return "", "", time.Time{}, rpcErr(err)
	}
	token = resp.Msg.GetSessionToken()
	if token == "" {
		return "", "", time.Time{}, fmt.Errorf("no token in login response")
	}
	canonical = resp.Msg.GetUser().GetUsername()
	if canonical == "" {
		canonical = username
	}

	// Login carries no expiry, an immediate refresh does
	refReq := connect.NewRequest(&v1.RefreshSessionRequest{})
	refReq.Header().Set("Authorization", "Bearer "+token)
	refResp, err := auth.RefreshSession(ctx, refReq)
	if err != nil {
		debugf("Post-login refresh failed (%v), assuming a short session", err)
		return token, canonical, time.Now().Add(time.Hour), nil
	}
	return refResp.Msg.GetSessionToken(), canonical, time.Unix(refResp.Msg.GetExpiresAt(), 0), nil
}

// Validates the pat and returns its owner
func whoami(ctx context.Context, token string) (string, error) {
	req := connect.NewRequest(&v1.GetCurrentUserRequest{})
	req.Header().Set("Authorization", "Bearer "+token)
	resp, err := bareAuthClient().GetCurrentUser(ctx, req)
	if err != nil {
		return "", rpcErr(err)
	}
	return resp.Msg.GetUser().GetUsername(), nil
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

			// PAT login verifies and stores, no session dance
			if patToken != "" {
				if !strings.HasPrefix(patToken, patPrefix) {
					return fmt.Errorf("token must start with %q - create one under Settings → Tokens", patPrefix)
				}
				owner, err := whoami(cmd.Context(), patToken)
				if err != nil {
					return fmt.Errorf("token was rejected by %s: %w", server, err)
				}
				config := AuthConfig{
					Token:     patToken,
					Username:  owner,
					ExpiresAt: time.Now().Add(24 * 365 * 10 * time.Hour), // PATs carry their own expiry server-side
					Server:    server,
				}
				if err := saveConfig(config); err != nil {
					return fmt.Errorf("failed to save config: %v", err)
				}
				fmt.Printf("Personal access token for %s stored\n", owner)
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

			token, canonical, expiry, err := login(cmd.Context(), username, password)
			if err != nil {
				return fmt.Errorf("login failed: %v", err)
			}

			config := AuthConfig{
				Token:     token,
				Username:  canonical,
				Server:    server,
				ExpiresAt: expiry,
			}
			if err := saveConfig(config); err != nil {
				return fmt.Errorf("failed to save config: %v", err)
			}

			fmt.Printf("Successfully logged in as %s on %s\n", canonical, server)
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
			// Best effort server side session revoke, PATs stay valid
			if token := client.Tokens.GetToken(); token != "" && !client.Tokens.IsPAT() {
				req := connect.NewRequest(&v1.LogoutRequest{})
				req.Header().Set("Authorization", "Bearer "+token)
				if _, err := bareAuthClient().Logout(cmd.Context(), req); err != nil {
					debugf("Server side logout failed: %v", err)
				}
			}

			if err := os.Remove(configPath()); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("failed to remove config: %v", err)
			}
			fmt.Println("Successfully logged out")
			return nil
		},
	}
}

package api

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

func newGitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "git",
		Short: "Git helpers that drive the local git binary",
	}
	cmd.AddCommand(
		newGitCredentialCmd(),
		newGitCloneCmd(),
	)
	return cmd
}

// Terminal stays attached so prompts and progress show
func runGit(ctx context.Context, args ...string) error {
	debugf("git %s", strings.Join(args, " "))
	c := exec.CommandContext(ctx, "git", args...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}

func newGitCredentialCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "credential <get|store|erase>",
		Short: "Git credential helper backed by the stored login",
		Long: `Answers git credential requests for the configured server with the
stored session or personal access token. Enable it with:

  git config --global credential.helper "dfcli git credential"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			attrs := readCredentialAttrs(os.Stdin)
			if args[0] != "get" {
				return nil
			}
			if !credentialHostMatches(client.BaseURL, attrs["protocol"], attrs["host"]) {
				debugf("Host %s is not %s, staying silent", attrs["host"], client.BaseURL)
				return nil
			}
			if client.Tokens.IsExpired() {
				if err := client.refreshToken(cmd.Context()); err != nil {
					return err
				}
			}
			token := client.Tokens.GetToken()
			if token == "" {
				return nil
			}
			username := client.Username
			if username == "" {
				username = "token"
			}
			fmt.Printf("username=%s\npassword=%s\n", username, token)
			return nil
		},
	}
}

// Key value lines up to the first blank line
func readCredentialAttrs(r io.Reader) map[string]string {
	attrs := map[string]string{}
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			break
		}
		if key, value, ok := strings.Cut(line, "="); ok {
			attrs[key] = value
		}
	}
	return attrs
}

// Scheme, host, and port must match the server
func credentialHostMatches(serverURL, protocol, host string) bool {
	if host == "" {
		return false
	}
	server, err := url.Parse(serverURL)
	if err != nil {
		return false
	}
	if protocol == "" {
		protocol = server.Scheme
	}
	if !strings.EqualFold(protocol, server.Scheme) {
		return false
	}
	return strings.EqualFold(hostPort(server.Scheme, server.Host), hostPort(protocol, host))
}

func hostPort(scheme, host string) string {
	h, p, err := net.SplitHostPort(host)
	if err != nil {
		h, p = host, ""
	}
	if p == "" {
		p = "80"
		if strings.EqualFold(scheme, "https") {
			p = "443"
		}
	}
	return net.JoinHostPort(h, p)
}

func newGitCloneCmd() *cobra.Command {
	var namespace string
	var useSSH bool

	cmd := &cobra.Command{
		Use:   "clone [namespace/]name [dir]",
		Short: "Clone a repository with the local git",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := gitRepoArg(args[0], namespace)
			if err != nil {
				return err
			}
			repo, err := getGitRepo(cmd.Context(), ref)
			if err != nil {
				return err
			}
			cloneURL := repo.GetHttpCloneUrl()
			if useSSH {
				cloneURL = repo.GetSshCloneUrl()
				if cloneURL == "" {
					return fmt.Errorf("ssh is disabled on %s", client.BaseURL)
				}
			}
			if cloneURL == "" {
				return fmt.Errorf("server returned no clone url for %s", ref)
			}
			gitArgs := []string{"clone", cloneURL}
			if len(args) == 2 {
				gitArgs = append(gitArgs, args[1])
			}
			return runGit(cmd.Context(), gitArgs...)
		},
	}

	cmd.Flags().BoolVar(&useSSH, "ssh", false, "Clone over ssh instead of http")
	cmd.Flags().StringVar(&namespace, "namespace", "", "Repository namespace, defaults to your username")
	return cmd
}

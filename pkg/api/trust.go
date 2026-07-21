package api

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

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

// Downloads the public instance root CA pem
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

			// dfcli itself trusts the CA from the config dir
			if err := os.MkdirAll(filepath.Dir(caPath()), 0755); err != nil {
				return err
			}
			if err := os.WriteFile(caPath(), pem, 0644); err != nil {
				return err
			}
			fmt.Printf("Stored instance CA at %s, dfcli now trusts this server's TLS.\n", caPath())

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

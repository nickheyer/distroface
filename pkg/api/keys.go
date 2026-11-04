package api

import (
	"fmt"
	"io"
	"os"
	"strings"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/spf13/cobra"
)

// File argument or stdin when absent
func readKeyInput(args []string) (string, error) {
	var data []byte
	var err error
	if len(args) == 1 && args[0] != "-" {
		data, err = os.ReadFile(args[0])
	} else {
		data, err = io.ReadAll(os.Stdin)
	}
	if err != nil {
		return "", err
	}
	key := strings.TrimSpace(string(data))
	if key == "" {
		return "", fmt.Errorf("no key data provided")
	}
	return key, nil
}

func newKeyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "key",
		Short: "Manage your ssh keys",
	}
	cmd.AddCommand(
		newKeyAddCmd(),
		newKeyListCmd(),
		newKeyRemoveCmd(),
	)
	return cmd
}

func newKeyAddCmd() *cobra.Command {
	var title, usage string
	var signing bool

	cmd := &cobra.Command{
		Use:   "add [file]",
		Short: "Register an ssh public key from a file or stdin",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			publicKey, err := readKeyInput(args)
			if err != nil {
				return err
			}
			if signing {
				usage = "signing"
			}
			keyUsage := v1.SSHKeyUsage_SSH_KEY_USAGE_AUTH
			switch strings.ToLower(usage) {
			case "auth":
			case "signing":
				keyUsage = v1.SSHKeyUsage_SSH_KEY_USAGE_SIGNING
			case "both":
				keyUsage = v1.SSHKeyUsage_SSH_KEY_USAGE_BOTH
			default:
				return fmt.Errorf("usage must be auth, signing, or both")
			}
			if title == "" {
				if fields := strings.Fields(publicKey); len(fields) >= 3 {
					title = fields[2]
				}
			}
			resp, err := client.Keys().AddSSHKey(cmd.Context(), &v1.AddSSHKeyRequest{
				Title:     title,
				PublicKey: publicKey,
				Usage:     keyUsage,
			})
			if err != nil {
				return rpcErr(err)
			}
			return printProto(resp.GetKey())
		},
	}

	cmd.Flags().StringVar(&title, "title", "", "Key title, defaults to the key comment")
	cmd.Flags().StringVar(&usage, "usage", "auth", "Key usage (auth, signing, both)")
	cmd.Flags().BoolVar(&signing, "signing", false, "Shorthand for --usage signing")
	return cmd
}

func newKeyListCmd() *cobra.Command {
	var table bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List your ssh keys",
		RunE: func(cmd *cobra.Command, args []string) error {
			keys, err := allPages(func(token string) ([]*v1.SSHKey, string, error) {
				resp, err := client.Keys().ListSSHKeys(cmd.Context(), &v1.ListSSHKeysRequest{
					Page: &v1.PageRequest{PageSize: maxPageSize, PageToken: token},
				})
				if err != nil {
					return nil, "", rpcErr(err)
				}
				return resp.GetKeys(), resp.GetPage().GetNextPageToken(), nil
			})
			if err != nil {
				return err
			}
			header := []string{"ID", "TITLE", "TYPE", "USAGE", "FINGERPRINT", "LAST USED"}
			return printOrTable(table, keys, header, func(k *v1.SSHKey) []string {
				return []string{
					k.GetId(),
					k.GetTitle(),
					k.GetKeyType(),
					enumLabel(k.GetUsage(), "SSH_KEY_USAGE_"),
					k.GetFingerprint(),
					fmtTime(k.GetLastUsedAt()),
				}
			})
		},
	}

	cmd.Flags().BoolVarP(&table, "table", "t", false, "Format results as a table")
	return cmd
}

func newKeyRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm id",
		Short: "Remove an ssh key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := client.Keys().DeleteSSHKey(cmd.Context(), &v1.DeleteSSHKeyRequest{Id: args[0]})
			if err != nil {
				return rpcErr(err)
			}
			fmt.Printf("Removed ssh key %s\n", args[0])
			return nil
		},
	}
}

func newGPGCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gpg",
		Short: "Manage your gpg signing keys",
	}
	cmd.AddCommand(
		newGPGAddCmd(),
		newGPGListCmd(),
		newGPGRemoveCmd(),
	)
	return cmd
}

func newGPGAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add [file]",
		Short: "Register an armored gpg public key from a file or stdin",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			armored, err := readKeyInput(args)
			if err != nil {
				return err
			}
			resp, err := client.Keys().AddGPGKey(cmd.Context(), &v1.AddGPGKeyRequest{ArmoredPublicKey: armored})
			if err != nil {
				return rpcErr(err)
			}
			if err := printProto(resp.GetKey()); err != nil {
				return err
			}
			if challenge := resp.GetChallenge(); challenge != "" {
				fmt.Fprintf(os.Stderr, "Sign this challenge and verify the key under Settings > Keys:\n%s\n", challenge)
			}
			return nil
		},
	}
}

func newGPGListCmd() *cobra.Command {
	var table bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List your gpg keys",
		RunE: func(cmd *cobra.Command, args []string) error {
			keys, err := allPages(func(token string) ([]*v1.GPGKey, string, error) {
				resp, err := client.Keys().ListGPGKeys(cmd.Context(), &v1.ListGPGKeysRequest{
					Page: &v1.PageRequest{PageSize: maxPageSize, PageToken: token},
				})
				if err != nil {
					return nil, "", rpcErr(err)
				}
				return resp.GetKeys(), resp.GetPage().GetNextPageToken(), nil
			})
			if err != nil {
				return err
			}
			header := []string{"ID", "KEY ID", "EMAILS", "VERIFIED", "EXPIRES"}
			return printOrTable(table, keys, header, func(k *v1.GPGKey) []string {
				return []string{
					k.GetId(),
					k.GetKeyId(),
					strings.Join(k.GetEmails(), ","),
					fmt.Sprint(k.GetVerified()),
					fmtTime(k.GetExpiresAt()),
				}
			})
		},
	}

	cmd.Flags().BoolVarP(&table, "table", "t", false, "Format results as a table")
	return cmd
}

func newGPGRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm id",
		Short: "Remove a gpg key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := client.Keys().DeleteGPGKey(cmd.Context(), &v1.DeleteGPGKeyRequest{Id: args[0]})
			if err != nil {
				return rpcErr(err)
			}
			fmt.Printf("Removed gpg key %s\n", args[0])
			return nil
		},
	}
}

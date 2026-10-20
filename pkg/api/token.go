package api

import (
	"fmt"
	"strings"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/spf13/cobra"
)

func newTokenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Manage personal access and deploy tokens",
	}
	cmd.AddCommand(
		newTokenCreateCmd(),
		newTokenListCmd(),
		newTokenDeleteCmd(),
	)
	return cmd
}

// Scope kind and id split on the first colon
func parseScope(s string, readOnly bool) (*v1.CredentialScope, error) {
	kind, id, ok := strings.Cut(s, ":")
	if !ok || id == "" {
		return nil, fmt.Errorf("scope must look like git:namespace/name or org:name")
	}
	scope := &v1.CredentialScope{Id: id, ReadOnly: readOnly}
	switch strings.ToLower(kind) {
	case "git":
		if parseRepoRef(id).Namespace == "" {
			return nil, fmt.Errorf("git scope must be qualified as namespace/name")
		}
		scope.Type = v1.CredentialScopeType_CREDENTIAL_SCOPE_TYPE_GIT_REPO
	case "org":
		scope.Type = v1.CredentialScopeType_CREDENTIAL_SCOPE_TYPE_ORG
	default:
		return nil, fmt.Errorf("scope kind must be git or org")
	}
	return scope, nil
}

func scopeLabel(s *v1.CredentialScope) string {
	if s == nil {
		return "full"
	}
	label := enumLabel(s.GetType(), "CREDENTIAL_SCOPE_TYPE_") + ":" + s.GetId()
	if s.GetReadOnly() {
		label += " (read only)"
	}
	return label
}

func newTokenCreateCmd() *cobra.Command {
	var name, scope string
	var readOnly bool
	var expiresDays int32

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a token, the plaintext is printed once",
		Long: `Create a personal access token. Without --scope the token carries your
full identity. --scope git:namespace/name limits it to one repository and
its linked packages, --scope org:name to one organization.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			req := &v1.CreateAPITokenRequest{Name: name}
			if scope != "" {
				s, err := parseScope(scope, readOnly)
				if err != nil {
					return err
				}
				req.Scope = s
			} else if readOnly {
				return fmt.Errorf("--read-only needs a --scope")
			}
			if expiresDays > 0 {
				req.ExpiresInDays = &expiresDays
			}
			resp, err := client.APITokens().CreateAPIToken(cmd.Context(), req)
			if err != nil {
				return rpcErr(err)
			}
			fmt.Printf("Created token %s (%s), id %s\n", resp.GetToken().GetName(), scopeLabel(resp.GetToken().GetScope()), resp.GetToken().GetId())
			fmt.Printf("Store it now, it is not shown again:\n\n  %s\n", resp.GetPlaintextToken())
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Token name")
	cmd.Flags().StringVar(&scope, "scope", "", "Limit to git:namespace/name or org:name")
	cmd.Flags().BoolVar(&readOnly, "read-only", false, "Scoped token may only read")
	cmd.Flags().Int32Var(&expiresDays, "expires-days", 0, "Days until expiry, 0 never expires")
	return cmd
}

func newTokenListCmd() *cobra.Command {
	var table bool
	var gitRepo string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List your tokens",
		RunE: func(cmd *cobra.Command, args []string) error {
			tokens, err := allPages(func(token string) ([]*v1.APIToken, string, error) {
				resp, err := client.APITokens().ListAPITokens(cmd.Context(), &v1.ListAPITokensRequest{
					Page:    &v1.PageRequest{PageSize: maxPageSize, PageToken: token},
					GitRepo: gitRepo,
				})
				if err != nil {
					return nil, "", rpcErr(err)
				}
				return resp.GetTokens(), resp.GetPage().GetNextPageToken(), nil
			})
			if err != nil {
				return err
			}
			header := []string{"ID", "NAME", "SCOPE", "EXPIRES", "LAST USED"}
			return printOrTable(table, tokens, header, func(t *v1.APIToken) []string {
				return []string{t.GetId(), t.GetName(), scopeLabel(t.GetScope()), fmtTime(t.GetExpiresAt()), fmtTime(t.GetLastUsedAt())}
			})
		},
	}

	cmd.Flags().StringVar(&gitRepo, "git-repo", "", "Only tokens scoped to this namespace/name")
	cmd.Flags().BoolVarP(&table, "table", "t", false, "Format results as a table")
	return cmd
}

func newTokenDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete id",
		Short: "Revoke a token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := client.APITokens().DeleteAPIToken(cmd.Context(), &v1.DeleteAPITokenRequest{Id: args[0]})
			if err != nil {
				return rpcErr(err)
			}
			fmt.Printf("Revoked token %s\n", args[0])
			return nil
		},
	}
}

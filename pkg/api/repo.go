package api

import (
	"context"
	"fmt"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/spf13/cobra"
)

// Bare names fall back to the login username
func gitRepoArg(arg, namespace string) (RepoRef, error) {
	ref := repoArg(arg, namespace)
	if ref.Namespace == "" {
		ref.Namespace = client.Username
	}
	if ref.Namespace == "" || ref.Name == "" {
		return ref, fmt.Errorf("qualify the repository as namespace/name")
	}
	return ref, nil
}

func getGitRepo(ctx context.Context, ref RepoRef) (*v1.GitRepository, error) {
	resp, err := client.Git().GetGitRepository(ctx, &v1.GetGitRepositoryRequest{
		Namespace: ref.Namespace,
		Name:      ref.Name,
	})
	if err != nil {
		return nil, rpcErr(err)
	}
	return resp.GetRepository(), nil
}

func newRepoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repo",
		Short: "Manage git repositories",
		Long: `Manage git repositories.

Repositories are addressed as [namespace/]name. Bare names resolve in
--namespace, or in your own namespace when the flag is absent.`,
	}
	cmd.AddCommand(
		newRepoCreateCmd(),
		newRepoListCmd(),
		newRepoViewCmd(),
		newRepoDeleteCmd(),
		newRepoForkCmd(),
		newRepoTransferCmd(),
		newRepoArchiveCmd(),
		newRepoTopicsCmd(),
	)
	return cmd
}

func newRepoCreateCmd() *cobra.Command {
	var namespace, description, mirror, mirrorUsername, mirrorToken, template, defaultBranch string
	var private, autoInit bool

	cmd := &cobra.Command{
		Use:   "create [namespace/]name",
		Short: "Create a git repository",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := gitRepoArg(args[0], namespace)
			if err != nil {
				return err
			}
			var visibility v1.Visibility
			if private {
				visibility = v1.Visibility_VISIBILITY_PRIVATE
			}
			if template != "" {
				tpl := parseRepoRef(template)
				if tpl.Namespace == "" {
					return fmt.Errorf("template must be qualified as namespace/name")
				}
				resp, err := client.Git().UseTemplate(cmd.Context(), &v1.UseTemplateRequest{
					TemplateNamespace: tpl.Namespace,
					TemplateName:      tpl.Name,
					Namespace:         ref.Namespace,
					Name:              ref.Name,
					Description:       description,
					Visibility:        visibility,
				})
				if err != nil {
					return rpcErr(err)
				}
				return printProto(resp.GetRepository())
			}
			req := &v1.CreateGitRepositoryRequest{
				Namespace:     ref.Namespace,
				Name:          ref.Name,
				Description:   description,
				Visibility:    visibility,
				DefaultBranch: defaultBranch,
				AutoInit:      autoInit,
			}
			if mirror != "" {
				req.Type = v1.GitRepoType_GIT_REPO_TYPE_MIRROR
				req.Mirror = &v1.MirrorConfig{Upstream: mirror, Username: mirrorUsername}
				if mirrorToken != "" {
					req.Mirror.AuthToken = &mirrorToken
				}
			}
			resp, err := client.Git().CreateGitRepository(cmd.Context(), req)
			if err != nil {
				return rpcErr(err)
			}
			return printProto(resp.GetRepository())
		},
	}

	cmd.Flags().StringVar(&namespace, "namespace", "", "Target namespace (user or organization)")
	cmd.Flags().StringVarP(&description, "description", "d", "", "Repository description")
	cmd.Flags().BoolVarP(&private, "private", "p", false, "Make repository private")
	cmd.Flags().StringVar(&mirror, "mirror", "", "Create a pull mirror of this upstream git url")
	cmd.Flags().StringVar(&mirrorUsername, "mirror-username", "", "Username for the upstream, some hosts accept the token alone")
	cmd.Flags().StringVar(&mirrorToken, "mirror-token", "", "Token or password for a private upstream")
	cmd.Flags().StringVar(&template, "template", "", "Create from a template repository (namespace/name)")
	cmd.Flags().StringVar(&defaultBranch, "default-branch", "", "Default branch name")
	cmd.Flags().BoolVar(&autoInit, "auto-init", false, "Create an initial commit with a readme")
	return cmd
}

func newRepoListCmd() *cobra.Command {
	var namespace string
	var table, archived bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List git repositories visible to you",
		RunE: func(cmd *cobra.Command, args []string) error {
			repos, err := allPages(func(token string) ([]*v1.GitRepository, string, error) {
				resp, err := client.Git().ListGitRepositories(cmd.Context(), &v1.ListGitRepositoriesRequest{
					Page:            &v1.PageRequest{PageSize: maxPageSize, PageToken: token},
					Namespace:       namespace,
					IncludeArchived: archived,
				})
				if err != nil {
					return nil, "", rpcErr(err)
				}
				return resp.GetRepositories(), resp.GetPage().GetNextPageToken(), nil
			})
			if err != nil {
				return err
			}
			return printOrTable(table, repos, []string{"NAME", "VISIBILITY", "TYPE", "BRANCH", "UPDATED"}, func(r *v1.GitRepository) []string {
				return []string{
					r.GetFullName(),
					enumLabel(r.GetVisibility(), "VISIBILITY_"),
					enumLabel(r.GetType(), "GIT_REPO_TYPE_"),
					r.GetDefaultBranch(),
					fmtTime(r.GetUpdatedAt()),
				}
			})
		},
	}

	cmd.Flags().StringVar(&namespace, "namespace", "", "Filter by namespace")
	cmd.Flags().BoolVar(&archived, "archived", false, "Include archived repositories")
	cmd.Flags().BoolVarP(&table, "table", "t", false, "Format results as a table")
	return cmd
}

func newRepoViewCmd() *cobra.Command {
	var namespace string

	cmd := &cobra.Command{
		Use:   "view [namespace/]name",
		Short: "Show a git repository",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := gitRepoArg(args[0], namespace)
			if err != nil {
				return err
			}
			repo, err := getGitRepo(cmd.Context(), ref)
			if err != nil {
				return err
			}
			return printProto(repo)
		},
	}

	cmd.Flags().StringVar(&namespace, "namespace", "", "Repository namespace")
	return cmd
}

func newRepoDeleteCmd() *cobra.Command {
	var namespace string
	var linked bool

	cmd := &cobra.Command{
		Use:   "delete [namespace/]name",
		Short: "Delete a git repository",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := gitRepoArg(args[0], namespace)
			if err != nil {
				return err
			}
			_, err = client.Git().DeleteGitRepository(cmd.Context(), &v1.DeleteGitRepositoryRequest{
				Namespace:    ref.Namespace,
				Name:         ref.Name,
				DeleteLinked: linked,
			})
			if err != nil {
				return rpcErr(err)
			}
			fmt.Printf("Deleted %s\n", ref)
			return nil
		},
	}

	cmd.Flags().StringVar(&namespace, "namespace", "", "Repository namespace")
	cmd.Flags().BoolVar(&linked, "linked", false, "Also delete linked image and artifact repositories")
	return cmd
}

func newRepoForkCmd() *cobra.Command {
	var namespace, name string

	cmd := &cobra.Command{
		Use:   "fork namespace/name",
		Short: "Fork a git repository into your namespace or an organization",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := gitRepoArg(args[0], "")
			if err != nil {
				return err
			}
			resp, err := client.Git().ForkGitRepository(cmd.Context(), &v1.ForkGitRepositoryRequest{
				Namespace:       ref.Namespace,
				Name:            ref.Name,
				TargetNamespace: namespace,
				TargetName:      name,
			})
			if err != nil {
				return rpcErr(err)
			}
			return printProto(resp.GetRepository())
		},
	}

	cmd.Flags().StringVar(&namespace, "namespace", "", "Namespace to fork into, defaults to your own")
	cmd.Flags().StringVar(&name, "name", "", "Name of the fork, defaults to the source name")
	return cmd
}

func newRepoTransferCmd() *cobra.Command {
	var namespace string

	cmd := &cobra.Command{
		Use:   "transfer [namespace/]name new-namespace[/new-name]",
		Short: "Move a git repository to another namespace or name",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := gitRepoArg(args[0], namespace)
			if err != nil {
				return err
			}
			target := parseRepoRef(args[1])
			newNamespace, newName := target.Namespace, target.Name
			if newNamespace == "" {
				newNamespace, newName = target.Name, ""
			}
			resp, err := client.Git().MoveGitRepository(cmd.Context(), &v1.MoveGitRepositoryRequest{
				Namespace:    ref.Namespace,
				Name:         ref.Name,
				NewNamespace: newNamespace,
				NewName:      newName,
			})
			if err != nil {
				return rpcErr(err)
			}
			return printProto(resp)
		},
	}

	cmd.Flags().StringVar(&namespace, "namespace", "", "Repository namespace")
	return cmd
}

func newRepoArchiveCmd() *cobra.Command {
	var namespace string
	var undo bool

	cmd := &cobra.Command{
		Use:   "archive [namespace/]name",
		Short: "Archive a git repository, --undo restores it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := gitRepoArg(args[0], namespace)
			if err != nil {
				return err
			}
			if undo {
				_, err = client.Git().UnarchiveGitRepository(cmd.Context(), &v1.UnarchiveGitRepositoryRequest{
					Namespace: ref.Namespace,
					Name:      ref.Name,
				})
			} else {
				_, err = client.Git().ArchiveGitRepository(cmd.Context(), &v1.ArchiveGitRepositoryRequest{
					Namespace: ref.Namespace,
					Name:      ref.Name,
				})
			}
			if err != nil {
				return rpcErr(err)
			}
			if undo {
				fmt.Printf("Unarchived %s\n", ref)
			} else {
				fmt.Printf("Archived %s\n", ref)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&namespace, "namespace", "", "Repository namespace")
	cmd.Flags().BoolVar(&undo, "undo", false, "Unarchive instead")
	return cmd
}

func newRepoTopicsCmd() *cobra.Command {
	var namespace string

	cmd := &cobra.Command{
		Use:   "topics [namespace/]name [topic...]",
		Short: "Replace the topics of a git repository, none clears them",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := gitRepoArg(args[0], namespace)
			if err != nil {
				return err
			}
			resp, err := client.Git().SetTopics(cmd.Context(), &v1.SetTopicsRequest{
				Namespace: ref.Namespace,
				Name:      ref.Name,
				Topics:    args[1:],
			})
			if err != nil {
				return rpcErr(err)
			}
			return printProto(resp)
		},
	}

	cmd.Flags().StringVar(&namespace, "namespace", "", "Repository namespace")
	return cmd
}

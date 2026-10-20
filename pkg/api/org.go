package api

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/spf13/cobra"
)

func newOrgCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "org",
		Short: "Manage organizations",
	}
	cmd.AddCommand(newOrgTeamCmd())
	return cmd
}

func newOrgTeamCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "team",
		Short: "Manage organization teams, addressed as org plus team name",
	}
	cmd.AddCommand(
		newTeamListCmd(),
		newTeamViewCmd(),
		newTeamCreateCmd(),
		newTeamMemberCmd("add", "Add an org member to a team"),
		newTeamMemberCmd("remove", "Remove a user from a team"),
		newTeamRepoCmd("add-repo", "Include a repository in a team"),
		newTeamRepoCmd("remove-repo", "Drop a repository from a team"),
	)
	return cmd
}

func resolveOrgID(ctx context.Context, name string) (string, error) {
	resp, err := client.Organizations().GetOrganization(ctx, &v1.GetOrganizationRequest{Name: name})
	if err != nil {
		return "", rpcErr(err)
	}
	return resp.GetOrganization().GetId(), nil
}

func listTeams(ctx context.Context, orgID string) ([]*v1.Team, error) {
	return allPages(func(token string) ([]*v1.Team, string, error) {
		resp, err := client.Organizations().ListTeams(ctx, &v1.ListTeamsRequest{
			Page:  &v1.PageRequest{PageSize: maxPageSize, PageToken: token},
			OrgId: orgID,
		})
		if err != nil {
			return nil, "", rpcErr(err)
		}
		return resp.GetTeams(), resp.GetPage().GetNextPageToken(), nil
	})
}

// Org name plus team name or id to the team
func resolveTeam(ctx context.Context, org, team string) (string, *v1.Team, error) {
	orgID, err := resolveOrgID(ctx, org)
	if err != nil {
		return "", nil, err
	}
	teams, err := listTeams(ctx, orgID)
	if err != nil {
		return "", nil, err
	}
	for _, t := range teams {
		if strings.EqualFold(t.GetName(), team) || t.GetId() == team {
			return orgID, t, nil
		}
	}
	return "", nil, fmt.Errorf("team %q not found in %s", team, org)
}

func newTeamListCmd() *cobra.Command {
	var table bool

	cmd := &cobra.Command{
		Use:   "list org",
		Short: "List the teams of an organization",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			orgID, err := resolveOrgID(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			teams, err := listTeams(cmd.Context(), orgID)
			if err != nil {
				return err
			}
			header := []string{"NAME", "MEMBERS", "REPOS", "ALL REPOS", "ADMIN", "DEFAULT"}
			return printOrTable(table, teams, header, func(t *v1.Team) []string {
				return []string{
					t.GetName(),
					strconv.Itoa(int(t.GetMemberCount())),
					strconv.Itoa(int(t.GetRepoCount())),
					strconv.FormatBool(t.GetIncludesAllRepos()),
					strconv.FormatBool(t.GetAdminAccess()),
					strconv.FormatBool(t.GetIsDefault()),
				}
			})
		},
	}

	cmd.Flags().BoolVarP(&table, "table", "t", false, "Format results as a table")
	return cmd
}

func newTeamViewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "view org team",
		Short: "Show a team with its members and repositories",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			orgID, team, err := resolveTeam(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			members, err := client.Organizations().ListTeamMembers(cmd.Context(), &v1.ListTeamMembersRequest{
				Page:   &v1.PageRequest{PageSize: maxPageSize},
				OrgId:  orgID,
				TeamId: team.GetId(),
			})
			if err != nil {
				return rpcErr(err)
			}
			repos, err := client.Organizations().ListTeamRepos(cmd.Context(), &v1.ListTeamReposRequest{
				Page:   &v1.PageRequest{PageSize: maxPageSize},
				OrgId:  orgID,
				TeamId: team.GetId(),
			})
			if err != nil {
				return rpcErr(err)
			}
			return printJSON(map[string]any{
				"team":    protoJSONValue(team),
				"members": protoJSONValues(members.GetMembers()),
				"repos":   protoJSONValues(repos.GetRepos()),
			})
		},
	}
}

func newTeamCreateCmd() *cobra.Command {
	var description string
	var canCreate, allRepos, admin bool

	cmd := &cobra.Command{
		Use:   "create org name",
		Short: "Create a team",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			orgID, err := resolveOrgID(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			resp, err := client.Organizations().CreateTeam(cmd.Context(), &v1.CreateTeamRequest{
				OrgId:            orgID,
				Name:             args[1],
				Description:      description,
				CanCreateRepos:   canCreate,
				IncludesAllRepos: allRepos,
				AdminAccess:      admin,
			})
			if err != nil {
				return rpcErr(err)
			}
			return printProto(resp.GetTeam())
		},
	}

	cmd.Flags().StringVarP(&description, "description", "d", "", "Team description")
	cmd.Flags().BoolVar(&canCreate, "can-create-repos", false, "Members may create repositories")
	cmd.Flags().BoolVar(&allRepos, "all-repos", false, "Team includes every repository")
	cmd.Flags().BoolVar(&admin, "admin", false, "Repo admin on included repositories")
	return cmd
}

func newTeamMemberCmd(verb, short string) *cobra.Command {
	return &cobra.Command{
		Use:   verb + " org team username",
		Short: short,
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			orgID, team, err := resolveTeam(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			userID, err := resolveUserID(cmd.Context(), args[2])
			if err != nil {
				return err
			}
			if verb == "add" {
				_, err = client.Organizations().AddTeamMember(cmd.Context(), &v1.AddTeamMemberRequest{
					OrgId: orgID, TeamId: team.GetId(), UserId: userID,
				})
			} else {
				_, err = client.Organizations().RemoveTeamMember(cmd.Context(), &v1.RemoveTeamMemberRequest{
					OrgId: orgID, TeamId: team.GetId(), UserId: userID,
				})
			}
			if err != nil {
				return rpcErr(err)
			}
			fmt.Printf("Team %s: %s %s\n", team.GetName(), verb, args[2])
			return nil
		},
	}
}

func newTeamRepoCmd(verb, short string) *cobra.Command {
	return &cobra.Command{
		Use:   verb + " org team repo",
		Short: short,
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			orgID, team, err := resolveTeam(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			repoName := parseRepoRef(args[2]).Name
			if verb == "add-repo" {
				_, err = client.Organizations().AddTeamRepo(cmd.Context(), &v1.AddTeamRepoRequest{
					OrgId: orgID, TeamId: team.GetId(), RepoName: repoName,
				})
			} else {
				_, err = client.Organizations().RemoveTeamRepo(cmd.Context(), &v1.RemoveTeamRepoRequest{
					OrgId: orgID, TeamId: team.GetId(), RepoName: repoName,
				})
			}
			if err != nil {
				return rpcErr(err)
			}
			fmt.Printf("Team %s: %s %s\n", team.GetName(), verb, repoName)
			return nil
		},
	}
}

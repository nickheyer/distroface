package api

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/spf13/cobra"
)

func newIssueCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "issue",
		Short: "Manage issues, addressed as namespace/name plus number",
	}
	cmd.AddCommand(
		newIssueListCmd(),
		newIssueCreateCmd(),
		newIssueViewCmd(),
		newIssueStateCmd("close", v1.IssueState_ISSUE_STATE_CLOSED),
		newIssueStateCmd("reopen", v1.IssueState_ISSUE_STATE_OPEN),
		newIssueCommentCmd(),
		newIssueLabelCmd(),
		newIssueAssignCmd(),
	)
	return cmd
}

func issueState(s string) (v1.IssueState, error) {
	switch strings.ToLower(s) {
	case "", "open":
		return v1.IssueState_ISSUE_STATE_OPEN, nil
	case "closed":
		return v1.IssueState_ISSUE_STATE_CLOSED, nil
	}
	return 0, fmt.Errorf("state must be open or closed")
}

func issueRow(i *v1.Issue) []string {
	labels := make([]string, len(i.GetLabels()))
	for n, l := range i.GetLabels() {
		labels[n] = l.GetName()
	}
	return []string{
		"#" + strconv.FormatInt(i.GetNumber(), 10),
		enumLabel(i.GetState(), "ISSUE_STATE_"),
		i.GetTitle(),
		i.GetAuthor().GetUsername(),
		strings.Join(labels, ","),
		fmtTime(i.GetUpdatedAt()),
	}
}

var issueHeader = []string{"NUMBER", "STATE", "TITLE", "AUTHOR", "LABELS", "UPDATED"}

func newIssueListCmd() *cobra.Command {
	var state, assignee, author string
	var labels []string
	var table bool

	cmd := &cobra.Command{
		Use:   "list namespace/name",
		Short: "List issues",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := gitRepoArg(args[0], "")
			if err != nil {
				return err
			}
			st, err := issueState(state)
			if err != nil {
				return err
			}
			isPull := false
			issues, err := allPages(func(token string) ([]*v1.Issue, string, error) {
				resp, err := client.Issues().ListIssues(cmd.Context(), &v1.ListIssuesRequest{
					Page:      &v1.PageRequest{PageSize: maxPageSize, PageToken: token},
					Namespace: ref.Namespace,
					Name:      ref.Name,
					State:     st,
					Labels:    labels,
					Assignee:  assignee,
					Author:    author,
					IsPull:    &isPull,
				})
				if err != nil {
					return nil, "", rpcErr(err)
				}
				return resp.GetIssues(), resp.GetPage().GetNextPageToken(), nil
			})
			if err != nil {
				return err
			}
			return printOrTable(table, issues, issueHeader, issueRow)
		},
	}

	cmd.Flags().StringVar(&state, "state", "open", "Filter by state (open or closed)")
	cmd.Flags().StringSliceVar(&labels, "label", nil, "Require these labels")
	cmd.Flags().StringVar(&assignee, "assignee", "", "Filter by assignee username")
	cmd.Flags().StringVar(&author, "author", "", "Filter by author username")
	cmd.Flags().BoolVarP(&table, "table", "t", false, "Format results as a table")
	return cmd
}

func newIssueCreateCmd() *cobra.Command {
	var title, body string

	cmd := &cobra.Command{
		Use:   "create namespace/name",
		Short: "Open an issue",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := gitRepoArg(args[0], "")
			if err != nil {
				return err
			}
			if title == "" {
				return fmt.Errorf("--title is required")
			}
			resp, err := client.Issues().CreateIssue(cmd.Context(), &v1.CreateIssueRequest{
				Namespace: ref.Namespace,
				Name:      ref.Name,
				Title:     title,
				Body:      body,
			})
			if err != nil {
				return rpcErr(err)
			}
			return printProto(resp.GetIssue())
		},
	}

	cmd.Flags().StringVar(&title, "title", "", "Issue title")
	cmd.Flags().StringVar(&body, "body", "", "Issue body")
	return cmd
}

func getIssue(ctx context.Context, ref RepoRef, number int64) (*v1.Issue, error) {
	resp, err := client.Issues().GetIssue(ctx, &v1.GetIssueRequest{
		Namespace: ref.Namespace,
		Name:      ref.Name,
		Number:    number,
	})
	if err != nil {
		return nil, rpcErr(err)
	}
	return resp.GetIssue(), nil
}

func newIssueViewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "view namespace/name number",
		Short: "Show an issue",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, number, err := repoNumberArgs(args)
			if err != nil {
				return err
			}
			issue, err := getIssue(cmd.Context(), ref, number)
			if err != nil {
				return err
			}
			return printProto(issue)
		},
	}
}

func newIssueStateCmd(verb string, state v1.IssueState) *cobra.Command {
	return &cobra.Command{
		Use:   verb + " namespace/name number",
		Short: strings.ToUpper(verb[:1]) + verb[1:] + " an issue",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, number, err := repoNumberArgs(args)
			if err != nil {
				return err
			}
			resp, err := client.Issues().UpdateIssue(cmd.Context(), &v1.UpdateIssueRequest{
				Namespace: ref.Namespace,
				Name:      ref.Name,
				Number:    number,
				State:     &state,
			})
			if err != nil {
				return rpcErr(err)
			}
			fmt.Printf("Issue #%d is now %s\n", number, enumLabel(resp.GetIssue().GetState(), "ISSUE_STATE_"))
			return nil
		},
	}
}

func newIssueCommentCmd() *cobra.Command {
	var body string

	cmd := &cobra.Command{
		Use:   "comment namespace/name number [body]",
		Short: "Comment on an issue or pull request",
		Args:  cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, number, err := repoNumberArgs(args)
			if err != nil {
				return err
			}
			if len(args) == 3 {
				body = args[2]
			}
			if body == "" {
				return fmt.Errorf("comment body is empty")
			}
			resp, err := client.Issues().CreateComment(cmd.Context(), &v1.CreateCommentRequest{
				Namespace: ref.Namespace,
				Name:      ref.Name,
				Number:    number,
				Body:      body,
			})
			if err != nil {
				return rpcErr(err)
			}
			return printProto(resp.GetComment())
		},
	}

	cmd.Flags().StringVar(&body, "body", "", "Comment body")
	return cmd
}

// Label names to ids, unknown names fail
func resolveLabelIDs(ctx context.Context, ref RepoRef, names []string) ([]string, error) {
	labels, err := allPages(func(token string) ([]*v1.Label, string, error) {
		resp, err := client.Issues().ListLabels(ctx, &v1.ListLabelsRequest{
			Page:      &v1.PageRequest{PageSize: maxPageSize, PageToken: token},
			Namespace: ref.Namespace,
			Name:      ref.Name,
		})
		if err != nil {
			return nil, "", rpcErr(err)
		}
		return resp.GetLabels(), resp.GetPage().GetNextPageToken(), nil
	})
	if err != nil {
		return nil, err
	}
	byName := make(map[string]string, len(labels))
	for _, l := range labels {
		byName[strings.ToLower(l.GetName())] = l.GetId()
	}
	ids := make([]string, 0, len(names))
	for _, name := range names {
		id, ok := byName[strings.ToLower(name)]
		if !ok {
			return nil, fmt.Errorf("label %q does not exist in %s", name, ref)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func resolveUserID(ctx context.Context, username string) (string, error) {
	resp, err := client.Users().GetUser(ctx, &v1.GetUserRequest{Username: username})
	if err != nil {
		return "", rpcErr(err)
	}
	return resp.GetUser().GetId(), nil
}

// Adds or removes ids against the current set
func mergeIDs(current, changes []string, remove bool) []string {
	drop := map[string]bool{}
	for _, id := range changes {
		drop[id] = true
	}
	out := make([]string, 0, len(current)+len(changes))
	for _, id := range current {
		if !drop[id] {
			out = append(out, id)
		}
	}
	if remove {
		return out
	}
	return append(out, changes...)
}

func newIssueLabelCmd() *cobra.Command {
	var remove bool

	cmd := &cobra.Command{
		Use:   "label namespace/name number label...",
		Short: "Add labels to an issue, --remove drops them",
		Args:  cobra.MinimumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, number, err := repoNumberArgs(args)
			if err != nil {
				return err
			}
			ids, err := resolveLabelIDs(cmd.Context(), ref, args[2:])
			if err != nil {
				return err
			}
			issue, err := getIssue(cmd.Context(), ref, number)
			if err != nil {
				return err
			}
			current := make([]string, 0, len(issue.GetLabels()))
			for _, l := range issue.GetLabels() {
				current = append(current, l.GetId())
			}
			resp, err := client.Issues().UpdateIssue(cmd.Context(), &v1.UpdateIssueRequest{
				Namespace: ref.Namespace,
				Name:      ref.Name,
				Number:    number,
				SetLabels: true,
				LabelIds:  mergeIDs(current, ids, remove),
			})
			if err != nil {
				return rpcErr(err)
			}
			return printProto(resp.GetIssue())
		},
	}

	cmd.Flags().BoolVar(&remove, "remove", false, "Remove the labels instead")
	return cmd
}

func newIssueAssignCmd() *cobra.Command {
	var remove bool

	cmd := &cobra.Command{
		Use:   "assign namespace/name number username...",
		Short: "Assign users to an issue, --remove unassigns them",
		Args:  cobra.MinimumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, number, err := repoNumberArgs(args)
			if err != nil {
				return err
			}
			ids := make([]string, 0, len(args)-2)
			for _, username := range args[2:] {
				id, err := resolveUserID(cmd.Context(), username)
				if err != nil {
					return err
				}
				ids = append(ids, id)
			}
			issue, err := getIssue(cmd.Context(), ref, number)
			if err != nil {
				return err
			}
			current := make([]string, 0, len(issue.GetAssignees()))
			for _, u := range issue.GetAssignees() {
				current = append(current, u.GetId())
			}
			resp, err := client.Issues().UpdateIssue(cmd.Context(), &v1.UpdateIssueRequest{
				Namespace:    ref.Namespace,
				Name:         ref.Name,
				Number:       number,
				SetAssignees: true,
				AssigneeIds:  mergeIDs(current, ids, remove),
			})
			if err != nil {
				return rpcErr(err)
			}
			return printProto(resp.GetIssue())
		},
	}

	cmd.Flags().BoolVar(&remove, "remove", false, "Unassign the users instead")
	return cmd
}

package api

import (
	"fmt"
	"strconv"
	"strings"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/spf13/cobra"
)

func newPRCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pr",
		Short: "Manage pull requests, addressed as namespace/name plus number",
	}
	cmd.AddCommand(
		newPRListCmd(),
		newPRCreateCmd(),
		newPRViewCmd(),
		newPRCheckoutCmd(),
		newPRMergeCmd(),
		newPRReviewCmd(),
		newPRCloseCmd(),
	)
	return cmd
}

var mergeMethods = map[string]v1.GitMergeMethod{
	"merge":        v1.GitMergeMethod_GIT_MERGE_METHOD_MERGE,
	"squash":       v1.GitMergeMethod_GIT_MERGE_METHOD_SQUASH,
	"rebase":       v1.GitMergeMethod_GIT_MERGE_METHOD_REBASE,
	"rebase-merge": v1.GitMergeMethod_GIT_MERGE_METHOD_REBASE_MERGE,
	"ff":           v1.GitMergeMethod_GIT_MERGE_METHOD_FAST_FORWARD,
}

func newPRListCmd() *cobra.Command {
	var state, base, head, author string
	var table bool

	cmd := &cobra.Command{
		Use:   "list namespace/name",
		Short: "List pull requests",
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
			pulls, err := allPages(func(token string) ([]*v1.PullRequest, string, error) {
				resp, err := client.Pulls().ListPullRequests(cmd.Context(), &v1.ListPullRequestsRequest{
					Page:      &v1.PageRequest{PageSize: maxPageSize, PageToken: token},
					Namespace: ref.Namespace,
					Name:      ref.Name,
					State:     st,
					Author:    author,
					Base:      base,
					Head:      head,
				})
				if err != nil {
					return nil, "", rpcErr(err)
				}
				return resp.GetPullRequests(), resp.GetPage().GetNextPageToken(), nil
			})
			if err != nil {
				return err
			}
			header := []string{"NUMBER", "STATE", "TITLE", "AUTHOR", "HEAD", "BASE", "UPDATED"}
			return printOrTable(table, pulls, header, func(p *v1.PullRequest) []string {
				i := p.GetIssue()
				state := enumLabel(i.GetState(), "ISSUE_STATE_")
				if p.GetMergedAt() != nil {
					state = "merged"
				}
				return []string{
					"#" + strconv.FormatInt(i.GetNumber(), 10),
					state,
					i.GetTitle(),
					i.GetAuthor().GetUsername(),
					p.GetHeadBranch(),
					p.GetBaseBranch(),
					fmtTime(i.GetUpdatedAt()),
				}
			})
		},
	}

	cmd.Flags().StringVar(&state, "state", "open", "Filter by state (open or closed)")
	cmd.Flags().StringVar(&base, "base", "", "Filter by base branch")
	cmd.Flags().StringVar(&head, "head", "", "Filter by head branch")
	cmd.Flags().StringVar(&author, "author", "", "Filter by author username")
	cmd.Flags().BoolVarP(&table, "table", "t", false, "Format results as a table")
	return cmd
}

func newPRCreateCmd() *cobra.Command {
	var base, head, title, body string
	var draft bool

	cmd := &cobra.Command{
		Use:   "create namespace/name",
		Short: "Open a pull request",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := gitRepoArg(args[0], "")
			if err != nil {
				return err
			}
			if head == "" || title == "" {
				return fmt.Errorf("--head and --title are required")
			}
			resp, err := client.Pulls().CreatePullRequest(cmd.Context(), &v1.CreatePullRequestRequest{
				Namespace: ref.Namespace,
				Name:      ref.Name,
				Base:      base,
				Head:      head,
				Title:     title,
				Body:      body,
				Draft:     draft,
			})
			if err != nil {
				return rpcErr(err)
			}
			return printProto(resp.GetPullRequest())
		},
	}

	cmd.Flags().StringVar(&base, "base", "", "Base branch, defaults to the default branch")
	cmd.Flags().StringVar(&head, "head", "", "Head branch, or namespace/name:branch for a fork")
	cmd.Flags().StringVar(&title, "title", "", "Pull request title")
	cmd.Flags().StringVar(&body, "body", "", "Pull request body")
	cmd.Flags().BoolVar(&draft, "draft", false, "Open as a draft")
	return cmd
}

func getPull(cmd *cobra.Command, args []string) (RepoRef, int64, *v1.PullRequest, error) {
	ref, number, err := repoNumberArgs(args)
	if err != nil {
		return ref, 0, nil, err
	}
	resp, err := client.Pulls().GetPullRequest(cmd.Context(), &v1.GetPullRequestRequest{
		Namespace: ref.Namespace,
		Name:      ref.Name,
		Number:    number,
	})
	if err != nil {
		return ref, 0, nil, rpcErr(err)
	}
	return ref, number, resp.GetPullRequest(), nil
}

func newPRViewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "view namespace/name number",
		Short: "Show a pull request",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, _, pr, err := getPull(cmd, args)
			if err != nil {
				return err
			}
			return printProto(pr)
		},
	}
}

func newPRCheckoutCmd() *cobra.Command {
	var remote string

	cmd := &cobra.Command{
		Use:   "checkout namespace/name number",
		Short: "Fetch the pull request head into a local pr-<n> branch and check it out",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, number, pr, err := getPull(cmd, args)
			if err != nil {
				return err
			}
			headRef := pr.GetHeadRef()
			if headRef == "" {
				headRef = fmt.Sprintf("pull/%d/head", number)
			}
			branch := fmt.Sprintf("pr-%d", number)
			if err := runGit(cmd.Context(), "fetch", remote, "+"+headRef+":"+branch); err != nil {
				return err
			}
			return runGit(cmd.Context(), "checkout", branch)
		},
	}

	cmd.Flags().StringVar(&remote, "remote", "origin", "Remote that points at the server")
	return cmd
}

func newPRMergeCmd() *cobra.Command {
	var method, title, message string
	var deleteBranch bool

	cmd := &cobra.Command{
		Use:   "merge namespace/name number",
		Short: "Merge a pull request",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, number, err := repoNumberArgs(args)
			if err != nil {
				return err
			}
			var mergeMethod v1.GitMergeMethod
			if method != "" {
				m, ok := mergeMethods[strings.ToLower(method)]
				if !ok {
					return fmt.Errorf("method must be one of merge, squash, rebase, rebase-merge, ff")
				}
				mergeMethod = m
			}
			resp, err := client.Pulls().MergePullRequest(cmd.Context(), &v1.MergePullRequestRequest{
				Namespace:    ref.Namespace,
				Name:         ref.Name,
				Number:       number,
				Method:       mergeMethod,
				Title:        title,
				Message:      message,
				DeleteBranch: deleteBranch,
			})
			if err != nil {
				return rpcErr(err)
			}
			fmt.Printf("Merged #%d as %s\n", number, resp.GetMergeCommitSha())
			return nil
		},
	}

	cmd.Flags().StringVar(&method, "method", "", "Merge method (merge, squash, rebase, rebase-merge, ff), defaults to the repository setting")
	cmd.Flags().StringVar(&title, "title", "", "Merge commit title")
	cmd.Flags().StringVar(&message, "message", "", "Merge commit message body")
	cmd.Flags().BoolVar(&deleteBranch, "delete-branch", false, "Delete the head branch after merging")
	return cmd
}

func newPRReviewCmd() *cobra.Command {
	var approve, requestChanges, comment bool
	var body string

	cmd := &cobra.Command{
		Use:   "review namespace/name number",
		Short: "Submit a review with --approve, --request-changes, or --comment",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, number, err := repoNumberArgs(args)
			if err != nil {
				return err
			}
			var state v1.ReviewState
			set := 0
			for _, pick := range []struct {
				on    bool
				state v1.ReviewState
			}{
				{approve, v1.ReviewState_REVIEW_STATE_APPROVED},
				{requestChanges, v1.ReviewState_REVIEW_STATE_CHANGES_REQUESTED},
				{comment, v1.ReviewState_REVIEW_STATE_COMMENTED},
			} {
				if pick.on {
					state = pick.state
					set++
				}
			}
			if set != 1 {
				return fmt.Errorf("pick exactly one of --approve, --request-changes, --comment")
			}
			if state != v1.ReviewState_REVIEW_STATE_APPROVED && body == "" {
				return fmt.Errorf("--body is required for this review")
			}
			resp, err := client.Pulls().CreateReview(cmd.Context(), &v1.CreateReviewRequest{
				Namespace: ref.Namespace,
				Name:      ref.Name,
				Number:    number,
				State:     state,
				Body:      body,
			})
			if err != nil {
				return rpcErr(err)
			}
			return printProto(resp.GetReview())
		},
	}

	cmd.Flags().BoolVar(&approve, "approve", false, "Approve the pull request")
	cmd.Flags().BoolVar(&requestChanges, "request-changes", false, "Request changes")
	cmd.Flags().BoolVar(&comment, "comment", false, "Leave a review comment without a verdict")
	cmd.Flags().StringVar(&body, "body", "", "Review body")
	return cmd
}

func newPRCloseCmd() *cobra.Command {
	var deleteBranch bool

	cmd := &cobra.Command{
		Use:   "close namespace/name number",
		Short: "Close a pull request without merging",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, number, err := repoNumberArgs(args)
			if err != nil {
				return err
			}
			_, err = client.Pulls().ClosePullRequest(cmd.Context(), &v1.ClosePullRequestRequest{
				Namespace:    ref.Namespace,
				Name:         ref.Name,
				Number:       number,
				DeleteBranch: deleteBranch,
			})
			if err != nil {
				return rpcErr(err)
			}
			fmt.Printf("Closed #%d\n", number)
			return nil
		},
	}

	cmd.Flags().BoolVar(&deleteBranch, "delete-branch", false, "Delete the head branch too")
	return cmd
}

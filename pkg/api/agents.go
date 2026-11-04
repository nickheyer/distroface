package api

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/spf13/cobra"
)

func newAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Work with repository agents, addressed as namespace/name",
		Long: `Start and follow agent sessions on a repository.

Agents are model driven assistants configured by repository, organization, or
instance admins. A session runs on the server through an inference connector
and records a transcript you can stream from here.`,
	}
	cmd.AddCommand(
		newAgentListCmd(),
		newAgentSessionsCmd(),
		newAgentRunCmd(),
		newAgentViewCmd(),
		newAgentWatchCmd(),
		newAgentCancelCmd(),
		newAgentApproveCmd(),
		newAgentContinueCmd(),
	)
	return cmd
}

func agentSessionState(s *v1.AgentSession) string {
	if s.GetStatus() == v1.AgentSessionStatus_AGENT_SESSION_STATUS_COMPLETED {
		return enumLabel(s.GetConclusion(), "AGENT_SESSION_CONCLUSION_")
	}
	return enumLabel(s.GetStatus(), "AGENT_SESSION_STATUS_")
}

func newAgentListCmd() *cobra.Command {
	var table bool
	cmd := &cobra.Command{
		Use:   "list namespace/name",
		Short: "List agents usable by a repository",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := gitRepoArg(args[0], "")
			if err != nil {
				return err
			}
			resp, err := client.Agents().GetRepoAgents(cmd.Context(), &v1.GetRepoAgentsRequest{Namespace: ref.Namespace, Name: ref.Name})
			if err != nil {
				return rpcErr(err)
			}
			if !table {
				return printProto(resp)
			}
			header := []string{"HANDLE", "NAME", "KIND", "SOURCE", "ENABLED", "MODEL"}
			return printOrTable(table, resp.GetAgents(), header, func(a *v1.Agent) []string {
				return []string{
					"@" + a.GetBotUsername(), a.GetName(), enumLabel(a.GetKind(), "AGENT_KIND_"),
					enumLabel(a.GetSource(), "AGENT_SOURCE_"), strconv.FormatBool(a.GetEnabled()), a.GetModel(),
				}
			})
		},
	}
	cmd.Flags().BoolVar(&table, "table", false, "Print a table instead of json")
	return cmd
}

func newAgentSessionsCmd() *cobra.Command {
	var agent, trigger, actor string
	var issue int64
	var table bool
	cmd := &cobra.Command{
		Use:   "sessions namespace/name",
		Short: "List agent sessions of a repository",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := gitRepoArg(args[0], "")
			if err != nil {
				return err
			}
			sessions, err := allPages(func(token string) ([]*v1.AgentSession, string, error) {
				resp, err := client.Agents().ListAgentSessions(cmd.Context(), &v1.ListAgentSessionsRequest{
					Page: &v1.PageRequest{PageSize: maxPageSize, PageToken: token}, Namespace: ref.Namespace, Name: ref.Name,
					Agent: agent, Trigger: trigger, Actor: actor, IssueNumber: issue,
				})
				if err != nil {
					return nil, "", rpcErr(err)
				}
				return resp.GetSessions(), resp.GetPage().GetNextPageToken(), nil
			})
			if err != nil {
				return err
			}
			header := []string{"NUMBER", "ID", "STATE", "AGENT", "TRIGGER", "TARGET", "TITLE", "CREATED"}
			return printOrTable(table, sessions, header, func(s *v1.AgentSession) []string {
				target := ""
				if s.GetIssueNumber() > 0 {
					target = "#" + strconv.FormatInt(s.GetIssueNumber(), 10)
				}
				return []string{
					"#" + strconv.FormatInt(s.GetNumber(), 10), s.GetId(), agentSessionState(s), "@" + s.GetBotUsername(),
					s.GetTrigger(), target, s.GetTitle(), fmtTime(s.GetCreatedAt()),
				}
			})
		},
	}
	cmd.Flags().StringVar(&agent, "agent", "", "Filter by agent handle or id")
	cmd.Flags().StringVar(&trigger, "trigger", "", "Filter by trigger, manual, pull_request, issue, label, mention, review, workflow_run")
	cmd.Flags().StringVar(&actor, "actor", "", "Filter by the user who started the session")
	cmd.Flags().Int64Var(&issue, "issue", 0, "Filter by issue or pull request number")
	cmd.Flags().BoolVar(&table, "table", false, "Print a table instead of json")
	return cmd
}

func newAgentRunCmd() *cobra.Command {
	var prompt, ref, model string
	var issue int64
	var watch bool
	cmd := &cobra.Command{
		Use:   "run namespace/name agent",
		Short: "Start a session for an agent",
		Example: `  dfcli agent run acme/app reviewer --issue 42
  dfcli agent run acme/app coder --prompt "Add a healthz endpoint" --ref main --watch`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, err := gitRepoArg(args[0], "")
			if err != nil {
				return err
			}
			if prompt == "-" {
				raw, err := readAllStdin()
				if err != nil {
					return err
				}
				prompt = raw
			}
			resp, err := client.Agents().StartAgentSession(cmd.Context(), &v1.StartAgentSessionRequest{
				Namespace: repo.Namespace, Name: repo.Name, Agent: strings.TrimPrefix(args[1], "@"), Prompt: prompt, Ref: ref, IssueNumber: issue, Model: model,
			})
			if err != nil {
				return rpcErr(err)
			}
			s := resp.GetSession()
			if !watch {
				return printProto(s)
			}
			fmt.Fprintf(os.Stderr, "Session #%d queued, %s\n", s.GetNumber(), s.GetHtmlUrl())
			return watchSession(cmd, repo, s.GetId())
		},
	}
	cmd.Flags().StringVar(&prompt, "prompt", "", "Instruction for the agent, - reads stdin")
	cmd.Flags().StringVar(&ref, "ref", "", "Branch to work from, default branch when empty")
	cmd.Flags().StringVar(&model, "model", "", "Model override within the connector allowlist")
	cmd.Flags().Int64Var(&issue, "issue", 0, "Issue or pull request number to target")
	cmd.Flags().BoolVar(&watch, "watch", false, "Stream the transcript until the session finishes")
	return cmd
}

func newAgentViewCmd() *cobra.Command {
	var transcript bool
	cmd := &cobra.Command{
		Use:   "view namespace/name session-id",
		Short: "Show a session",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, err := gitRepoArg(args[0], "")
			if err != nil {
				return err
			}
			resp, err := client.Agents().GetAgentSession(cmd.Context(), &v1.GetAgentSessionRequest{Namespace: repo.Namespace, Name: repo.Name, Id: args[1]})
			if err != nil {
				return rpcErr(err)
			}
			if !transcript {
				return printProto(resp.GetSession())
			}
			events, err := allPages(func(token string) ([]*v1.AgentSessionEvent, string, error) {
				r, err := client.Agents().ListAgentSessionEvents(cmd.Context(), &v1.ListAgentSessionEventsRequest{
					Page: &v1.PageRequest{PageSize: maxPageSize, PageToken: token}, Namespace: repo.Namespace, Name: repo.Name, Id: args[1],
				})
				if err != nil {
					return nil, "", rpcErr(err)
				}
				return r.GetEvents(), r.GetPage().GetNextPageToken(), nil
			})
			if err != nil {
				return err
			}
			for _, ev := range events {
				printAgentEvent(ev)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&transcript, "transcript", false, "Print the transcript instead of the session")
	return cmd
}

func newAgentWatchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "watch namespace/name session-id",
		Short: "Stream a session's transcript until it finishes",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, err := gitRepoArg(args[0], "")
			if err != nil {
				return err
			}
			return watchSession(cmd, repo, args[1])
		},
	}
}

func watchSession(cmd *cobra.Command, repo RepoRef, id string) error {
	stream, err := client.Agents().StreamAgentSessionEvents(cmd.Context(), &v1.StreamAgentSessionEventsRequest{Namespace: repo.Namespace, Name: repo.Name, Id: id})
	if err != nil {
		return rpcErr(err)
	}
	defer stream.Close()
	for {
		ev, err := stream.Receive()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return rpcErr(err)
		}
		if ev.GetDone() {
			fmt.Fprintf(os.Stderr, "Session %s\n", enumLabel(ev.GetStatus(), "AGENT_SESSION_STATUS_"))
			return nil
		}
		printAgentEvent(ev)
	}
}

// One transcript event as a terminal line, long tool output trimmed
func printAgentEvent(ev *v1.AgentSessionEvent) {
	kind := enumLabel(ev.GetKind(), "AGENT_EVENT_KIND_")
	text := strings.TrimSpace(ev.GetText())
	switch ev.GetKind() {
	case v1.AgentEventKind_AGENT_EVENT_KIND_USAGE, v1.AgentEventKind_AGENT_EVENT_KIND_SYSTEM:
		return
	case v1.AgentEventKind_AGENT_EVENT_KIND_TOOL_CALL:
		fmt.Printf("[%s] %s %s\n", kind, ev.GetTool(), firstLineOf(ev.GetData(), 160))
		return
	case v1.AgentEventKind_AGENT_EVENT_KIND_TOOL_RESULT:
		if len(text) > 400 {
			text = text[:400] + "..."
		}
		fmt.Printf("[%s] %s: %s\n", kind, ev.GetTool(), text)
		return
	}
	if text == "" {
		return
	}
	fmt.Printf("[%s] %s\n", kind, text)
}

func firstLineOf(s string, max int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > max {
		s = s[:max] + "..."
	}
	return s
}

func newAgentCancelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cancel namespace/name session-id",
		Short: "Cancel a queued, running, or waiting session",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, err := gitRepoArg(args[0], "")
			if err != nil {
				return err
			}
			resp, err := client.Agents().CancelAgentSession(cmd.Context(), &v1.CancelAgentSessionRequest{Namespace: repo.Namespace, Name: repo.Name, Id: args[1]})
			if err != nil {
				return rpcErr(err)
			}
			return printProto(resp.GetSession())
		},
	}
}

func newAgentApproveCmd() *cobra.Command {
	var reject bool
	var note string
	cmd := &cobra.Command{
		Use:   "approve namespace/name session-id",
		Short: "Approve or reject the action a session is waiting on",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, err := gitRepoArg(args[0], "")
			if err != nil {
				return err
			}
			cur, err := client.Agents().GetAgentSession(cmd.Context(), &v1.GetAgentSessionRequest{Namespace: repo.Namespace, Name: repo.Name, Id: args[1]})
			if err != nil {
				return rpcErr(err)
			}
			pending := cur.GetSession().GetPendingApproval()
			if pending == nil {
				return fmt.Errorf("session is not waiting for approval")
			}
			resp, err := client.Agents().ResolveAgentApproval(cmd.Context(), &v1.ResolveAgentApprovalRequest{
				Namespace: repo.Namespace, Name: repo.Name, Id: args[1], ApprovalId: pending.GetId(), Approve: !reject, Note: note,
			})
			if err != nil {
				return rpcErr(err)
			}
			return printProto(resp.GetSession())
		},
	}
	cmd.Flags().BoolVar(&reject, "reject", false, "Reject instead of approving")
	cmd.Flags().StringVar(&note, "note", "", "Note shown to the agent")
	return cmd
}

func newAgentContinueCmd() *cobra.Command {
	var watch bool
	cmd := &cobra.Command{
		Use:   "continue namespace/name session-id instruction",
		Short: "Send a follow up instruction to a finished session",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, err := gitRepoArg(args[0], "")
			if err != nil {
				return err
			}
			resp, err := client.Agents().ContinueAgentSession(cmd.Context(), &v1.ContinueAgentSessionRequest{Namespace: repo.Namespace, Name: repo.Name, Id: args[1], Prompt: args[2]})
			if err != nil {
				return rpcErr(err)
			}
			if !watch {
				return printProto(resp.GetSession())
			}
			return watchSession(cmd, repo, args[1])
		},
	}
	cmd.Flags().BoolVar(&watch, "watch", false, "Stream the transcript until the session finishes")
	return cmd
}

// Whole of standard input as a string
func readAllStdin() (string, error) {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

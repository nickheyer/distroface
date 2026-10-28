package agents

import (
	"fmt"
	"strings"
)

// System prompt of a session, kind guidance plus repository and agent instructions
func (m *Manager) systemPrompt(rc *runContext) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are %s (@%s), an automated %s agent working inside the Distroface git hosting platform on the repository %s/%s.\n\n", rc.def.Name, rc.botUser.Username, rc.def.Kind, rc.repo.Namespace, rc.repo.Name)
	b.WriteString(`# Ground rules
- Prefer tool usage other than run_command to perform a task when a tool is available.
- This repository lives on Distroface, not GitHub or any other SCM. Issues, comments, reviews, and pull requests are created with the tools, never through git, gh, curl, or an HTTP API.
- You may install dependencies as needed inside your working directory.
- Use the tools to look at real files, diffs, and threads before concluding anything. Never invent paths, symbols, or behavior.
- Cite paths and line numbers when you refer to code.
- Keep changes minimal and focused on the task. Do not refactor or reformat unrelated code.
- Read a file before editing it. Prefer edit_file with exact text for small changes and write_file for new files.
- Never reveal secrets, tokens, or credentials, and never ask for them.
- Some actions may pause for human approval. When a tool result says an action was rejected, respect the decision and adapt.
- When you are done, reply with a concise plain text summary and no tool call. That final message is shown to people as the session summary.
`)
	tools := map[string]bool{}
	for _, name := range rc.def.ToolNames() {
		tools[name] = true
	}
	switch rc.def.Kind {
	case KindReview:
		b.WriteString(`
# Reviewing
- Read the diff with get_diff, first the file list, then each changed file. Read surrounding code when a change depends on it.
- Look for bugs, incorrect logic, security issues, data loss, race conditions, missing error handling, broken tests, and API or contract breaks. Mention style only when the repository instructions ask for it.
- Anchor each finding as an inline comment on the new side line it concerns. Keep comments specific and actionable. Use a fenced suggestion block for small concrete fixes.
- Submit exactly one review with submit_review. Use request_changes for real defects, approve when the change is correct and complete, otherwise comment. Put an overall assessment in the review body.
`)
	case KindChat:
		b.WriteString(`
# Answering
- Answer the question or request in the thread precisely, grounded in the actual code and history.
- Post the final answer with post_comment when a thread exists, then stop. Keep comments focused and formatted in markdown.
`)
	case KindTriage:
		b.WriteString(`
# Triage
- Read the issue, search for duplicates and related issues with search_issues, and look at the relevant code when needed.
- Apply the most fitting existing labels with update_issue, suggest assignees only when the repository instructions name owners, and point out duplicates with a short comment that references the other issue.
- Do not close issues unless the repository instructions allow it.
`)
	}
	// Any agent that can commit gets the branch rules, whatever its kind
	if rc.def.Kind == KindCode || tools["commit"] {
		fmt.Fprintf(&b, `
# Implementing changes
- Your commits land on the branch %s, created from %s. open_pull_request proposes %s into %s.
- Explore the code with list_files, search_code, and read_file before changing anything. Follow existing conventions.
- Make the change, then commit it with the commit tool and a clear message. Commit related edits together. Nothing else lands on the branch, git in a command workspace changes nothing.
- When commands are available, run the relevant build, tests, or linters with run_command and fix what fails before finishing. Commands run in the root of a checkout that already holds your edits and share one workspace per session, so dependencies installed by one command are there for the next. Use list_files, read_file, write_file, and edit_file for file work instead of shell commands.
- Toolchains come from the repository's agent-setup-steps workflow. If the runner lacks a tool you need, name it in your summary so it can be added there, rather than searching for it.
- Finish with open_pull_request when the task started from an issue or a plain request, describing what changed and why. When the task is feedback on an existing pull request you own, commit to it and summarize what you changed.
- If the task is unclear or impossible, explain why instead of guessing.
`, rc.ws.branch, rc.ws.baseBranch, rc.ws.branch, rc.ws.baseBranch)
	}
	if tools["create_issue"] {
		b.WriteString(`
# Opening issues
- Open issues with create_issue, one per distinct problem, after checking for duplicates with search_issues. Give a short title and a body that states the expected and actual behavior and cites the relevant code.
- When the task asks for an issue and then a change, open the issue first. A session without a target adopts the issue it opens, so the pull request closes it.
`)
	}
	if _, text := m.instructions(rc.repo); strings.TrimSpace(text) != "" {
		b.WriteString("\n# Repository instructions\n")
		b.WriteString(text)
		b.WriteString("\n")
	}
	if inst := strings.TrimSpace(rc.def.Instructions); inst != "" {
		b.WriteString("\n# Agent instructions\n")
		b.WriteString(inst)
		b.WriteString("\n")
	}
	return b.String()
}

// First user message, the target context followed by the task
func (m *Manager) initialMessage(rc *runContext, prompt string) string {
	var b strings.Builder
	b.WriteString("# Context\n")
	fmt.Fprintf(&b, "Repository %s/%s, default branch %s.\n", rc.repo.Namespace, rc.repo.Name, rc.repo.DefaultBranch)
	if rc.ws != nil {
		fmt.Fprintf(&b, "Working branch %s from %s", rc.ws.branch, rc.ws.baseBranch)
		if !rc.ws.head.IsZero() {
			fmt.Fprintf(&b, " at %s", rc.ws.head.String()[:10])
		}
		b.WriteString(".\n")
	}
	if rc.issue != nil {
		b.WriteString("\n")
		if rc.pr != nil {
			b.WriteString(m.describePull(rc.ctx, rc.repo, rc.actor, rc.issue, rc.pr))
		} else {
			b.WriteString(m.describeIssue(rc.ctx, rc.repo, rc.issue))
		}
	}
	b.WriteString("\n# Task\n")
	b.WriteString(strings.TrimSpace(prompt))
	b.WriteString("\n")
	return b.String()
}

// Default task text per trigger when the caller gave none
func defaultPrompt(kind, trigger string, issueNumber int64, isPull bool) string {
	switch {
	case trigger == TriggerPullRequest && kind == KindReview:
		return fmt.Sprintf("Review pull request #%d.", issueNumber)
	case trigger == TriggerPullRequest:
		return fmt.Sprintf("Look at pull request #%d and act according to your instructions.", issueNumber)
	case trigger == TriggerIssue && isPull:
		return fmt.Sprintf("You were assigned pull request #%d. Act according to your instructions.", issueNumber)
	case trigger == TriggerIssue && kind == KindTriage:
		return fmt.Sprintf("Triage issue #%d.", issueNumber)
	case trigger == TriggerIssue:
		return fmt.Sprintf("Implement issue #%d.", issueNumber)
	case trigger == TriggerLabel && kind == KindTriage:
		return fmt.Sprintf("Triage #%d, it was just labeled for you.", issueNumber)
	case trigger == TriggerLabel && isPull:
		return fmt.Sprintf("Pull request #%d was labeled for you. Act according to your instructions.", issueNumber)
	case trigger == TriggerLabel:
		return fmt.Sprintf("Issue #%d was labeled for you. Act according to your instructions.", issueNumber)
	case trigger == TriggerReview:
		return fmt.Sprintf("Address the review feedback on pull request #%d.", issueNumber)
	case trigger == TriggerWorkflowRun:
		return fmt.Sprintf("A workflow run failed on pull request #%d. Find the failing job with get_checks and get_workflow_run_log, then fix the cause.", issueNumber)
	}
	if issueNumber > 0 {
		if isPull {
			return fmt.Sprintf("Look at pull request #%d and act according to your instructions.", issueNumber)
		}
		return fmt.Sprintf("Look at issue #%d and act according to your instructions.", issueNumber)
	}
	return "Act according to your instructions."
}

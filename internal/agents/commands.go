package agents

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/filemode"
	"github.com/nickheyer/distroface/internal/actions"
	"github.com/nickheyer/distroface/internal/db"
)

const (
	// Grace past the runner timeout before giving up
	commandWaitGrace = 5 * time.Minute
	// Job tokens live a day, the workspace ends first
	commandJobMaxMinutes = 23 * 60
)

// Live job running the session's commands, checkout persists
type commandWorkspace struct {
	runID     string
	runNumber int64
	jobID     string
	url       string
	// Where the checkout sat after the last successful command
	branch string
	head   plumbing.Hash
}

// Runs a script in the command workspace, returns output
func toolRunCommand(rc *runContext, args map[string]any) (string, error) {
	if rc.m.actions == nil {
		return "", fmt.Errorf("actions are not available on this instance")
	}
	if !rc.settings.GetAllowCommands() {
		return "", fmt.Errorf("commands are disabled by the agents policy")
	}
	if !rc.m.actions.Enabled(rc.ctx, rc.repo) {
		return "", fmt.Errorf("commands need Actions enabled on this repository")
	}
	command := strings.TrimSpace(argString(args, "command"))
	if command == "" {
		return "", fmt.Errorf("a command is required")
	}
	// Zero cap trusts the model, zero from both means no limit
	timeout := max(argInt(args, "timeout_minutes"), 0)
	if capMinutes := int(rc.settings.GetCommandTimeoutMinutes()); capMinutes > 0 && (timeout <= 0 || timeout > capMinutes) {
		timeout = capMinutes
	}
	runsOn := commandRunsOn(rc)
	labels := strings.Join(runsOn, ", ")
	cw, err := rc.m.ensureCommandWorkspace(rc, runsOn)
	if err != nil {
		return "", err
	}
	script, branch, head := commandScript(rc, cw, command)
	exec, err := rc.m.actions.EnqueueExec(rc.ctx, cw.jobID, script, timeout)
	if err != nil {
		if errors.Is(err, actions.ErrConflict) {
			// Workspace closed under us, next call opens a fresh one
			rc.cmd = nil
			return "", fmt.Errorf("the command workspace closed, run the command again to open a new one")
		}
		return "", fmt.Errorf("could not queue the command: %w", err)
	}
	rc.m.record(rc.ctx, rc.sess, EventStatus, fmt.Sprintf("Running command %d in Actions workflow run #%d", exec.Number, cw.runNumber), "run_command", "",
		map[string]any{"run_id": cw.runID, "run_number": cw.runNumber, "job_id": cw.jobID, "exec_id": exec.ID, "command_number": exec.Number, "url": cw.url})

	waitCtx := rc.ctx
	if timeout > 0 {
		var cancel func()
		waitCtx, cancel = context.WithTimeout(rc.ctx, time.Duration(timeout)*time.Minute+commandWaitGrace)
		defer cancel()
	}
	done, err := rc.m.actions.WaitExec(waitCtx, exec.ID)
	if err != nil {
		if rc.ctx.Err() != nil {
			return "", rc.ctx.Err()
		}
		if errors.Is(err, context.DeadlineExceeded) {
			// Runner stopped answering, a fresh job takes over
			rc.m.abandonWorkspace(rc, cw)
			return "", fmt.Errorf("the command did not finish within %d minutes, is a runner with label %s online?", timeout, labels)
		}
		return "", err
	}
	switch {
	case done.Error != "":
		// Could not run at all, next call opens a fresh one
		rc.cmd = nil
		return "", fmt.Errorf("the command could not run: %s, run it again to open a new workspace", done.Error)
	case done.Conclusion == actions.ConclusionSuccess:
		// Whole script ran, checkout holds tip and every edit
		cw.branch, cw.head = branch, head
	}
	if job, _ := rc.m.actions.GetJob(rc.ctx, cw.jobID); job != nil && job.Status == actions.StatusCompleted {
		rc.cmd = nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "command %d finished with exit code %d (%s) in Actions workflow run #%d (%s)\n", exec.Number, done.ExitCode, done.Conclusion, cw.runNumber, cw.url)
	if done.Output == "" {
		b.WriteString("the command produced no output\n")
	} else {
		b.WriteString(strings.TrimRight(done.Output, "\n"))
		b.WriteByte('\n')
	}
	return b.String(), nil
}

// Drops the workspace, cancelling a job nobody claimed
func (m *Manager) abandonWorkspace(rc *runContext, cw *commandWorkspace) {
	rc.cmd = nil
	job, _ := m.actions.GetJob(rc.ctx, cw.jobID)
	if job == nil || job.Status == actions.StatusCompleted {
		return
	}
	if job.Status == actions.StatusRunning {
		_ = m.actions.RequestJobClose(rc.ctx, cw.jobID)
		return
	}
	if run, _ := m.actions.GetRun(rc.ctx, cw.runID); run != nil {
		_ = m.actions.CancelRun(rc.ctx, rc.repo, run)
	}
}

// Agents label setting, then the actions default
func commandRunsOn(rc *runContext) []string {
	runsOn := splitLabels(rc.settings.GetCommandsRunsOn())
	if len(runsOn) == 0 {
		runsOn = splitLabels(rc.m.git.GitSettings(rc.ctx, rc.repo).GetActions().GetDefaultRunsOn())
	}
	if len(runsOn) == 0 {
		runsOn = []string{"ubuntu-latest"}
	}
	return runsOn
}

// Target branch and tip, the base until a commit lands
func commandTarget(rc *runContext) (string, plumbing.Hash) {
	r, err := rc.openRepo()
	if err != nil {
		return rc.ws.branch, rc.ws.head
	}
	defer r.Close()
	if tip := r.BranchHash(rc.ws.branch); !tip.IsZero() {
		return rc.ws.branch, tip
	}
	return rc.ws.baseBranch, r.BranchHash(rc.ws.baseBranch)
}

// Session job cap, never past its token's life
func commandJobMinutes(rc *runContext) int {
	minutes := int(rc.settings.GetMaxSessionMinutes())
	if minutes <= 0 || minutes > commandJobMaxMinutes {
		return commandJobMaxMinutes
	}
	return minutes
}

// Opens the workspace, adopting a live one after restart
func (m *Manager) ensureCommandWorkspace(rc *runContext, runsOn []string) (*commandWorkspace, error) {
	if rc.cmd != nil {
		if job, _ := m.actions.GetJob(rc.ctx, rc.cmd.jobID); job != nil && commandJobOpen(job) {
			return rc.cmd, nil
		}
		rc.cmd = nil
	}
	if job, run, _ := m.actions.LiveAgentSessionJob(rc.ctx, rc.sess.ID); job != nil && run != nil {
		// Unknown checkout state, the first command syncs it
		rc.cmd = &commandWorkspace{runID: run.ID, runNumber: run.Number, jobID: job.ID, url: m.actions.RunURL(rc.ctx, rc.repo, run.ID)}
		return rc.cmd, nil
	}
	labels := strings.Join(runsOn, ", ")
	online, registered := m.actions.RunnerOnlineFor(rc.ctx, rc.repo, runsOn, actions.CapabilityExec)
	switch {
	case !registered:
		return nil, fmt.Errorf("no registered runner carries the label %s for this repository, its organization, or the instance, commands cannot run until one is registered", labels)
	case !online:
		return nil, fmt.Errorf("no online runner with the label %s supports command workspaces, start the runner or upgrade dfcli, commands cannot run until then", labels)
	}
	branch, sha := commandTarget(rc)
	opts := actions.AgentRunOptions{
		Session:        actions.AgentRunInfo{SessionID: rc.sess.ID, SessionNumber: rc.sess.Number, AgentSlug: rc.def.Slug},
		Branch:         branch,
		RunsOn:         runsOn,
		TimeoutMinutes: commandJobMinutes(rc),
	}
	if !sha.IsZero() {
		opts.SHA = sha.String()
	}
	run, job, err := m.actions.StartAgentSessionRun(rc.ctx, rc.repo, rc.bot, opts)
	if err != nil {
		return nil, fmt.Errorf("could not open the command workspace: %w", err)
	}
	url := m.actions.RunURL(rc.ctx, rc.repo, run.ID)
	m.record(rc.ctx, rc.sess, EventStatus, fmt.Sprintf("Opened command workspace in Actions workflow run #%d", run.Number), "run_command", "",
		map[string]any{"run_id": run.ID, "run_number": run.Number, "job_id": job.ID, "url": url})
	rc.cmd = &commandWorkspace{runID: run.ID, runNumber: run.Number, jobID: job.ID, url: url, branch: branch, head: sha}
	return rc.cmd, nil
}

func commandJobOpen(j *db.ActionsJob) bool {
	switch j.Status {
	case actions.StatusWaiting, actions.StatusQueued, actions.StatusRunning:
		return !j.CloseRequested
	}
	return false
}

// Branch sync and pending edits ahead of the command
func commandScript(rc *runContext, cw *commandWorkspace, command string) (script, branch string, head plumbing.Hash) {
	var b strings.Builder
	branch, head = commandTarget(rc)
	if !head.IsZero() && (cw.head != head || cw.branch != branch) {
		fmt.Fprintf(&b, "git fetch -q --depth=1 origin %s\n", shellQuote("+refs/heads/"+branch+":refs/remotes/origin/"+branch))
		fmt.Fprintf(&b, "git checkout -q --force -B %s %s\n", shellQuote(branch), shellQuote("origin/"+branch))
	}
	for _, p := range rc.ws.pendingPaths() {
		e := rc.ws.edits[p]
		if e.delete {
			fmt.Fprintf(&b, "rm -f -- %s\n", shellQuote(p))
			continue
		}
		if i := strings.LastIndexByte(p, '/'); i >= 0 {
			fmt.Fprintf(&b, "mkdir -p -- %s\n", shellQuote(p[:i]))
		}
		fmt.Fprintf(&b, "printf '%%s' %s | base64 -d > %s\n", shellQuote(base64.StdEncoding.EncodeToString(e.content)), shellQuote(p))
		if e.mode == filemode.Executable {
			fmt.Fprintf(&b, "chmod +x -- %s\n", shellQuote(p))
		}
	}
	// Errexit guards only the sync, the script runs whole
	b.WriteString("set +e\n")
	b.WriteString(command)
	return b.String(), branch, head
}

// Comma separated labels as a trimmed list
func splitLabels(s string) []string {
	var out []string
	for part := range strings.SplitSeq(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

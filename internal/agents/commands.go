package agents

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nickheyer/distroface/internal/actions"
	"github.com/nickheyer/distroface/internal/db"
	"gopkg.in/yaml.v3"
)

// Command job pacing
const (
	commandPollInterval = 2 * time.Second
	// Engine needs a number, a year reads as none
	noJobTimeoutMinutes = 60 * 24 * 365
)

// Runs a script on a workflow runner and returns the job log tail
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
	runsOn := splitLabels(rc.settings.GetCommandsRunsOn())
	if len(runsOn) == 0 {
		runsOn = splitLabels(rc.m.git.GitSettings(rc.ctx, rc.repo).GetActions().GetDefaultRunsOn())
	}
	if len(runsOn) == 0 {
		runsOn = []string{"ubuntu-latest"}
	}
	labels := strings.Join(runsOn, ", ")
	// Refuse before creating a job nobody can claim
	if !rc.m.actions.RunnerAvailable(rc.ctx, rc.repo, runsOn) {
		return "", fmt.Errorf("no registered runner carries the label %s for this repository, its organization, or the instance, commands cannot run until one is registered", labels)
	}
	// The job checks out the working branch tip, or the base when nothing was committed yet
	ref, sha := rc.ws.branch, ""
	if r, err := rc.openRepo(); err == nil {
		if tip := r.BranchHash(rc.ws.branch); !tip.IsZero() {
			sha = tip.String()
		} else {
			ref = rc.ws.baseBranch
			if tip := r.BranchHash(ref); !tip.IsZero() {
				sha = tip.String()
			}
		}
		r.Close()
	}
	source, err := commandWorkflow(rc, runsOn, timeout, command)
	if err != nil {
		return "", err
	}
	run, err := rc.m.actions.StartInlineRun(rc.ctx, rc.repo, rc.bot, fmt.Sprintf("agent-session-%d.yml", rc.sess.Number), source, ref, sha, map[string]any{
		"agent_session": map[string]any{"id": rc.sess.ID, "number": rc.sess.Number, "agent": rc.def.Slug},
	})
	if err != nil {
		return "", fmt.Errorf("could not start the command job: %w", err)
	}
	url := rc.m.actions.RunURL(rc.ctx, rc.repo, run.ID)
	rc.m.record(rc.ctx, rc.sess, EventStatus, fmt.Sprintf("Running command in Actions workflow run #%d", run.Number), "run_command", "", map[string]any{"run_id": run.ID, "run_number": run.Number, "url": url})
	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().Add(time.Duration(timeout)*time.Minute + 2*time.Minute)
	}
	for {
		select {
		case <-rc.ctx.Done():
			if fresh, _ := rc.m.actions.GetRun(rc.ctx, run.ID); fresh != nil && fresh.Status != actions.StatusCompleted {
				_ = rc.m.actions.CancelRun(rc.m.base, rc.repo, fresh)
			}
			return "", rc.ctx.Err()
		case <-time.After(commandPollInterval):
		}
		fresh, err := rc.m.actions.GetRun(rc.ctx, run.ID)
		if err != nil || fresh == nil {
			return "", fmt.Errorf("the command job disappeared")
		}
		if fresh.Status == actions.StatusCompleted {
			run = fresh
			break
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			_ = rc.m.actions.CancelRun(rc.ctx, rc.repo, fresh)
			return "", fmt.Errorf("the command job did not finish within %d minutes, is a runner with label %s online?", timeout, labels)
		}
	}
	jobs, _ := rc.m.actions.RunJobs(rc.ctx, run.ID)
	var b strings.Builder
	fmt.Fprintf(&b, "command finished with conclusion %s in Actions workflow run #%d (%s)\n", run.Conclusion, run.Number, url)
	logged := false
	for _, j := range jobs {
		for _, note := range jobNotes(j) {
			b.WriteString(note)
			b.WriteByte('\n')
		}
		raw, err := rc.m.actions.JobLog(rc.ctx, j)
		if err != nil || len(raw) == 0 {
			continue
		}
		logged = true
		b.WriteString(cleanLog(string(raw)))
	}
	if !logged {
		b.WriteString("the job produced no log\n")
	}
	return b.String(), nil
}

// Stored annotation shape on a job row
type jobAnnotation struct {
	Level   string `json:"level"`
	Message string `json:"message"`
}

// Stored step shape, only what the summary needs
type jobStep struct {
	Name       string `json:"name"`
	Conclusion string `json:"conclusion"`
}

// Annotations and failed steps as lines for the model
func jobNotes(j *db.ActionsJob) []string {
	var out []string
	var anns []jobAnnotation
	if json.Unmarshal([]byte(j.Annotations), &anns) == nil {
		for _, a := range anns {
			msg := strings.TrimSpace(a.Message)
			if msg == "" {
				continue
			}
			level := strings.TrimSpace(a.Level)
			if level == "" {
				level = "note"
			}
			out = append(out, level+": "+msg)
		}
	}
	var steps []jobStep
	if json.Unmarshal([]byte(j.Steps), &steps) == nil {
		for _, s := range steps {
			switch s.Conclusion {
			case actions.ConclusionFailure, actions.ConclusionTimedOut, actions.ConclusionCancelled:
				out = append(out, fmt.Sprintf("step %q ended with %s", s.Name, s.Conclusion))
			}
		}
	}
	return out
}

// Comma separated labels as a trimmed list
func splitLabels(s string) []string {
	var out []string
	for _, l := range strings.Split(s, ",") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// Strips runner markup lines that carry no information for the model
func cleanLog(s string) string {
	lines := strings.Split(s, "\n")
	out := lines[:0]
	for _, l := range lines {
		switch {
		case l == "##[endgroup]":
			continue
		case strings.HasPrefix(l, "##[group]"):
			out = append(out, "## "+strings.TrimPrefix(l, "##[group]"))
		default:
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// Workflow source that checks out the branch, applies uncommitted edits, and runs the script
func commandWorkflow(rc *runContext, runsOn []string, timeout int, command string) (string, error) {
	steps := []map[string]any{{"uses": "actions/checkout@v4"}}
	if edits := rc.ws.pendingPaths(); len(edits) > 0 {
		var script strings.Builder
		for _, p := range edits {
			e := rc.ws.edits[p]
			if e.delete {
				fmt.Fprintf(&script, "rm -f -- %s\n", shellQuote(p))
				continue
			}
			dir := p
			if i := strings.LastIndexByte(p, '/'); i >= 0 {
				dir = p[:i]
				fmt.Fprintf(&script, "mkdir -p -- %s\n", shellQuote(dir))
			}
			fmt.Fprintf(&script, "printf '%%s' %s | base64 -d > %s\n", shellQuote(base64.StdEncoding.EncodeToString(e.content)), shellQuote(p))
			if e.mode.IsFile() && e.mode.String() == "0100755" {
				fmt.Fprintf(&script, "chmod +x -- %s\n", shellQuote(p))
			}
		}
		steps = append(steps, map[string]any{"name": "Apply uncommitted edits", "run": script.String()})
	}
	steps = append(steps, map[string]any{"name": "Run", "shell": "bash", "run": command})
	jobTimeout := timeout
	if jobTimeout <= 0 {
		jobTimeout = noJobTimeoutMinutes
	}
	wf := map[string]any{
		"name":        fmt.Sprintf("Agent command (session #%d)", rc.sess.Number),
		"on":          "workflow_dispatch",
		"permissions": "read-all",
		"jobs": map[string]any{
			"command": map[string]any{
				"runs-on":         runsOn,
				"timeout-minutes": jobTimeout,
				"steps":           steps,
			},
		},
	}
	out, err := yaml.Marshal(wf)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

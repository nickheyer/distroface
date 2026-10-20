package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/nickheyer/distroface/internal/actions/jobparser"
	"github.com/nickheyer/distroface/internal/db"
)

func decodePayload(raw string) map[string]any {
	var payload map[string]any
	_ = json.Unmarshal([]byte(raw), &payload)
	if payload == nil {
		payload = map[string]any{}
	}
	return payload
}

// Inputs as the event payload stores them, strings like GitHub
func inputsFromPayload(payload string) map[string]any {
	if in, ok := decodePayload(payload)["inputs"].(map[string]any); ok {
		return in
	}
	return map[string]any{}
}

// Integer GitHub clients parse out of a row id
func NumericID(s string) int64 {
	if n, err := strconv.ParseInt(s, 10, 64); err == nil && n >= 0 {
		return n
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return int64(h.Sum64() & (1<<53 - 1))
}

// Event payload form of typed inputs
func inputStrings(typed map[string]any) map[string]any {
	out := make(map[string]any, len(typed))
	for k, v := range typed {
		out[k] = inputString(v)
	}
	return out
}

func inputString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case nil:
		return ""
	}
	return mustJSON(v)
}

// Declared types applied back onto the strings the payload keeps
func typedDispatchInputs(decl []jobparser.WorkflowDispatchInput, raw map[string]any) map[string]any {
	out := make(map[string]any, len(raw))
	for k, v := range raw {
		out[k] = v
	}
	for _, d := range decl {
		s, ok := raw[d.Name].(string)
		if !ok {
			continue
		}
		switch d.Type {
		case "boolean":
			out[d.Name] = strings.EqualFold(strings.TrimSpace(s), "true")
		case "number":
			n, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
			if err != nil {
				n = 0
			}
			out[d.Name] = n
		}
	}
	return out
}

// Typed run inputs, dispatch values typed by their declarations
func runInputs(run *db.ActionsRun) map[string]any {
	raw := inputsFromPayload(run.Payload)
	if run.Event != "workflow_dispatch" || len(raw) == 0 {
		return raw
	}
	return typedDispatchInputs(ParseWorkflowFile(run.WorkflowPath, []byte(run.Workflow)).DispatchInputs(), raw)
}

// Github context of a run, bound to a job when given
func (m *Manager) githubContext(ctx context.Context, repo *db.GitRepository, run *db.ActionsRun, job *db.ActionsJob) map[string]any {
	payload := decodePayload(run.Payload)
	ref := plumbing.ReferenceName(run.Ref)
	refType := "branch"
	if ref.IsTag() {
		refType = "tag"
	}
	actor := m.username(ctx, run.TriggerUserID)
	base := strings.TrimRight(m.git.BaseURL(ctx), "/")
	full := repo.Namespace + "/" + repo.Name
	g := map[string]any{
		"action":            "",
		"action_path":       "",
		"action_ref":        "",
		"action_repository": "",
		"action_status":     "",
		"actor":             actor,
		"api_url":           base + "/api/v1",
		"base_ref":          pullBaseBranch(payload),
		"env":               "",
		"event":             payload,
		"event_name":        run.Event,
		"event_path":        "",
		"graphql_url":       "",
		"head_ref":          pullHeadBranch(payload),
		"job":               "",
		"path":              "",
		"ref":               run.Ref,
		"ref_name":          ref.Short(),
		"ref_protected":     false,
		"ref_type":          refType,
		"repository":        full,
		"repository_owner":  repo.Namespace,
		"repository_id":     strconv.FormatInt(NumericID(repo.ID), 10),
		"repositoryUrl":     m.git.HTMLURL(ctx, repo),
		"retention_days":    strconv.Itoa(int(m.Settings(ctx, repo).GetArtifactRetentionDays())),
		"run_id":            strconv.FormatInt(NumericID(run.ID), 10),
		"run_number":        strconv.FormatInt(run.Number, 10),
		"run_attempt":       "1",
		"secret_source":     "Actions",
		"server_url":        base,
		"sha":               run.SHA,
		"triggering_actor":  actor,
		"workflow":          run.WorkflowName,
		"workflow_ref":      full + "/" + run.WorkflowPath + "@" + run.Ref,
		"workflow_sha":      run.SHA,
		"workspace":         "",
	}
	// Only the head ref exists, pull_request_target already carries the base
	if run.Event == "pull_request" {
		g["ref"] = fmt.Sprintf("refs/pull/%d/head", run.PullNumber)
		g["ref_name"] = fmt.Sprintf("%d/head", run.PullNumber)
	}
	if job != nil {
		g["job"] = job.WorkflowJobID
		g["run_attempt"] = strconv.Itoa(max(job.Attempt, 1))
		if job.ParentJobID != "" {
			// Called jobs see the caller's inputs the way GitHub hands them over
			if caller, _ := m.store.GetActionsJob(ctx, job.ParentJobID); caller != nil {
				payload["inputs"] = inputStrings(decodePayload(caller.CallInputs))
			}
			g["event_name"] = "workflow_call"
		}
	}
	return g
}

func (m *Manager) username(ctx context.Context, userID string) string {
	if userID == "" {
		return "system"
	}
	u, err := m.store.GetUserByID(ctx, userID)
	if err != nil || u == nil {
		return "unknown"
	}
	return u.Username
}

// Typed inputs a job sees, caller values for called jobs
func (m *Manager) jobInputs(ctx context.Context, run *db.ActionsRun, job *db.ActionsJob) map[string]any {
	if job != nil && job.ParentJobID != "" {
		if caller, _ := m.store.GetActionsJob(ctx, job.ParentJobID); caller != nil {
			return decodePayload(caller.CallInputs)
		}
		return map[string]any{}
	}
	return runInputs(run)
}

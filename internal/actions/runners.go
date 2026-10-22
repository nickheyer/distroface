package actions

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func timestampOf(t time.Time) *timestamppb.Timestamp {
	return timestamppb.New(t)
}

// Runners able to serve a repository, paged, own runners first
func (m *Manager) RunnersServingRepo(ctx context.Context, repo *db.GitRepository, limit, offset int) ([]*db.ActionsRunner, int64, error) {
	return m.store.ListActionsRunnersServingRepo(ctx, repo.ID, m.repoOrgID(ctx, repo), limit, offset)
}

func randomToken(prefix string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

// ValidScope checks a scope kind and id pair
func ValidScope(kind, id string) error {
	switch kind {
	case db.ActionsScopeSystem:
		if id != "" {
			return fmt.Errorf("%w: system scope takes no id", ErrInvalid)
		}
	case db.ActionsScopeOrg, db.ActionsScopeRepo:
		if id == "" {
			return fmt.Errorf("%w: %s scope needs an id", ErrInvalid, kind)
		}
	default:
		return fmt.Errorf("%w: unknown scope kind %q", ErrInvalid, kind)
	}
	return nil
}

// CreateRegistrationToken mints a one time token for a scope
func (m *Manager) CreateRegistrationToken(ctx context.Context, scopeKind, scopeID, createdBy string) (string, time.Time, error) {
	if err := ValidScope(scopeKind, scopeID); err != nil {
		return "", time.Time{}, err
	}
	plain, err := randomToken("dfr_")
	if err != nil {
		return "", time.Time{}, err
	}
	expires := time.Now().UTC().Add(registrationTokenTTL)
	row := &db.ActionsRunnerToken{ScopeKind: scopeKind, ScopeID: scopeID, TokenHash: hashToken(plain), ExpiresAt: expires, CreatedBy: createdBy}
	if err := m.store.CreateActionsRunnerToken(ctx, row); err != nil {
		return "", time.Time{}, err
	}
	return plain, expires, nil
}

// Register exchanges a registration token for a runner and its token
func (m *Manager) Register(ctx context.Context, registration, name string, labels []string, version string) (*db.ActionsRunner, string, error) {
	registration = strings.TrimSpace(registration)
	if registration == "" {
		return nil, "", fmt.Errorf("%w: registration token is required", ErrInvalid)
	}
	tok, err := m.store.GetActionsRunnerToken(ctx, hashToken(registration))
	if err != nil {
		return nil, "", err
	}
	if tok == nil {
		return nil, "", fmt.Errorf("%w: registration token is invalid or expired", ErrDenied)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "runner-" + tok.ID[:8]
	}
	plain, err := randomToken("dfrt_")
	if err != nil {
		return nil, "", err
	}
	runner := &db.ActionsRunner{
		ScopeKind: tok.ScopeKind, ScopeID: tok.ScopeID, Name: name,
		Labels: mustJSON(normalizeLabels(labels)), TokenHash: hashToken(plain), Version: version, LastSeen: nowPtr(),
	}
	if err := m.store.RegisterActionsRunner(ctx, tok.ID, runner); err != nil {
		if errors.Is(err, stores.ErrRunnerTokenUsed) {
			return nil, "", fmt.Errorf("%w: registration token is invalid or expired", ErrDenied)
		}
		return nil, "", err
	}
	return runner, plain, nil
}

func normalizeLabels(labels []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, l := range labels {
		l = strings.TrimSpace(l)
		if l == "" || seen[strings.ToLower(l)] {
			continue
		}
		seen[strings.ToLower(l)] = true
		out = append(out, l)
	}
	return out
}

// Marks the runner seen and fails jobs it dropped
func (m *Manager) Heartbeat(ctx context.Context, runner *db.ActionsRunner, version string, running []string, reportsJobs bool) error {
	if version == "" {
		version = runner.Version
	}
	if err := m.store.TouchActionsRunner(ctx, runner.ID, version, len(running)); err != nil {
		return err
	}
	if reportsJobs {
		m.failDroppedJobs(ctx, runner, running)
	}
	return nil
}

// Unreported running jobs fail now instead of at timeout
func (m *Manager) failDroppedJobs(ctx context.Context, runner *db.ActionsRunner, running []string) {
	jobs, err := m.store.ListActionsJobsByRunner(ctx, runner.ID, StatusRunning)
	if err != nil || len(jobs) == 0 {
		return
	}
	reported := make(map[string]bool, len(running))
	for _, id := range running {
		reported[id] = true
	}
	cutoff := time.Now().UTC().Add(-droppedJobGrace)
	for _, j := range jobs {
		if reported[j.ID] || j.StartedAt == nil || j.StartedAt.After(cutoff) {
			continue
		}
		run, _ := m.store.GetActionsRunByID(ctx, j.RunID)
		if run == nil {
			continue
		}
		repo, _ := m.store.GetGitRepositoryByID(ctx, run.RepoID)
		if repo == nil {
			continue
		}
		m.failJob(ctx, repo, run, j, ConclusionFailure, "runner stopped executing the job")
		m.advanceRun(ctx, repo, run)
	}
}

// RunnerStatus renders online, busy, or offline
func RunnerStatus(r *db.ActionsRunner) string {
	if r.LastSeen == nil || time.Since(*r.LastSeen) > runnerOnlineWindow {
		return "offline"
	}
	if r.Busy > 0 {
		return "busy"
	}
	return "online"
}

// UpdateRunner changes the name or labels
func (m *Manager) UpdateRunner(ctx context.Context, runner *db.ActionsRunner, name *string, labels []string, setLabels bool) error {
	if name != nil && strings.TrimSpace(*name) != "" {
		runner.Name = strings.TrimSpace(*name)
	}
	if setLabels {
		runner.Labels = mustJSON(normalizeLabels(labels))
	}
	return m.store.UpdateActionsRunner(ctx, runner)
}

// DeleteRunner removes a runner and fails anything it was executing
func (m *Manager) DeleteRunner(ctx context.Context, runner *db.ActionsRunner) error {
	// Row goes first so an in flight poll cannot claim afterwards
	if err := m.store.DeleteActionsRunner(ctx, runner.ID); err != nil {
		return err
	}
	jobs, _ := m.store.ListActionsJobsByRunner(ctx, runner.ID, StatusRunning)
	for _, j := range jobs {
		run, _ := m.store.GetActionsRunByID(ctx, j.RunID)
		if run == nil {
			continue
		}
		repo, _ := m.store.GetGitRepositoryByID(ctx, run.RepoID)
		if repo == nil {
			continue
		}
		m.failJob(ctx, repo, run, j, ConclusionFailure, "runner was removed")
		m.advanceRun(ctx, repo, run)
	}
	return nil
}

// RunnerLabels decodes the stored label list
func RunnerLabels(r *db.ActionsRunner) []string {
	return orEmpty(decodeStrings(r.Labels))
}

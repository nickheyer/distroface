// Package runner keeps runner rows and hosts the embedded runner
package runner

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nickheyer/distroface/internal/actions"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
)

// Header carrying the long lived runner token
const TokenHeader = "X-Runner-Token"

// Fixed name of the runner inside the server process
const BuiltinName = "builtin"

const registrationTokenTTL = time.Hour

// Owns runner rows and tokens, jobs stay with the engine
type Registry struct {
	store   *stores.Store
	actions *actions.Manager
}

func NewRegistry(store *stores.Store, am *actions.Manager) *Registry {
	return &Registry{store: store, actions: am}
}

// Runners able to serve a repository, paged, own runners first
func (g *Registry) ServingRepo(ctx context.Context, repo *db.GitRepository, limit, offset int) ([]*db.ActionsRunner, int64, error) {
	return g.store.ListActionsRunnersServingRepo(ctx, repo.ID, g.orgIDOf(ctx, repo), limit, offset)
}

// Organization id behind an org namespace, empty otherwise
func (g *Registry) orgIDOf(ctx context.Context, repo *db.GitRepository) string {
	if repo == nil || !repo.IsOrgNamespace {
		return ""
	}
	org, err := g.store.GetOrganization(ctx, repo.Namespace)
	if err != nil || org == nil {
		return ""
	}
	return org.ID
}

// Mints a one time registration token for a scope
func (g *Registry) CreateRegistrationToken(ctx context.Context, scopeKind, scopeID, createdBy string) (string, time.Time, error) {
	if err := validScope(scopeKind, scopeID); err != nil {
		return "", time.Time{}, err
	}
	plain, err := randomToken("dfr_")
	if err != nil {
		return "", time.Time{}, err
	}
	expires := time.Now().UTC().Add(registrationTokenTTL)
	row := &db.ActionsRunnerToken{ScopeKind: scopeKind, ScopeID: scopeID, TokenHash: hashToken(plain), ExpiresAt: expires, CreatedBy: createdBy}
	if err := g.store.CreateActionsRunnerToken(ctx, row); err != nil {
		return "", time.Time{}, err
	}
	return plain, expires, nil
}

// Exchanges a registration token for a runner and its token
func (g *Registry) Register(ctx context.Context, registration, name string, labels []string, version string, capabilities []string) (*db.ActionsRunner, string, error) {
	registration = strings.TrimSpace(registration)
	if registration == "" {
		return nil, "", fmt.Errorf("%w: registration token is required", actions.ErrInvalid)
	}
	tok, err := g.store.GetActionsRunnerToken(ctx, hashToken(registration))
	if err != nil {
		return nil, "", err
	}
	if tok == nil {
		return nil, "", fmt.Errorf("%w: registration token is invalid or expired", actions.ErrDenied)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "runner-" + tok.ID[:8]
	}
	plain, err := randomToken("dfrt_")
	if err != nil {
		return nil, "", err
	}
	now := time.Now().UTC()
	row := &db.ActionsRunner{
		ScopeKind: tok.ScopeKind, ScopeID: tok.ScopeID, Name: name,
		Labels: encodeList(normalizeLabels(labels)), TokenHash: hashToken(plain), Version: version, LastSeen: &now,
		Capabilities: encodeList(normalizeLabels(capabilities)),
	}
	if err := g.store.RegisterActionsRunner(ctx, tok.ID, row); err != nil {
		if errors.Is(err, stores.ErrRunnerTokenUsed) {
			return nil, "", fmt.Errorf("%w: registration token is invalid or expired", actions.ErrDenied)
		}
		return nil, "", err
	}
	return row, plain, nil
}

// Registers the server's own runner with a fresh token
func (g *Registry) RegisterBuiltin(ctx context.Context, labels []string, version string) (*db.ActionsRunner, string, error) {
	plain, err := randomToken("dfrt_")
	if err != nil {
		return nil, "", err
	}
	now := time.Now().UTC()
	row := &db.ActionsRunner{
		ScopeKind: db.ActionsScopeSystem, ScopeID: "", Name: BuiltinName, Builtin: true,
		Labels: encodeList(normalizeLabels(labels)), TokenHash: hashToken(plain), Version: version,
		Capabilities: encodeList([]string{actions.CapabilityExec}), LastSeen: &now,
	}
	if err := g.store.UpsertBuiltinActionsRunner(ctx, row); err != nil {
		return nil, "", fmt.Errorf("register built in runner: %w", err)
	}
	return row, plain, nil
}

// Resolves the runner behind a token
func (g *Registry) Authenticate(ctx context.Context, token string) (*db.ActionsRunner, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("%w: missing runner token", actions.ErrDenied)
	}
	r, err := g.store.GetActionsRunnerByTokenHash(ctx, hashToken(token))
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, fmt.Errorf("%w: unknown runner token", actions.ErrDenied)
	}
	return r, nil
}

// Marks the runner seen and fails jobs it dropped
func (g *Registry) Heartbeat(ctx context.Context, runner *db.ActionsRunner, version string, running []string, reportsJobs bool, capabilities []string) error {
	if version == "" {
		version = runner.Version
	}
	if err := g.store.TouchActionsRunner(ctx, runner.ID, version, len(running)); err != nil {
		return err
	}
	// No list means an older runner without extras
	if caps := encodeList(normalizeLabels(capabilities)); caps != runner.Capabilities {
		_ = g.store.SetActionsRunnerCapabilities(ctx, runner.ID, caps)
		runner.Capabilities = caps
	}
	if reportsJobs {
		g.actions.FailDroppedJobs(ctx, runner.ID, running)
	}
	return nil
}

// Changes the name or labels
func (g *Registry) Update(ctx context.Context, runner *db.ActionsRunner, name *string, labels []string, setLabels bool) error {
	if name != nil && strings.TrimSpace(*name) != "" {
		runner.Name = strings.TrimSpace(*name)
	}
	if setLabels {
		runner.Labels = encodeList(normalizeLabels(labels))
	}
	return g.store.UpdateActionsRunner(ctx, runner)
}

// Removes a runner and fails anything it was executing
func (g *Registry) Delete(ctx context.Context, runner *db.ActionsRunner) error {
	// Row goes first so an in flight poll cannot claim afterwards
	if err := g.store.DeleteActionsRunner(ctx, runner.ID); err != nil {
		return err
	}
	g.actions.FailJobsOnRunner(ctx, runner.ID, "runner was removed")
	return nil
}

// Drops the server's runner row once turned off
func (g *Registry) DeleteBuiltin(ctx context.Context) error {
	row, err := g.store.GetBuiltinActionsRunner(ctx)
	if err != nil || row == nil {
		return err
	}
	return g.Delete(ctx, row)
}

// Rejects scope pairs the runner tables cannot hold
func validScope(kind, id string) error {
	switch kind {
	case db.ActionsScopeSystem:
		if id != "" {
			return fmt.Errorf("%w: system scope takes no id", actions.ErrInvalid)
		}
	case db.ActionsScopeOrg, db.ActionsScopeRepo:
		if id == "" {
			return fmt.Errorf("%w: %s scope needs an id", actions.ErrInvalid, kind)
		}
	default:
		return fmt.Errorf("%w: unknown scope kind %q", actions.ErrInvalid, kind)
	}
	return nil
}

func randomToken(prefix string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

func hashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// Trims, drops blanks, and dedupes case insensitively
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

func encodeList(v []string) string {
	b, _ := json.Marshal(v)
	return string(b)
}

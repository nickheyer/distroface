package agents

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/utils"
)

const botCacheTTL = 30 * time.Second

// Identity returns the bot account behind an agent handle, creating it on first use
func (m *Manager) Identity(ctx context.Context, def *Definition) (*db.User, error) {
	ident, err := m.store.GetAgentIdentity(ctx, def.Slug)
	if err != nil {
		return nil, err
	}
	if ident != nil {
		u, err := m.store.GetUserByID(ctx, ident.UserID)
		if err != nil {
			return nil, err
		}
		if u != nil {
			changed := false
			if !u.IsActive {
				u.IsActive, changed = true, true
			}
			if def.Name != "" && u.DisplayName != def.Name {
				u.DisplayName, changed = def.Name, true
			}
			if changed {
				_ = m.store.UpdateUser(ctx, u)
			}
			return u, nil
		}
	}
	username, err := m.botUsername(ctx, def.Slug)
	if err != nil {
		return nil, err
	}
	u := &db.User{ID: uuid.New().String(), Username: username, DisplayName: def.Name, AuthProvider: db.AuthProviderAgent, IsActive: true}
	if err := m.store.CreateUser(ctx, u); err != nil {
		return nil, err
	}
	if err := m.store.CreateAgentIdentity(ctx, &db.AgentIdentity{Slug: def.Slug, UserID: u.ID}); err != nil {
		return nil, err
	}
	m.invalidateBots()
	return u, nil
}

// The handle itself when free, otherwise a suffixed form
func (m *Manager) botUsername(ctx context.Context, slug string) (string, error) {
	candidates := []string{slug, slug + "-agent"}
	for i := 2; i < 20; i++ {
		candidates = append(candidates, fmt.Sprintf("%s-agent-%d", slug, i))
	}
	for _, c := range candidates {
		if !utils.NamespaceAllowed(c) {
			continue
		}
		if u, _ := m.store.GetUserByUsername(ctx, c); u != nil {
			continue
		}
		if org, _ := m.store.GetOrganization(ctx, c); org != nil {
			continue
		}
		return c, nil
	}
	return "", fmt.Errorf("%w: no free username for agent %s", ErrConflict, slug)
}

// Deactivates the bot when no stored agent uses the handle any more
func (m *Manager) retireIdentity(ctx context.Context, slug string) {
	for _, kind := range []string{db.ActionsScopeSystem, db.ActionsScopeOrg, db.ActionsScopeRepo} {
		_ = kind
	}
	var count int64
	if err := m.store.DB().WithContext(ctx).Model(&db.Agent{}).Where("slug = ?", slug).Count(&count).Error; err != nil || count > 0 {
		return
	}
	ident, _ := m.store.GetAgentIdentity(ctx, slug)
	if ident == nil {
		return
	}
	if u, _ := m.store.GetUserByID(ctx, ident.UserID); u != nil && u.IsActive {
		u.IsActive = false
		_ = m.store.UpdateUser(ctx, u)
	}
}

// Principal the agent acts as inside the forge and git managers
func (m *Manager) BotActor(ctx context.Context, def *Definition) (*auth.AuthenticatedUser, *db.User, error) {
	u, err := m.Identity(ctx, def)
	if err != nil {
		return nil, nil, err
	}
	return &auth.AuthenticatedUser{ID: u.ID, Username: u.Username, Roles: []string{"user"}, Provider: db.AuthProviderAgent}, u, nil
}

// IsAgentUser reports whether a user id belongs to a bot account
func (m *Manager) IsAgentUser(ctx context.Context, userID string) bool {
	if userID == "" {
		return false
	}
	m.botMu.Lock()
	if time.Since(m.botAt) > botCacheTTL {
		if ids, err := m.store.AgentUserIDs(ctx); err == nil {
			m.botIDs, m.botAt = ids, time.Now()
		}
	}
	ok := m.botIDs[userID]
	m.botMu.Unlock()
	return ok
}

func (m *Manager) invalidateBots() {
	m.botMu.Lock()
	m.botAt = time.Time{}
	m.botMu.Unlock()
}

// Username of the bot behind a handle, empty when none exists yet
func (m *Manager) BotUsername(ctx context.Context, slug string) string {
	ident, _ := m.store.GetAgentIdentity(ctx, slug)
	if ident == nil {
		return ""
	}
	u, _ := m.store.GetUserByID(ctx, ident.UserID)
	if u == nil {
		return ""
	}
	return u.Username
}

package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/nickheyer/distroface/internal/cache"
)

// Resolved identities live this long before the db is asked again
const (
	identityTTL    = time.Minute
	identityGenTTL = 24 * time.Hour
)

var (
	identityPrefix = cache.Prefix + "auth:id:"
	userGenPrefix  = cache.Prefix + "auth:gen:"
)

// Cached identity plus the user generation it was resolved under
type cachedIdentity struct {
	User    *AuthenticatedUser `json:"u"`
	Gen     string             `json:"g"`
	Expires *time.Time         `json:"e,omitempty"`
}

// Tokens never land in redis, only a hash of them
func identityKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return identityPrefix + hex.EncodeToString(sum[:16])
}

// Changes whenever anything behind a user's identity changes
func userGenKey(userID string) string {
	return userGenPrefix + userID
}

func newGeneration() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Cached identity for a token when its user generation still matches.
// A known user id saves a round trip, the entry and generation load together
func (m *Manager) lookupIdentity(ctx context.Context, token, userID string) (*AuthenticatedUser, bool) {
	if !m.cache.Enabled() {
		return nil, false
	}
	var entry cachedIdentity
	var gen string
	if userID != "" {
		vals, present, err := m.cache.GetMany(ctx, identityKey(token), userGenKey(userID))
		if err != nil || !present[0] || !present[1] {
			return nil, false
		}
		if json.Unmarshal([]byte(vals[0]), &entry) != nil {
			return nil, false
		}
		gen = vals[1]
	} else {
		hit, err := m.cache.Get(ctx, identityKey(token), &entry)
		if err != nil || !hit || entry.User == nil {
			return nil, false
		}
		current, ok, err := m.cache.GetString(ctx, userGenKey(entry.User.ID))
		if err != nil || !ok {
			return nil, false
		}
		gen = current
	}
	if entry.User == nil || entry.Gen == "" || entry.Gen != gen {
		return nil, false
	}
	if userID != "" && entry.User.ID != userID {
		return nil, false
	}
	if entry.Expires != nil && entry.Expires.Before(time.Now()) {
		return nil, false
	}
	return entry.User, true
}

// Stores a freshly resolved identity under the user's current generation
func (m *Manager) rememberIdentity(ctx context.Context, token string, user *AuthenticatedUser, expires *time.Time) {
	if !m.cache.Enabled() || user == nil || user.ID == "" {
		return
	}
	gen, ok, err := m.cache.GetString(ctx, userGenKey(user.ID))
	if err != nil {
		return
	}
	if !ok {
		gen = newGeneration()
		won, err := m.cache.SetNX(ctx, userGenKey(user.ID), gen, identityGenTTL)
		if err != nil {
			return
		}
		if !won {
			if gen, ok, err = m.cache.GetString(ctx, userGenKey(user.ID)); err != nil || !ok {
				return
			}
		}
	}
	_ = m.cache.Set(ctx, identityKey(token), cachedIdentity{User: user, Gen: gen, Expires: expires}, identityTTL)
}

// Drops one token's cached identity, used on logout
func (m *Manager) forgetIdentity(ctx context.Context, token string) {
	if m.cache.Enabled() {
		_ = m.cache.Delete(ctx, identityKey(token))
	}
}

// InvalidateIdentities drops cached identities for the users, none means everyone
func (m *Manager) InvalidateIdentities(ctx context.Context, userIDs []string) {
	if !m.cache.Enabled() {
		return
	}
	if len(userIDs) == 0 {
		_ = m.cache.DeletePrefix(ctx, identityPrefix)
		return
	}
	keys := make([]string, len(userIDs))
	for i, id := range userIDs {
		keys[i] = userGenKey(id)
	}
	_ = m.cache.Delete(ctx, keys...)
}

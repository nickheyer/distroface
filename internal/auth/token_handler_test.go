package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/settings"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Portal policy stub, only the knobs the scope tests need
type stubPolicy struct {
	portalHost bool
	allowPush  bool
}

func (p stubPolicy) MapName(_ *http.Request, name string) string { return name }
func (p stubPolicy) AllowAnonymous(*http.Request) bool           { return false }
func (p stubPolicy) AllowPush(*http.Request) bool                { return p.allowPush }
func (p stubPolicy) AllowRepo(*http.Request, string) bool        { return true }
func (p stubPolicy) IsPortalHost(string) bool                    { return p.portalHost }

func newScopeHandler(t *testing.T) (*accessEnv, *TokenHandler) {
	t.Helper()
	env := newAccessEnv(t)
	h := &TokenHandler{store: env.store, enforcer: env.enforcer, repoAccess: env.access, log: logger.New()}
	return env, h
}

// Granted actions keyed by type:name
func grants(h *TokenHandler, user *AuthenticatedUser, scope string) map[string]string {
	out := map[string]string{}
	for _, ra := range h.resolveAccess(httptest.NewRequest(http.MethodGet, "/auth/token", nil), user, scope) {
		out[ra.Type+":"+ra.Name] = strings.Join(ra.Actions, ",")
	}
	return out
}

func TestTokenScopesGrantDeleteLikeTheUI(t *testing.T) {
	_, h := newScopeHandler(t)
	cases := []struct {
		name  string
		user  *AuthenticatedUser
		scope string
		want  string
	}{
		{"owner deletes own package", account("alice", "user"), "repository:alice/mine:pull,push,delete", "pull,push,delete"},
		{"org member pushes but never deletes", account("bob", "user"), "repository:acme/app:pull,push,delete", "pull,push"},
		{"admin deletes anywhere", account("carol", "admin"), "repository:acme/app:pull,push,delete", "pull,push,delete"},
		{"stranger only pulls public", account("carol", "user"), "repository:acme/pub:pull,push,delete", "pull"},
		{"anonymous only pulls public", anonymous(), "repository:acme/pub:pull,push,delete", "pull"},
		{"unknown package never deletes", account("alice", "user"), "repository:alice/nothere:pull,push,delete", "pull,push"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := grants(h, tc.user, tc.scope)
			key := "repository:" + strings.SplitN(tc.scope, ":", 3)[1]
			if got[key] != tc.want {
				t.Errorf("granted %q, want %q (all %v)", got[key], tc.want, got)
			}
		})
	}
}

func TestTokenScopesCatalogNeedsManage(t *testing.T) {
	_, h := newScopeHandler(t)
	admin := account("carol", "admin")
	if got := grants(h, admin, "registry:catalog:*"); got["registry:catalog"] != "*" {
		t.Errorf("admin catalog grant = %v", got)
	}
	both := grants(h, admin, "repository:alice/mine:pull registry:catalog:*")
	if both["registry:catalog"] != "*" || both["repository:alice/mine"] != "pull" {
		t.Errorf("combined grant = %v", both)
	}
	for name, user := range map[string]*AuthenticatedUser{
		"plain user": account("alice", "user"),
		"anonymous":  anonymous(),
		"scoped admin": {ID: "u-carol", Username: "carol", Roles: []string{"admin"}, Provider: "local",
			Scope: &CredentialScope{Type: v1.CredentialScopeType_CREDENTIAL_SCOPE_TYPE_ORG, ID: "acme"}},
	} {
		if got := grants(h, user, "registry:catalog:*"); len(got) != 0 {
			t.Errorf("%s got the catalog: %v", name, got)
		}
	}
	if got := grants(h, admin, "registry:other:*"); len(got) != 0 {
		t.Errorf("unknown registry resource granted: %v", got)
	}

	// A portal host never lists the whole registry
	h.policy = stubPolicy{portalHost: true, allowPush: true}
	if got := grants(h, admin, "registry:catalog:*"); len(got) != 0 {
		t.Errorf("portal host listed the catalog: %v", got)
	}
}

func TestTokenScopesPortalWithoutPushDropsDelete(t *testing.T) {
	_, h := newScopeHandler(t)
	h.policy = stubPolicy{allowPush: false}
	got := grants(h, account("alice", "user"), "repository:alice/mine:pull,push,delete")
	if got["repository:alice/mine"] != "pull" {
		t.Errorf("read only portal granted %v", got)
	}
}

func TestTokenEndpointRefusesInactiveAccounts(t *testing.T) {
	env := newAccessEnv(t)
	ctx := context.Background()
	res := settings.NewResolver(env.store, nil)
	manager, err := NewManager(env.store, env.enforcer, "test-secret", res)
	if err != nil {
		t.Fatal(err)
	}
	ts, err := NewTokenService(t.TempDir(), "distroface", "distroface-registry", res)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	dana := &storage.User{ID: "u-dana", Username: "dana", PasswordHash: string(hash)}
	if err := env.store.CreateUser(ctx, dana); err != nil {
		t.Fatal(err)
	}
	// Create applies the column default, deactivation is a save
	dana.IsActive = false
	if err := env.store.UpdateUser(ctx, dana); err != nil {
		t.Fatal(err)
	}
	h := NewTokenHandler(ts, env.store, manager, env.enforcer, nil, nil, nil, logger.New())
	mint := func(user, pass string) int {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/auth/token?service=distroface-registry&scope=repository:dana/app:pull", nil)
		req.SetBasicAuth(user, pass)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := mint("dana", "pw"); code != http.StatusUnauthorized {
		t.Errorf("deactivated account minted a token: %d", code)
	}
	dana.IsActive = true
	if err := env.store.UpdateUser(ctx, dana); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if code := mint("dana", "pw"); code != http.StatusOK {
			t.Errorf("active account refused: %d", code)
		}
	}
	if code := mint("dana", "wrong"); code != http.StatusUnauthorized {
		t.Errorf("wrong password minted a token: %d", code)
	}
}

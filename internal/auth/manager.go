package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/internal/settings"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials   = errors.New("invalid credentials")
	ErrUserNotActive        = errors.New("user is not active")
	ErrInvalidToken         = errors.New("invalid token")
	ErrSessionExpired       = errors.New("session expired")
	ErrLocalAuthDisabled    = errors.New("local authentication is disabled")
	ErrRegistrationDisabled = errors.New("registration is disabled")
	ErrAPITokenExpired      = errors.New("api token has expired")
	ErrAPITokenNotFound     = errors.New("api token not found")
)

type Manager struct {
	store     *stores.Store
	enforcer  *rbac.Enforcer
	res       *settings.Resolver
	jwtSecret []byte
}

const jwtSecretSettingKey = "jwt_secret"

func NewManager(store *stores.Store, enforcer *rbac.Enforcer, jwtSecret string, res *settings.Resolver) (*Manager, error) {
	ctx := context.Background()
	var secret []byte

	// Priority: config value -> DB-stored value -> generate + persist to DB
	if jwtSecret != "" {
		secret = []byte(jwtSecret)
	} else {
		stored, err := store.GetSystemSetting(ctx, jwtSecretSettingKey)
		if err == nil && stored != "" {
			secret, err = hex.DecodeString(stored)
			if err != nil {
				return nil, fmt.Errorf("failed to decode stored JWT secret: %w", err)
			}
		} else {
			secret = make([]byte, 32)
			if _, err := rand.Read(secret); err != nil {
				return nil, fmt.Errorf("failed to generate JWT secret: %w", err)
			}
			if err := store.SetSystemSetting(ctx, jwtSecretSettingKey, hex.EncodeToString(secret)); err != nil {
				return nil, fmt.Errorf("failed to persist JWT secret: %w", err)
			}
			_ = store.CleanAllSessions(ctx)
		}
	}

	return &Manager{
		store:     store,
		enforcer:  enforcer,
		res:       res,
		jwtSecret: secret,
	}, nil
}

// Live effective auth settings
func (m *Manager) auth(ctx context.Context) *v1.AuthSettings {
	return m.res.System(ctx).GetAuth()
}

func (m *Manager) sessionTimeout(ctx context.Context) time.Duration {
	return time.Duration(m.auth(ctx).GetSessionTimeoutSeconds()) * time.Second
}

func (m *Manager) Login(ctx context.Context, username, password string) (*db.User, []string, string, time.Time, error) {
	if !m.auth(ctx).GetLocalEnabled() {
		return nil, nil, "", time.Time{}, ErrLocalAuthDisabled
	}

	user, err := m.store.GetUserByUsernameAndProvider(ctx, username, "local")
	if err != nil || user == nil {
		// Fall back to identifier-based lookup for email login
		user, err = m.store.GetUserByIdentifier(ctx, username)
		if err != nil || user == nil {
			return nil, nil, "", time.Time{}, ErrInvalidCredentials
		}
	}

	if !checkPassword(user.PasswordHash, password) {
		return nil, nil, "", time.Time{}, ErrInvalidCredentials
	}

	if !user.IsActive {
		return nil, nil, "", time.Time{}, ErrUserNotActive
	}

	roleNames, err := m.store.GetUserRoleNames(ctx, user.ID)
	if err != nil {
		return nil, nil, "", time.Time{}, fmt.Errorf("failed to get user roles: %w", err)
	}

	expiresAt := time.Now().Add(m.sessionTimeout(ctx))
	token, err := m.generateJWT(user.ID, user.Username, roleNames, expiresAt)
	if err != nil {
		return nil, nil, "", time.Time{}, err
	}

	session := &db.Session{
		ID:        uuid.New().String(),
		UserID:    user.ID,
		Token:     token,
		ExpiresAt: expiresAt,
	}
	if err := m.store.CreateSession(ctx, session); err != nil {
		return nil, nil, "", time.Time{}, err
	}

	now := time.Now()
	user.LastLogin = &now
	_ = m.store.UpdateUser(ctx, user)

	return user, roleNames, token, expiresAt, nil
}

func (m *Manager) ValidateSession(ctx context.Context, token string) (*AuthenticatedUser, error) {
	if token == "" {
		return nil, ErrInvalidToken
	}

	claims, err := m.validateJWT(token)
	if err != nil {
		return nil, err
	}

	session, err := m.store.GetSession(ctx, token)
	if err != nil || session == nil {
		return nil, ErrSessionExpired
	}

	userID, _ := claims["user_id"].(string)
	if session.UserID != userID {
		return nil, ErrInvalidToken
	}

	user, err := m.store.GetUserByID(ctx, userID)
	if err != nil || user == nil {
		return nil, ErrInvalidToken
	}

	if !user.IsActive {
		return nil, ErrUserNotActive
	}

	roleNames, err := m.store.GetUserRoleNames(ctx, user.ID)
	if err != nil {
		return nil, err
	}

	authUser := &AuthenticatedUser{
		ID:                 user.ID,
		Username:           user.Username,
		Roles:              roleNames,
		Provider:           user.AuthProvider,
		MustChangePassword: user.MustChangePassword,
	}
	if user.Email != nil {
		authUser.Email = *user.Email
	}

	return authUser, nil
}

// Fresh session for an already authenticated user
func (m *Manager) IssueSession(ctx context.Context, userID string) (string, time.Time, error) {
	user, err := m.store.GetUserByID(ctx, userID)
	if err != nil || user == nil {
		return "", time.Time{}, ErrInvalidToken
	}
	if !user.IsActive {
		return "", time.Time{}, ErrUserNotActive
	}

	roleNames, err := m.store.GetUserRoleNames(ctx, user.ID)
	if err != nil {
		return "", time.Time{}, err
	}

	expiresAt := time.Now().Add(m.sessionTimeout(ctx))
	token, err := m.generateJWT(user.ID, user.Username, roleNames, expiresAt)
	if err != nil {
		return "", time.Time{}, err
	}

	session := &db.Session{
		ID:        uuid.New().String(),
		UserID:    user.ID,
		Token:     token,
		ExpiresAt: expiresAt,
	}
	if err := m.store.CreateSession(ctx, session); err != nil {
		return "", time.Time{}, err
	}
	return token, expiresAt, nil
}

func (m *Manager) Logout(ctx context.Context, token string) error {
	return m.store.DeleteSession(ctx, token)
}

func (m *Manager) CreateLocalUser(ctx context.Context, username, email, password string) (*db.User, error) {
	hashedPassword, err := hashPassword(password)
	if err != nil {
		return nil, err
	}

	var emailPtr *string
	if email != "" {
		emailPtr = &email
	}

	user := &db.User{
		ID:           uuid.New().String(),
		Username:     username,
		Email:        emailPtr,
		PasswordHash: hashedPassword,
		AuthProvider: "local",
		IsActive:     true,
	}

	if err := m.store.CreateUser(ctx, user); err != nil {
		return nil, err
	}

	return user, nil
}

// Admin provisioned account, optionally forced to rotate the password
func (m *Manager) AdminCreateLocalUser(ctx context.Context, username, email, displayName, password string, mustChangePassword bool) (*db.User, error) {
	hashedPassword, err := hashPassword(password)
	if err != nil {
		return nil, err
	}

	var emailPtr *string
	if email != "" {
		emailPtr = &email
	}
	if displayName == "" {
		displayName = username
	}

	user := &db.User{
		ID:                 uuid.New().String(),
		Username:           username,
		Email:              emailPtr,
		PasswordHash:       hashedPassword,
		DisplayName:        displayName,
		AuthProvider:       "local",
		IsActive:           true,
		MustChangePassword: mustChangePassword,
	}

	if err := m.store.CreateUser(ctx, user); err != nil {
		return nil, err
	}

	return user, nil
}

func (m *Manager) ChangePassword(ctx context.Context, userID, oldPassword, newPassword string) error {
	user, err := m.store.GetUserByID(ctx, userID)
	if err != nil || user == nil {
		return ErrInvalidCredentials
	}

	if user.AuthProvider != "local" {
		return errors.New("password change only available for local auth users")
	}

	if !checkPassword(user.PasswordHash, oldPassword) {
		return ErrInvalidCredentials
	}

	hashedPassword, err := hashPassword(newPassword)
	if err != nil {
		return err
	}

	user.PasswordHash = hashedPassword
	user.MustChangePassword = false
	return m.store.UpdateUser(ctx, user)
}

func (m *Manager) AnonymousUser() *AuthenticatedUser {
	return &AuthenticatedUser{
		ID:       "anonymous",
		Username: "anonymous",
		Roles:    []string{"anonymous"},
		Provider: "anonymous",
	}
}

func (m *Manager) IsAnonymousAccessEnabled() bool {
	return m.auth(context.Background()).GetAnonymousAccess()
}

func (m *Manager) IsAnyAuthEnabled() bool {
	a := m.auth(context.Background())
	return a.GetLocalEnabled() || a.GetOidc().GetEnabled()
}

func (m *Manager) IsLocalAuthEnabled() bool {
	return m.auth(context.Background()).GetLocalEnabled()
}

func (m *Manager) IsRegistrationAllowed() bool {
	a := m.auth(context.Background())
	return a.GetLocalEnabled() && a.GetLocalAllowRegistration()
}

func (m *Manager) Settings() *settings.Resolver {
	return m.res
}

func (m *Manager) GetStore() *stores.Store {
	return m.store
}

// GenerateAPIToken creates a new API token for a user. Plaintext is returned, SHA-256 hash is stored.
func (m *Manager) GenerateAPIToken(ctx context.Context, userID, name string, expiresInDays *int32) (string, *db.APIToken, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("failed to generate token: %w", err)
	}

	plaintext := "df_" + base64.RawURLEncoding.EncodeToString(raw)

	hash := sha256.Sum256([]byte(plaintext))
	hashHex := hex.EncodeToString(hash[:])

	var expiresAt *time.Time
	if expiresInDays != nil && *expiresInDays > 0 {
		t := time.Now().Add(time.Duration(*expiresInDays) * 24 * time.Hour)
		expiresAt = &t
	}

	token := &db.APIToken{
		ID:        uuid.New().String(),
		UserID:    userID,
		Name:      name,
		TokenHash: hashHex,
		ExpiresAt: expiresAt,
	}

	if err := m.store.CreateAPIToken(ctx, token); err != nil {
		return "", nil, fmt.Errorf("failed to store api token: %w", err)
	}

	return plaintext, token, nil
}

// Routes to the API token or session validator by prefix
func (m *Manager) ValidateToken(ctx context.Context, token string) (*AuthenticatedUser, error) {
	if strings.HasPrefix(token, "df_") {
		return m.ValidateAPIToken(ctx, token)
	}
	return m.ValidateSession(ctx, token)
}

// ValidateAPIToken validates a raw API token (df_...) and returns the authenticated user.
func (m *Manager) ValidateAPIToken(ctx context.Context, rawToken string) (*AuthenticatedUser, error) {
	if !strings.HasPrefix(rawToken, "df_") {
		return nil, ErrInvalidToken
	}

	hash := sha256.Sum256([]byte(rawToken))
	hashHex := hex.EncodeToString(hash[:])

	apiToken, err := m.store.GetAPITokenByHash(ctx, hashHex)
	if err != nil || apiToken == nil {
		return nil, ErrAPITokenNotFound
	}

	if apiToken.ExpiresAt != nil && apiToken.ExpiresAt.Before(time.Now()) {
		return nil, ErrAPITokenExpired
	}

	user, err := m.store.GetUserByID(ctx, apiToken.UserID)
	if err != nil || user == nil {
		return nil, fmt.Errorf("failed to get token user: %w", err)
	}

	if !user.IsActive {
		return nil, ErrUserNotActive
	}

	roleNames, err := m.store.GetUserRoleNames(ctx, user.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user roles: %w", err)
	}

	// Synchronous, a detached write races store shutdown
	_ = m.store.UpdateAPITokenLastUsed(ctx, apiToken.ID)

	authUser := &AuthenticatedUser{
		ID:                 user.ID,
		Username:           user.Username,
		Roles:              roleNames,
		Provider:           user.AuthProvider,
		MustChangePassword: user.MustChangePassword,
	}
	if user.Email != nil {
		authUser.Email = *user.Email
	}

	return authUser, nil
}

func (m *Manager) generateJWT(userID, username string, roles []string, expiresAt time.Time) (string, error) {
	claims := jwt.MapClaims{
		"user_id":  userID,
		"username": username,
		"roles":    roles,
		"exp":      expiresAt.Unix(),
		"iat":      time.Now().Unix(),
		"jti":      uuid.New().String(), // Same second logins must not collide
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(m.jwtSecret)
}

func (m *Manager) validateJWT(tokenString string) (jwt.MapClaims, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return m.jwtSecret, nil
	})

	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
		if exp, ok := claims["exp"].(float64); ok {
			if time.Now().Unix() > int64(exp) {
				return nil, ErrSessionExpired
			}
		}
		return claims, nil
	}

	return nil, ErrInvalidToken
}

func hashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes), err
}

func checkPassword(hashedPassword, password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(password))
	return err == nil
}

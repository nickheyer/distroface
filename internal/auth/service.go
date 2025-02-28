package auth

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/nickheyer/distroface/internal/auth/permissions"
	"github.com/nickheyer/distroface/internal/models"
	"github.com/nickheyer/distroface/internal/repository"
	"github.com/nickheyer/distroface/internal/utils"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials = errors.New("INVALID CREDENTIALS")
	ErrInvalidToken       = errors.New("INVALID TOKEN")
	ErrTokenExpired       = errors.New("TOKEN EXPIRED")
	ErrInvalidScope       = errors.New("INVALID SCOPE")
)

type Claims struct {
	// STANDARD CLAIM
	Subject      string           `json:"sub"`
	Audience     string           `json:"aud"`
	ExpiresAt    *jwt.NumericDate `json:"exp"`
	IssuedAt     *jwt.NumericDate `json:"iat"`
	NotBefore    *jwt.NumericDate `json:"nbf,omitempty"`
	Issuer       string           `json:"iss"`
	JwtID        string           `json:"jti,omitempty"`
	AllowReissue bool             `json:"allow_reissue,omitempty"`

	// REGISTRY CLAIM
	Access []models.ResourceActions `json:"access"`
}

func (c Claims) Valid() error {
	now := time.Now()
	if !c.AllowReissue && now.After(c.ExpiresAt.Time) {
		return ErrTokenExpired
	}
	if now.Before(c.IssuedAt.Time) {
		return errors.New("TOKEN ISSUED IN FUTURE")
	}
	return nil
}

type AuthType string

const (
	AuthTypeWeb      AuthType = "web"
	AuthTypeRegistry AuthType = "registry"
)

type RegAuthResponse struct {
	AccessToken string    `json:"access_token"`
	ExpiresIn   int       `json:"expires_in"`
	IssuedAt    time.Time `json:"issued_at"`
	TokenType   string    `json:"token_type"`
}

type WebAuthResponse struct {
	Token     string    `json:"token,omitempty"`
	ExpiresIn int       `json:"expires_in,omitempty"`
	IssuedAt  time.Time `json:"issued_at,omitempty"`
	Username  string    `json:"username,omitempty"`
	Groups    []string  `json:"groups,omitempty"`
}

type AuthRequest struct {
	Username string   `json:"username,omitempty"`
	Password string   `json:"password,omitempty"`
	Scope    string   `json:"scope,omitempty"`
	Service  string   `json:"service,omitempty"`
	Type     AuthType `json:"type,omitempty"`
}

type authService struct {
	repo         repository.Repository
	permManager  *permissions.PermissionManager
	tokenManager *TokenManager
	config       *models.Config
}

type AuthService interface {
	Authenticate(ctx context.Context, req AuthRequest) (interface{}, error)
	ValidateToken(ctx context.Context, token string) (*Claims, error)
	RefreshToken(ctx context.Context, refreshToken string) (*WebAuthResponse, error)
	RevokeToken(ctx context.Context, token string) error
	GetPermissions(ctx context.Context, subject string) ([]models.Permission, error)
	HasPermission(ctx context.Context, subject string, perm models.Permission) bool
}

type tokenBlacklist struct {
	tokens map[string]time.Time
	mu     sync.RWMutex
}

func newTokenBlacklist() *tokenBlacklist {
	bl := &tokenBlacklist{
		tokens: make(map[string]time.Time),
	}
	go bl.cleanup()
	return bl
}

func (bl *tokenBlacklist) add(token string, expiry time.Time) {
	bl.mu.Lock()
	defer bl.mu.Unlock()
	bl.tokens[token] = expiry
}

func (bl *tokenBlacklist) isRevoked(token string) bool {
	bl.mu.RLock()
	defer bl.mu.RUnlock()
	_, exists := bl.tokens[token]
	return exists
}

func (bl *tokenBlacklist) cleanup() {
	ticker := time.NewTicker(15 * time.Minute)
	for range ticker.C {
		bl.mu.Lock()
		now := time.Now()
		for token, expiry := range bl.tokens {
			if now.After(expiry) {
				delete(bl.tokens, token)
			}
		}
		bl.mu.Unlock()
	}
}

func NewAuthService(
	repo repository.Repository,
	permManager *permissions.PermissionManager,
	signKey *rsa.PrivateKey,
	verifyKey *rsa.PublicKey,
	cfg *models.Config,
) AuthService {
	return &authService{
		repo:         repo,
		permManager:  permManager,
		tokenManager: NewTokenManager(signKey, verifyKey),
		config:       cfg,
	}
}

func (s *authService) Authenticate(ctx context.Context, req AuthRequest) (interface{}, error) {
	fmt.Printf("Authenticating request - Type: %s, Username: %s, Service: %s, Scope: %s\n",
		req.Type, req.Username, req.Service, req.Scope)

	var user *models.User
	var err error

	// GET OR CREATE USER
	if req.Username == "anonymous" || req.Username == "" {
		settings, err := utils.GetSettings[*models.AuthSettings](s.repo, "auth")

		if err == nil && settings.AllowAnonymous {
			user = &models.User{
				Username: "anonymous",
				Groups:   []string{"anonymous"},
			}
		} else {
			return nil, ErrInvalidCredentials
		}
	} else {
		user, err = s.repo.GetUser(req.Username)
		if err != nil {
			return nil, ErrInvalidCredentials
		}
		if !verifyPassword(req.Password, user.Password) {
			return nil, ErrInvalidCredentials
		}
	}

	// HANDLE WEB UI AUTH
	if req.Type == AuthTypeWeb {
		claims := &Claims{
			Subject:      user.Username,
			Audience:     req.Service,
			ExpiresAt:    jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:     jwt.NewNumericDate(time.Now()),
			NotBefore:    jwt.NewNumericDate(time.Now()),
			Issuer:       s.config.Auth.Issuer,
			AllowReissue: true,
		}

		token, err := s.tokenManager.GenerateToken(claims)
		if err != nil {
			return nil, fmt.Errorf("failed to generate token: %w", err)
		}

		return &WebAuthResponse{
			Token:     token,
			ExpiresIn: int(time.Until(claims.ExpiresAt.Time).Seconds()),
			IssuedAt:  claims.IssuedAt.Time,
			Username:  user.Username,
			Groups:    user.Groups,
		}, nil
	}

	// HANDLE REGISTRY AUTH
	if req.Type == AuthTypeRegistry {
		resourceActions, err := parseScope(req.Scope)
		if err != nil {
			fmt.Printf("Error parsing scope: %v\n", err)
			return nil, fmt.Errorf("failed to parse scope: %w", err)
		}

		claims := &Claims{
			Subject:      user.Username,
			Audience:     req.Service,
			ExpiresAt:    jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:     jwt.NewNumericDate(time.Now()),
			NotBefore:    jwt.NewNumericDate(time.Now()),
			Issuer:       s.config.Auth.Issuer,
			Access:       resourceActions,
			AllowReissue: true,
		}

		fmt.Printf("Generated access claims: %+v\n", claims.Access)

		token, err := s.tokenManager.GenerateToken(claims)
		if err != nil {
			return nil, fmt.Errorf("failed to generate token: %w", err)
		}

		fmt.Printf("Generated token for %s with claims: %+v\n", user.Username, claims)

		return &RegAuthResponse{
			AccessToken: token,
			ExpiresIn:   int(time.Until(claims.ExpiresAt.Time).Seconds()),
			IssuedAt:    claims.IssuedAt.Time,
			TokenType:   "Bearer",
		}, nil
	}

	return nil, fmt.Errorf("invalid auth type: %s", req.Type)
}

func (s *authService) ValidateToken(ctx context.Context, token string) (*Claims, error) {
	return s.tokenManager.ValidateToken(token)
}

func (s *authService) RefreshToken(ctx context.Context, refreshToken string) (*WebAuthResponse, error) {
	claim, err := s.tokenManager.ValidateToken(refreshToken)
	if err != nil {
		return nil, err
	}

	// GET USER FOR GROUPS
	username := claim.Subject
	var groups []string
	if username == "" || username == "anonymous" {
		fmt.Printf("Providing token for anonymous user\n")
		username = "anonymous"
		groups = []string{"anonymous"}
	} else {
		user, err := s.repo.GetUser(username)
		if err != nil {
			fmt.Printf("Failed to find user for username %s: %v\n Defaulting to anonymous\n", username, err)
		}
		groups = user.Groups
	}

	claims := &Claims{
		Subject:      username,
		Audience:     s.config.Auth.Service,
		ExpiresAt:    jwt.NewNumericDate(time.Now().Add(time.Hour)),
		IssuedAt:     jwt.NewNumericDate(time.Now()),
		NotBefore:    jwt.NewNumericDate(time.Now()),
		Issuer:       s.config.Auth.Issuer,
		AllowReissue: true,
	}

	token, err := s.tokenManager.GenerateToken(claims)
	if err != nil {
		return nil, fmt.Errorf("failed to generate token: %w", err)
	}

	return &WebAuthResponse{
		Token:     token,
		ExpiresIn: int(time.Until(claims.ExpiresAt.Time).Seconds()),
		IssuedAt:  claims.IssuedAt.Time,
		Username:  username,
		Groups:    groups,
	}, nil
}

func (s *authService) RevokeToken(ctx context.Context, token string) error {
	return s.tokenManager.RevokeToken(token)
}

func (s *authService) GetPermissions(ctx context.Context, subject string) ([]models.Permission, error) {
	user, err := s.repo.GetUser(subject)
	if err != nil {
		return nil, err
	}

	var perms []models.Permission
	for _, groupName := range user.Groups {
		group, err := s.repo.GetGroup(groupName)
		if err != nil {
			continue
		}

		for _, roleName := range group.Roles {
			role, err := s.repo.GetRole(roleName)
			if err != nil {
				continue
			}
			perms = append(perms, role.Permissions...)
		}
	}

	return deduplicatePermissions(perms), nil
}

func (s *authService) HasPermission(ctx context.Context, subject string, perm models.Permission) bool {
	return s.permManager.HasPermission(ctx, subject, perm)
}

type TokenManager struct {
	signKey   *rsa.PrivateKey
	verifyKey *rsa.PublicKey
	blacklist *tokenBlacklist
}

func NewTokenManager(signKey *rsa.PrivateKey, verifyKey *rsa.PublicKey) *TokenManager {
	return &TokenManager{
		signKey:   signKey,
		verifyKey: verifyKey,
		blacklist: newTokenBlacklist(),
	}
}

func (tm *TokenManager) GenerateToken(claims *Claims) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return token.SignedString(tm.signKey)
}

func (tm *TokenManager) ValidateToken(tokenString string) (*Claims, error) {
	if tm.blacklist.isRevoked(tokenString) {
		fmt.Printf("TOKEN FOUND IN REVOKED (BLACKLIST): %s\n", tokenString)
		return nil, ErrInvalidToken
	}

	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return tm.verifyKey, nil
	})

	if err != nil {
		fmt.Printf("UNABLE TO PARSE CLAIMS FROM TOKEN: %v\n", err)
		return nil, ErrInvalidToken
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}

	fmt.Printf("COULD NOT CAST STRUCT TO CLAIMS TYPE AND/OR NOT VALID CLAIMS STRUCT: %v\n", err)
	return nil, ErrInvalidToken
}

func (tm *TokenManager) RevokeToken(token string) error {
	// PARSE TOKEN WITHOUT VALIDATION TO GET EXPIRY
	parser := jwt.Parser{SkipClaimsValidation: true}
	parsedToken, _, err := parser.ParseUnverified(token, &Claims{})
	if err != nil {
		return err
	}

	if claims, ok := parsedToken.Claims.(*Claims); ok {
		tm.blacklist.add(token, claims.ExpiresAt.Time)
		return nil
	}

	return errors.New("INVALID TOKEN CLAIMS")
}

func parseScope(scope string) ([]models.ResourceActions, error) {
	if scope == "" {
		fmt.Printf("PARSE SCOPE: NO SCOPE PROVIDED\n")
		return nil, nil
	}

	fmt.Printf("PARSE SCOPE: PARSING %s\n", scope)
	scopes := strings.Split(scope, " ")
	actions := make([]models.ResourceActions, 0, len(scopes))

	for _, s := range scopes {
		// PARSE SCOPE FORMAT: repository:hello:pull,push
		parts := strings.Split(s, ":")
		if len(parts) != 3 {
			fmt.Printf("PARSE SCOPE: INVALID SCOPE FORMAT %s\n", s)
			continue
		}

		// EXTRACT TYPE, NAME, AND ACTIONS
		resourceType := parts[0] // REPOSITORY
		name := parts[1]         // IMAGE NAME
		requestedStr := parts[2] // PULL,PUSH

		// TRIM ANY EXTRA SLASHES
		name = strings.TrimPrefix(name, "/")
		name = strings.TrimSuffix(name, "/")

		// SPLIT ACTIONS STRING INTO SLICE
		requestedActions := strings.Split(requestedStr, ",")

		fmt.Printf("PARSE SCOPE: TYPE=%s NAME=%s ACTIONS=%v\n",
			resourceType, name, requestedActions)

		actions = append(actions, models.ResourceActions{
			Type:    resourceType,
			Name:    name,
			Actions: requestedActions,
		})
	}

	if len(actions) == 0 {
		fmt.Printf("PARSE SCOPE: NO VALID ACTIONS FOUND\n")
		return nil, fmt.Errorf("NO VALID ACTIONS IN SCOPE")
	}

	fmt.Printf("PARSE SCOPE: GENERATED %d ACTION SETS\n", len(actions))
	return actions, nil
}

func verifyPassword(provided string, stored []byte) bool {
	return bcrypt.CompareHashAndPassword(stored, []byte(provided)) == nil
}

func deduplicatePermissions(perms []models.Permission) []models.Permission {
	seen := make(map[string]bool)
	result := make([]models.Permission, 0)

	for _, perm := range perms {
		key := fmt.Sprintf("%s:%s:%s", perm.Action, perm.Resource, perm.Scope)
		if !seen[key] {
			seen[key] = true
			result = append(result, perm)
		}
	}

	return result
}

func HashPassword(password string) ([]byte, error) {
	return bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
}

func VerifyPassword(password string, hashedPassword []byte) bool {
	return bcrypt.CompareHashAndPassword(hashedPassword, []byte(password)) == nil
}

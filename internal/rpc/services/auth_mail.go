package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/mail"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/mailer"
	"github.com/nickheyer/distroface/internal/settings"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// ── Password reset and email verification ────────────────────────────────

const (
	passwordResetTTL     = time.Hour
	emailVerificationTTL = 24 * time.Hour
)

// Random url safe token and its stored hash
func newMailToken() (string, string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	return token, hashMailToken(token), nil
}

func hashMailToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Public base url of the instance from the settings
func instanceBaseURL(ctx context.Context, res *settings.Resolver) string {
	sys := res.System(ctx)
	host := sys.GetServer().GetPublicHostname()
	if host == "" {
		host = "localhost"
	}
	scheme := "http"
	if sys.GetTls().GetMode() == v1.TLSMode_TLS_MODE_HTTPS_ONLY {
		scheme = "https"
	}
	return scheme + "://" + host
}

func (s *AuthService) systemScope() mailer.Scope {
	return mailer.Scope{Type: v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM}
}

// Queues a templated system mail, logged when mail is off
func (s *AuthService) sendSystemMail(ctx context.Context, to, subject string, t mailer.Template) {
	if s.mail == nil {
		return
	}
	t.Instance = s.mail.InstanceName(ctx)
	text, html := mailer.Render(t)
	if err := s.mail.Enqueue(ctx, s.systemScope(), mailer.Message{To: []string{to}, Subject: subject, Text: text, HTML: html}); err != nil {
		s.log.Error("mail: queueing %q to %s: %v", subject, to, err)
	}
}

func (s *AuthService) RequestPasswordReset(ctx context.Context, req *v1.RequestPasswordResetRequest) (*v1.RequestPasswordResetResponse, error) {
	identifier := strings.TrimSpace(req.Identifier)
	if identifier == "" {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "identifier is required")
	}
	// The answer never reveals whether the account exists
	user, err := s.store.GetUserByIdentifier(ctx, identifier)
	if err != nil || user == nil || user.AuthProvider != "local" || !user.IsActive || user.Email == nil || *user.Email == "" {
		return &v1.RequestPasswordResetResponse{}, nil
	}
	token, hash, err := newMailToken()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	row := &storage.PasswordReset{ID: uuid.New().String(), UserID: user.ID, TokenHash: hash, ExpiresAt: time.Now().Add(passwordResetTTL)}
	if err := s.store.CreatePasswordReset(ctx, row); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	link := instanceBaseURL(ctx, s.authManager.Settings()) + "/reset-password/" + token
	s.sendSystemMail(ctx, *user.Email, "[Distroface] Reset your password", mailer.Template{
		Title:  "Reset your password",
		Intro:  "Someone asked to reset the password of " + user.Username + ". The link below works for one hour. Ignore this message if that was not you.",
		Action: "Reset password", URL: link,
	})
	return &v1.RequestPasswordResetResponse{}, nil
}

func (s *AuthService) ResetPassword(ctx context.Context, req *v1.ResetPasswordRequest) (*v1.ResetPasswordResponse, error) {
	if req.Token == "" || len(req.NewPassword) < 8 {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "a token and a password of at least 8 characters are required")
	}
	row, err := s.store.GetPasswordReset(ctx, hashMailToken(req.Token))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if row == nil || row.UsedAt != nil || row.ExpiresAt.Before(time.Now()) {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "the reset link is invalid or has expired")
	}
	if err := s.authManager.SetPassword(ctx, row.UserID, req.NewPassword); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	_ = s.store.UsePasswordReset(ctx, row.ID)
	_ = s.store.DeleteSessionsByUser(ctx, row.UserID)
	return &v1.ResetPasswordResponse{}, nil
}

// Queues a verification mail for a user's current address
func (s *AuthService) sendVerification(ctx context.Context, user *storage.User) error {
	if user.Email == nil || *user.Email == "" {
		return connect.Errorf(connect.CodeFailedPrecondition, "the account has no email address")
	}
	if _, err := mail.ParseAddress(*user.Email); err != nil {
		return connect.Errorf(connect.CodeFailedPrecondition, "the account email address is invalid")
	}
	token, hash, err := newMailToken()
	if err != nil {
		return connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	row := &storage.EmailVerification{ID: uuid.New().String(), UserID: user.ID, Email: *user.Email, TokenHash: hash, ExpiresAt: time.Now().Add(emailVerificationTTL)}
	if err := s.store.CreateEmailVerification(ctx, row); err != nil {
		return connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	link := instanceBaseURL(ctx, s.authManager.Settings()) + "/verify-email/" + token
	s.sendSystemMail(ctx, *user.Email, "[Distroface] Verify your email address", mailer.Template{
		Title:  "Verify your email address",
		Intro:  "Confirm that " + *user.Email + " belongs to the account " + user.Username + ". The link works for one day.",
		Action: "Verify email", URL: link,
	})
	return nil
}

func (s *AuthService) RequestEmailVerification(ctx context.Context, req *v1.RequestEmailVerificationRequest) (*v1.RequestEmailVerificationResponse, error) {
	authUser := auth.UserFromContext(ctx)
	if authUser == nil || authUser.IsAnonymous() {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	user, err := s.store.GetUserByID(ctx, authUser.ID)
	if err != nil || user == nil {
		return nil, connect.NewError(connect.CodeNotFound, "user not found")
	}
	if user.EmailVerifiedAt != nil {
		return &v1.RequestEmailVerificationResponse{}, nil
	}
	if err := s.sendVerification(ctx, user); err != nil {
		return nil, err
	}
	return &v1.RequestEmailVerificationResponse{}, nil
}

func (s *AuthService) VerifyEmail(ctx context.Context, req *v1.VerifyEmailRequest) (*v1.VerifyEmailResponse, error) {
	if req.Token == "" {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "token is required")
	}
	row, err := s.store.GetEmailVerification(ctx, hashMailToken(req.Token))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if row == nil || row.UsedAt != nil || row.ExpiresAt.Before(time.Now()) {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "the verification link is invalid or has expired")
	}
	user, err := s.store.GetUserByID(ctx, row.UserID)
	if err != nil || user == nil {
		return nil, connect.Errorf(connect.CodeNotFound, "user not found")
	}
	if user.Email == nil || !strings.EqualFold(*user.Email, row.Email) {
		return nil, connect.Errorf(connect.CodeFailedPrecondition, "the email address changed since the link was sent")
	}
	if err := s.store.SetUserEmailVerified(ctx, user.ID, time.Now()); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	_ = s.store.UseEmailVerification(ctx, row.ID)
	return &v1.VerifyEmailResponse{}, nil
}

// Reports whether an unverified local account must verify before signing in
func (s *AuthService) needsVerification(ctx context.Context, user *storage.User) bool {
	if user == nil || user.AuthProvider != "local" || user.EmailVerifiedAt != nil {
		return false
	}
	if !s.authManager.Settings().System(ctx).GetAuth().GetRequireEmailVerification() {
		return false
	}
	return user.Email != nil && *user.Email != ""
}

// Mails invite links to the addresses listed on an invite
func (s *AuthService) mailInvite(ctx context.Context, invite *storage.RegistrationInvite, emails []string) {
	if len(emails) == 0 {
		return
	}
	link := instanceBaseURL(ctx, s.authManager.Settings()) + "/register?invite=" + invite.Code
	for _, to := range emails {
		to = strings.TrimSpace(to)
		if _, err := mail.ParseAddress(to); err != nil {
			continue
		}
		intro := "You have been invited to create an account."
		if invite.Description != "" {
			intro += " " + invite.Description
		}
		if invite.PinHash != "" {
			intro += " The invite also needs the PIN the sender shares with you."
		}
		s.sendSystemMail(ctx, to, "[Distroface] You are invited", mailer.Template{Title: "You are invited", Intro: intro, Action: "Create account", URL: link})
	}
}

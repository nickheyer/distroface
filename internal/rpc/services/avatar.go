package services

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect/v2"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
)

// Largest accepted upload
const avatarMaxBytes = 1 << 20

// Route the uploaded image is served from, versioned so caches refresh
func avatarRoute(userID string, at time.Time) string {
	return fmt.Sprintf("/api/avatars/%s?v=%d", userID, at.Unix())
}

// Sniffed image type of an upload, a connect error when unusable
func avatarContentType(data []byte) (string, error) {
	if len(data) == 0 {
		return "", connect.Errorf(connect.CodeInvalidArgument, "an image is required")
	}
	if len(data) > avatarMaxBytes {
		return "", connect.Errorf(connect.CodeInvalidArgument, "images must be under 1 MB")
	}
	ct := http.DetectContentType(data)
	switch ct {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return ct, nil
	}
	return "", connect.Errorf(connect.CodeInvalidArgument, "use a png, jpeg, gif, or webp image")
}

// Stores the image and points the account at its route
func storeAvatar(ctx context.Context, store *stores.Store, user *storage.User, data []byte, contentType string) error {
	now := time.Now().UTC()
	if err := store.SetUserAvatar(ctx, &storage.UserAvatar{UserID: user.ID, ContentType: contentType, Data: data, UpdatedAt: now}); err != nil {
		return err
	}
	user.AvatarURL = avatarRoute(user.ID, now)
	return store.UpdateUser(ctx, user)
}

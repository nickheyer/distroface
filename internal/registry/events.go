package registry

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/logger"

	"github.com/google/uuid"
)

const internalWebhookSecret = "distroface-internal-webhook-secret"

// eventEnvelope matches the JSON envelope sent by Distribution v3 notifications.
type eventEnvelope struct {
	Events []event `json:"events"`
}

type event struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Action    string    `json:"action"`
	Target    struct {
		MediaType  string `json:"mediaType"`
		Size       int64  `json:"size"`
		Digest     string `json:"digest"`
		Repository string `json:"repository"`
		Tag        string `json:"tag"`
		URL        string `json:"url"`
	} `json:"target"`
	Request struct {
		ID        string `json:"id"`
		Addr      string `json:"addr"`
		Host      string `json:"host"`
		Method    string `json:"method"`
		UserAgent string `json:"useragent"`
	} `json:"request"`
	Actor struct {
		Name string `json:"name"`
	} `json:"actor"`
}

// EventHandler handles POST /internal/registry/events from the Distribution v3 notification system.
type EventHandler struct {
	store *storage.Store
	log   *logger.Logger
}

// NewEventHandler creates a new registry event handler.
func NewEventHandler(store *storage.Store, log *logger.Logger) *EventHandler {
	return &EventHandler{store: store, log: log}
}

func (h *EventHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	auth := r.Header.Get("Authorization")
	if auth != "Bearer "+internalWebhookSecret {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var envelope eventEnvelope
	if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
		h.log.Error("events: failed to decode envelope: %v", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	for _, evt := range envelope.Events {
		h.handleEvent(r, &evt)
	}

	w.WriteHeader(http.StatusOK)
}

func (h *EventHandler) handleEvent(r *http.Request, evt *event) {
	switch evt.Action {
	case "push":
		h.handlePush(r, evt)
	case "pull":
		h.handlePull(r, evt)
	}
}

func (h *EventHandler) handlePush(r *http.Request, evt *event) {
	namespace, name := splitRepoName(evt.Target.Repository)
	if namespace == "" || name == "" {
		return
	}

	repo, err := h.store.GetRepository(r.Context(), namespace, name)
	if err != nil {
		h.log.Error("events: failed to look up repo %s/%s: %v", namespace, name, err)
		return
	}

	if repo == nil {
		ownerID := ""
		user, err := h.store.GetUserByUsername(r.Context(), namespace)
		if err != nil {
			h.log.Error("events: failed to look up user %s: %v", namespace, err)
		}
		if user != nil {
			ownerID = user.ID
		}

		repo = &storage.Repository{
			ID:        uuid.New().String(),
			Namespace: namespace,
			Name:      name,
			OwnerID:   ownerID,
		}
		if err := h.store.CreateRepository(r.Context(), repo); err != nil {
			h.log.Error("events: failed to create repo %s/%s: %v", namespace, name, err)
			return
		}
		h.log.Info("events: auto-created repository %s/%s", namespace, name)
	}

	if err := h.store.IncrementPushCount(r.Context(), namespace, name); err != nil {
		h.log.Error("events: failed to increment push count for %s/%s: %v", namespace, name, err)
	}
}

func (h *EventHandler) handlePull(r *http.Request, evt *event) {
	namespace, name := splitRepoName(evt.Target.Repository)
	if namespace == "" || name == "" {
		return
	}

	if err := h.store.IncrementPullCount(r.Context(), namespace, name); err != nil {
		h.log.Error("events: failed to increment pull count for %s/%s: %v", namespace, name, err)
	}
}

func splitRepoName(fullName string) (namespace, name string) {
	parts := strings.SplitN(fullName, "/", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], parts[1]
}

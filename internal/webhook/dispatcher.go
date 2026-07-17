package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/pkg/logger"
)

const (
	maxRetries      = 5
	baseRetryDelay  = 10 * time.Second
	maxRetryDelay   = 15 * time.Minute
	requestTimeout  = 10 * time.Second
	signatureHeader = "X-Distroface-Signature-256"
	eventHeader     = "X-Distroface-Event"
	deliveryHeader  = "X-Distroface-Delivery"
	maxResponseBody = 10 * 1024 // 10KB
)

// Exponential 10s 40s 160s 640s capped with 20 percent jitter
func retryDelay(attempt int) time.Duration {
	delay := baseRetryDelay << (2 * (attempt - 1))
	if delay <= 0 || delay > maxRetryDelay {
		delay = maxRetryDelay
	}
	jitter := 0.8 + 0.4*rand.Float64()
	return time.Duration(float64(delay) * jitter)
}

// WebhookPayload is the JSON body sent to webhook URLs.
type WebhookPayload struct {
	Event      string            `json:"event"`
	Timestamp  string            `json:"timestamp"`
	Repository RepositoryPayload `json:"repository"`
	Tag        string            `json:"tag,omitempty"`
	Digest     string            `json:"digest,omitempty"`
}

// RepositoryPayload is the repository section of a webhook payload.
type RepositoryPayload struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	FullName  string `json:"full_name"`
}

// Dispatcher handles async webhook delivery with retries.
type Dispatcher struct {
	store  *stores.Store
	log    *logger.Logger
	client *http.Client
}

// NewDispatcher creates a new webhook dispatcher.
func NewDispatcher(store *stores.Store, log *logger.Logger, allowPrivateNetworks bool) *Dispatcher {
	return &Dispatcher{
		store: store,
		log:   log,
		client: &http.Client{
			Timeout:   requestTimeout,
			Transport: newSafeTransport(allowPrivateNetworks),
		},
	}
}

// Dispatch finds all active webhooks for a repo and delivers the payload asynchronously.
func (d *Dispatcher) Dispatch(ctx context.Context, event, namespace, name string, tag, digest string) {
	webhooks, err := d.store.GetActiveWebhooksForRepo(ctx, namespace, name)
	if err != nil {
		d.log.Error("webhook: failed to get webhooks for %s/%s: %v", namespace, name, err)
		return
	}

	if len(webhooks) == 0 {
		return
	}

	payload := WebhookPayload{
		Event:     event,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Repository: RepositoryPayload{
			Namespace: namespace,
			Name:      name,
			FullName:  namespace + "/" + name,
		},
		Tag:    tag,
		Digest: digest,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		d.log.Error("webhook: failed to marshal payload: %v", err)
		return
	}

	for _, wh := range webhooks {
		if !webhookMatchesEvent(wh, event) {
			continue
		}
		whBody := body
		if wh.PayloadTemplate != "" {
			rendered, err := RenderTemplate(wh.PayloadTemplate, payload)
			if err != nil {
				d.log.Error("webhook: template render failed for %s, using default payload: %v", wh.URL, err)
			} else {
				whBody = rendered
			}
		}
		go d.deliverWithRetry(wh, whBody, event)
	}
}

// Redeliver re-sends a past delivery's payload.
func (d *Dispatcher) Redeliver(ctx context.Context, deliveryID string) (*db.WebhookDelivery, error) {
	delivery, err := d.store.GetWebhookDelivery(ctx, deliveryID)
	if err != nil {
		return nil, err
	}
	if delivery == nil {
		return nil, fmt.Errorf("delivery not found")
	}

	webhook, err := d.store.GetWebhook(ctx, delivery.WebhookID)
	if err != nil {
		return nil, err
	}
	if webhook == nil {
		return nil, fmt.Errorf("webhook not found")
	}

	newDelivery := d.deliver(webhook, []byte(delivery.RequestBody), delivery.Event)
	return newDelivery, nil
}

func (d *Dispatcher) deliverWithRetry(wh *db.Webhook, body []byte, event string) {
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(retryDelay(attempt))
		}

		delivery := d.deliver(wh, body, event)
		delivery.Attempt = attempt + 1

		if err := d.store.CreateWebhookDelivery(context.Background(), delivery); err != nil {
			d.log.Error("webhook: failed to record delivery: %v", err)
		}

		if delivery.Success {
			return
		}

		d.log.Warn("webhook: delivery attempt %d/%d failed for %s (status %d)", attempt+1, maxRetries, wh.URL, delivery.StatusCode)
	}
}

func (d *Dispatcher) deliver(wh *db.Webhook, body []byte, event string) *db.WebhookDelivery {
	deliveryID := uuid.New().String()
	delivery := &db.WebhookDelivery{
		ID:          deliveryID,
		WebhookID:   wh.ID,
		Event:       event,
		RequestBody: string(body),
	}

	start := time.Now()

	req, err := http.NewRequest(http.MethodPost, wh.URL, bytes.NewReader(body))
	if err != nil {
		delivery.ResponseBody = fmt.Sprintf("failed to create request: %v", err)
		delivery.DurationMs = time.Since(start).Milliseconds()
		return delivery
	}

	contentType := wh.ContentType
	if contentType == "" {
		contentType = "application/json"
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set(eventHeader, event)
	req.Header.Set(deliveryHeader, deliveryID)

	// HMAC-SHA256 signing
	if wh.Secret != "" {
		sig := computeHMAC(wh.Secret, body)
		req.Header.Set(signatureHeader, "sha256="+sig)
	}

	resp, err := d.client.Do(req)
	delivery.DurationMs = time.Since(start).Milliseconds()
	if err != nil {
		delivery.ResponseBody = fmt.Sprintf("request failed: %v", err)
		return delivery
	}
	defer resp.Body.Close()

	delivery.StatusCode = resp.StatusCode
	delivery.Success = resp.StatusCode >= 200 && resp.StatusCode < 300

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	delivery.ResponseBody = string(respBody)

	return delivery
}

func computeHMAC(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func webhookMatchesEvent(wh *db.Webhook, event string) bool {
	// Events stored as JSON array: ["push","pull","delete"]
	var events []string
	if err := json.Unmarshal([]byte(wh.Events), &events); err != nil {
		return false
	}
	for _, e := range events {
		if strings.EqualFold(e, event) {
			return true
		}
	}
	return false
}

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
	"github.com/nickheyer/distroface/internal/settings"
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

// ── Git payloads ─────────────────────────────────────────────────────────

// Who triggered a git webhook
type Sender struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name,omitempty"`
}

// Repository section of every git webhook
type GitRepoPayload struct {
	Namespace     string `json:"namespace"`
	Name          string `json:"name"`
	FullName      string `json:"full_name"`
	CloneURL      string `json:"clone_url"`
	SSHURL        string `json:"ssh_url,omitempty"`
	HTMLURL       string `json:"html_url"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"default_branch"`
}

// Name and email of a commit party
type Person struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// One commit in a push payload
type CommitPayload struct {
	SHA       string `json:"sha"`
	Message   string `json:"message"`
	URL       string `json:"url"`
	Author    Person `json:"author"`
	Committer Person `json:"committer"`
	Timestamp string `json:"timestamp"`
}

// Push details
type PushPayload struct {
	Ref        string          `json:"ref"`
	Before     string          `json:"before"`
	After      string          `json:"after"`
	Forced     bool            `json:"forced"`
	Pusher     string          `json:"pusher"`
	Commits    []CommitPayload `json:"commits"`
	Truncated  bool            `json:"truncated"`
	CompareURL string          `json:"compare_url"`
}

// Branch or tag details
type RefPayload struct {
	Ref  string `json:"ref"`
	Name string `json:"name"`
	SHA  string `json:"sha"`
}

// Wiki change details
type WikiPayload struct {
	Action string `json:"action"`
	Ref    string `json:"ref"`
	Before string `json:"before"`
	After  string `json:"after"`
	Page   string `json:"page,omitempty"`
}

// Issue details shared by issue and pull request payloads
type IssuePayload struct {
	Number    int64    `json:"number"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	State     string   `json:"state"`
	Labels    []string `json:"labels"`
	Assignees []string `json:"assignees"`
	Milestone string   `json:"milestone,omitempty"`
	Author    string   `json:"author"`
	URL       string   `json:"url"`
	IsPull    bool     `json:"is_pull"`
}

// Comment details
type CommentPayload struct {
	ID     string `json:"id"`
	Body   string `json:"body"`
	Author string `json:"author"`
	Path   string `json:"path,omitempty"`
	Line   int    `json:"line,omitempty"`
}

// Pull request details
type PullRequestPayload struct {
	Number      int64  `json:"number"`
	Title       string `json:"title"`
	Base        string `json:"base"`
	Head        string `json:"head"`
	HeadRepo    string `json:"head_repo"`
	HeadSHA     string `json:"head_sha"`
	Draft       bool   `json:"draft"`
	Merged      bool   `json:"merged"`
	MergeCommit string `json:"merge_commit,omitempty"`
	URL         string `json:"url"`
	Author      string `json:"author"`
}

// Review details
type ReviewPayload struct {
	ID       string `json:"id"`
	State    string `json:"state"`
	Body     string `json:"body"`
	Reviewer string `json:"reviewer"`
}

// Release details
type ReleasePayload struct {
	Tag        string   `json:"tag"`
	Name       string   `json:"name"`
	Body       string   `json:"body"`
	Draft      bool     `json:"draft"`
	Prerelease bool     `json:"prerelease"`
	Assets     []string `json:"assets"`
	Images     []string `json:"images"`
	URL        string   `json:"url"`
}

// Workflow run details
type WorkflowRunPayload struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Number     int64  `json:"number"`
	Event      string `json:"event"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	URL        string `json:"url"`
}

// GitPayload is the body of every git webhook
type GitPayload struct {
	Event       string              `json:"event"`
	Action      string              `json:"action,omitempty"`
	Timestamp   string              `json:"timestamp"`
	Repository  GitRepoPayload      `json:"repository"`
	Sender      Sender              `json:"sender"`
	Push        *PushPayload        `json:"push,omitempty"`
	Ref         *RefPayload         `json:"ref,omitempty"`
	Wiki        *WikiPayload        `json:"wiki,omitempty"`
	Issue       *IssuePayload       `json:"issue,omitempty"`
	Comment     *CommentPayload     `json:"comment,omitempty"`
	PullRequest *PullRequestPayload `json:"pull_request,omitempty"`
	Review      *ReviewPayload      `json:"review,omitempty"`
	Release     *ReleasePayload     `json:"release,omitempty"`
	Forkee      *GitRepoPayload     `json:"forkee,omitempty"`
	WorkflowRun *WorkflowRunPayload `json:"workflow_run,omitempty"`
}

// Dispatcher handles async webhook delivery with retries.
type Dispatcher struct {
	store  *stores.Store
	log    *logger.Logger
	client *http.Client
}

// NewDispatcher creates a new webhook dispatcher.
func NewDispatcher(store *stores.Store, log *logger.Logger, res *settings.Resolver) *Dispatcher {
	allowPrivate := func() bool {
		return res.System(context.Background()).GetWebhooks().GetAllowPrivateNetworks()
	}
	return &Dispatcher{
		store: store,
		log:   log,
		client: &http.Client{
			Timeout:   requestTimeout,
			Transport: newSafeTransport(allowPrivate),
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

// DispatchGit finds webhooks for a git repository and delivers the payload asynchronously.
func (d *Dispatcher) DispatchGit(ctx context.Context, event, gitRepoID, namespace string, payload GitPayload) {
	webhooks, err := d.store.GetActiveWebhooksForGitRepo(ctx, gitRepoID, namespace)
	if err != nil {
		d.log.Error("webhook: failed to get webhooks for git repo %s: %v", gitRepoID, err)
		return
	}
	if len(webhooks) == 0 {
		return
	}
	body, err := json.Marshal(payload)
	if err != nil {
		d.log.Error("webhook: failed to marshal git payload: %v", err)
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

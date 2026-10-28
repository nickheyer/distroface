// Package llm speaks to inference providers behind one chat completion interface
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Connector kinds, stored on provider rows
const (
	KindOpenAI      = "openai"
	KindAnthropic   = "anthropic"
	KindAzureOpenAI = "azure_openai"
	KindOllama      = "ollama"
	KindGemini      = "gemini"
	KindBedrock     = "bedrock"
)

// Message roles
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// Stop reasons normalized across providers
const (
	StopEnd       = "end"
	StopToolCalls = "tool_calls"
	StopLength    = "length"
	StopOther     = "other"
)

// Message is one turn of a conversation
type Message struct {
	Role       string
	Content    string
	ToolCalls  []ToolCall // requests for tools from assistant
	ToolCallID string     // one call
	ToolName   string
	Reasoning  json.RawMessage // replayed provider reasoning
}

// ToolCall is one function invocation the model asked for
type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

// Tool is one function the model may call
type Tool struct {
	Name        string
	Description string
	// JSON schema object describing the arguments
	Parameters json.RawMessage
}

// Request is one completion
type Request struct {
	Model       string
	System      string
	Messages    []Message
	Tools       []Tool
	MaxTokens   int
	Temperature *float64
	// Stable id of the conversation, routes provider caches
	CacheKey string
	// Empty is the provider default, "1h" survives long pauses
	CacheTTL string
}

// Usage counts tokens of one completion, input is the whole prompt
type Usage struct {
	InputTokens  int64
	OutputTokens int64
	// Prompt tokens served from or written to the provider cache
	CacheReadTokens  int64
	CacheWriteTokens int64
}

// Response is the model's answer
type Response struct {
	Content    string
	ToolCalls  []ToolCall
	StopReason string
	Usage      Usage
	Model      string
	Reasoning  json.RawMessage // reasoning blocks
}

// Model is one entry of a provider's catalog
type Model struct {
	ID            string
	Name          string
	ContextWindow int64
}

// Client completes conversations and lists models
type Client interface {
	Complete(ctx context.Context, req *Request) (*Response, error)
	ListModels(ctx context.Context) ([]Model, error)
}

// Config wires one connector
type Config struct {
	Kind           string
	BaseURL        string
	APIKey         string
	Headers        map[string]string
	APIVersion     string
	TimeoutSeconds int
	SkipTLSVerify  bool
	// Private and loopback endpoints allowed, read at dial time
	AllowPrivate func() bool
	// Native tool calling, off wraps the client in the prompted protocol
	ToolsSupported bool
	UserAgent      string
}

// Error classes the agent loop reacts to
var (
	ErrContextLength = errors.New("context length exceeded")
	ErrRateLimited   = errors.New("rate limited")
	ErrUnauthorized  = errors.New("unauthorized")
	ErrUnavailable   = errors.New("provider unavailable")
	ErrBadRequest    = errors.New("bad request")
)

// APIError carries the provider's status and message
type APIError struct {
	Status  int
	Message string
	class   error
}

func (e *APIError) Error() string {
	if e.Status > 0 {
		return fmt.Sprintf("provider returned %d: %s", e.Status, e.Message)
	}
	return e.Message
}

func (e *APIError) Unwrap() error { return e.class }

// Classifies an http failure by status and body text
func newAPIError(status int, body string) *APIError {
	msg := strings.TrimSpace(body)
	var parsed struct {
		Error any `json:"error"`
	}
	if json.Unmarshal([]byte(body), &parsed) == nil && parsed.Error != nil {
		switch v := parsed.Error.(type) {
		case string:
			msg = v
		case map[string]any:
			if m, _ := v["message"].(string); m != "" {
				msg = m
			}
		}
	}
	e := &APIError{Status: status, Message: msg}
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "context length") || strings.Contains(lower, "context_length") ||
		strings.Contains(lower, "maximum context") || strings.Contains(lower, "too many tokens") ||
		strings.Contains(lower, "prompt is too long") || strings.Contains(lower, "input is too long") ||
		strings.Contains(lower, "reduce the length") || strings.Contains(lower, "context size"):
		e.class = ErrContextLength
	case status == http.StatusTooManyRequests:
		e.class = ErrRateLimited
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		e.class = ErrUnauthorized
	case status >= 500 || status == http.StatusRequestTimeout:
		e.class = ErrUnavailable
	case status >= 400:
		e.class = ErrBadRequest
	default:
		e.class = ErrUnavailable
	}
	return e
}

// Default endpoints per kind
func defaultBaseURL(kind string) string {
	switch kind {
	case KindOpenAI:
		return "https://api.openai.com/v1"
	case KindAnthropic:
		return "https://api.anthropic.com"
	case KindOllama:
		return "http://localhost:11434"
	case KindGemini:
		return "https://generativelanguage.googleapis.com/v1beta/openai"
	case KindBedrock:
		return "https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1"
	}
	return ""
}

// DefaultBaseURL is the endpoint a kind reaches when none is configured
func DefaultBaseURL(kind string) string { return defaultBaseURL(kind) }

// ValidKind reports whether a stored kind is known
func ValidKind(kind string) bool {
	switch kind {
	case KindOpenAI, KindAnthropic, KindAzureOpenAI, KindOllama, KindGemini, KindBedrock:
		return true
	}
	return false
}

// New builds a client for a configuration
func New(cfg Config) (Client, error) {
	if !ValidKind(cfg.Kind) {
		return nil, fmt.Errorf("unknown provider kind %q", cfg.Kind)
	}
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		base = defaultBaseURL(cfg.Kind)
	}
	if base == "" {
		return nil, fmt.Errorf("%s connectors need a base url", cfg.Kind)
	}
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		return nil, fmt.Errorf("base url must start with http:// or https://")
	}
	httpc := newHTTPClient(cfg)
	var inner Client
	switch cfg.Kind {
	case KindAnthropic:
		inner = &anthropicClient{base: base, cfg: cfg, http: httpc}
	case KindOllama:
		inner = &ollamaClient{base: base, cfg: cfg, http: httpc}
	case KindAzureOpenAI:
		inner = &openAIClient{base: base, cfg: cfg, http: httpc, azure: true}
	default:
		inner = &openAIClient{base: base, cfg: cfg, http: httpc}
	}
	if !cfg.ToolsSupported {
		return &promptedTools{inner: inner}, nil
	}
	return inner, nil
}

// Adds configured headers and a user agent
func applyHeaders(req *http.Request, cfg Config) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if cfg.UserAgent != "" {
		req.Header.Set("User-Agent", cfg.UserAgent)
	}
	for k, v := range cfg.Headers {
		if k = strings.TrimSpace(k); k != "" {
			req.Header.Set(k, v)
		}
	}
}

// Compact arguments from objects or strings, replays match bytewise
func rawArguments(v json.RawMessage) json.RawMessage {
	if len(v) == 0 {
		return json.RawMessage("{}")
	}
	var s string
	if json.Unmarshal(v, &s) == nil {
		s = strings.TrimSpace(s)
		if s == "" {
			return json.RawMessage("{}")
		}
		if json.Valid([]byte(s)) {
			return compactArguments([]byte(s))
		}
		b, _ := json.Marshal(map[string]any{"input": s})
		return b
	}
	if json.Valid(v) {
		return compactArguments(v)
	}
	return json.RawMessage("{}")
}

func compactArguments(v []byte) json.RawMessage {
	var buf bytes.Buffer
	if err := json.Compact(&buf, v); err != nil {
		return v
	}
	return buf.Bytes()
}

// Merges adjacent same role messages, which strict providers require
func mergeAdjacent(msgs []Message, roleOf func(Message) string) [][]Message {
	var out [][]Message
	for _, m := range msgs {
		if n := len(out); n > 0 && roleOf(out[n-1][0]) == roleOf(m) {
			out[n-1] = append(out[n-1], m)
			continue
		}
		out = append(out, []Message{m})
	}
	return out
}

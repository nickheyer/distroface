package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const anthropicVersion = "2023-06-01"

// Api insists on a number, this reads as no cap
const unboundedMaxTokens = 1_000_000

// Output ceilings the api reported, keyed by endpoint and model
var learnedCeilings sync.Map

// Endpoints that refused a long answer outside the event stream
var streamOnly sync.Map

// How much of the cache markup each endpoint accepts
var cacheSupport sync.Map

// Endpoints and models that refuse a sampling temperature
var noTemperature sync.Map

const (
	cacheFull = iota
	cacheNoTTL
	cacheNone
)

var ceilingPattern = regexp.MustCompile(`max_tokens: *\d+ *> *(\d+)`)

var cachePattern = regexp.MustCompile(`cache_control|\bttl\b`)

const (
	blockThinking = "thinking"
	blockRedacted = "redacted_thinking"
)

// Messages api client
type anthropicClient struct {
	base string
	cfg  Config
	http *http.Client
}

type anBlock struct {
	Type         string          `json:"type"`
	Text         string          `json:"text,omitempty"`
	ID           string          `json:"id,omitempty"`
	Name         string          `json:"name,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
	ToolUseID    string          `json:"tool_use_id,omitempty"`
	Content      string          `json:"content,omitempty"`
	Thinking     string          `json:"thinking,omitempty"`
	Signature    string          `json:"signature,omitempty"`
	Data         string          `json:"data,omitempty"`
	CacheControl *anCache        `json:"cache_control,omitempty"`
}

// Replay needs the thinking field even when empty
func (b anBlock) MarshalJSON() ([]byte, error) {
	type plain anBlock
	if b.Type != blockThinking {
		return json.Marshal(plain(b))
	}
	return json.Marshal(struct {
		Type      string `json:"type"`
		Thinking  string `json:"thinking"`
		Signature string `json:"signature,omitempty"`
	}{b.Type, b.Thinking, b.Signature})
}

func (b anBlock) reasoning() bool { return b.Type == blockThinking || b.Type == blockRedacted }

type anCache struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

type anMessage struct {
	Role    string    `json:"role"`
	Content []anBlock `json:"content"`
}

type anTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anRequest struct {
	Model       string      `json:"model"`
	System      []anBlock   `json:"system,omitempty"`
	Messages    []anMessage `json:"messages"`
	Tools       []anTool    `json:"tools,omitempty"`
	MaxTokens   int         `json:"max_tokens"`
	Temperature *float64    `json:"temperature,omitempty"`
}

type anStreamRequest struct {
	anRequest
	Stream bool `json:"stream"`
}

type anResponse struct {
	Model      string    `json:"model"`
	Content    []anBlock `json:"content"`
	StopReason string    `json:"stop_reason"`
	Usage      anUsage   `json:"usage"`
}

// Input excludes cached tokens, the prompt sums all three
type anUsage struct {
	InputTokens         int64 `json:"input_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadTokens     int64 `json:"cache_read_input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
}

func (u anUsage) prompt() int64 { return u.InputTokens + u.CacheCreationTokens + u.CacheReadTokens }

func (c *anthropicClient) auth(req *http.Request) {
	if c.cfg.APIKey != "" {
		req.Header.Set("x-api-key", c.cfg.APIKey)
	}
	req.Header.Set("anthropic-version", anthropicVersion)
}

func (c *anthropicClient) Complete(ctx context.Context, req *Request) (*Response, error) {
	if req.Model == "" {
		return nil, fmt.Errorf("%w: a model is required", ErrBadRequest)
	}
	key := c.base + " " + req.Model
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = unboundedMaxTokens
	}
	if v, ok := learnedCeilings.Load(key); ok && v.(int) < maxTokens {
		maxTokens = v.(int)
	}
	body := anRequest{Model: req.Model, MaxTokens: maxTokens, Temperature: req.Temperature}
	if _, ok := noTemperature.Load(key); ok {
		body.Temperature = nil
	}
	if req.System != "" {
		body.System = []anBlock{{Type: "text", Text: req.System}}
	}
	// Tool results and user text share the user role, strict alternation is required
	roleOf := func(m Message) string {
		if m.Role == RoleTool {
			return RoleUser
		}
		return m.Role
	}
	for _, group := range mergeAdjacent(req.Messages, roleOf) {
		msg := anMessage{Role: roleOf(group[0])}
		for _, m := range group {
			switch m.Role {
			case RoleTool:
				content := m.Content
				if content == "" {
					content = "(empty)"
				}
				msg.Content = append(msg.Content, anBlock{Type: "tool_result", ToolUseID: m.ToolCallID, Content: content})
			case RoleAssistant:
				var reasoning []anBlock
				if len(m.Reasoning) > 0 && json.Unmarshal(m.Reasoning, &reasoning) == nil {
					msg.Content = append(msg.Content, reasoning...)
				}
				if m.Content != "" {
					msg.Content = append(msg.Content, anBlock{Type: "text", Text: m.Content})
				}
				for _, tc := range m.ToolCalls {
					msg.Content = append(msg.Content, anBlock{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: rawArguments(tc.Arguments)})
				}
				if len(msg.Content) == 0 {
					msg.Content = append(msg.Content, anBlock{Type: "text", Text: "(no content)"})
				}
			default:
				text := m.Content
				if text == "" {
					text = "(empty)"
				}
				msg.Content = append(msg.Content, anBlock{Type: "text", Text: text})
			}
		}
		body.Messages = append(body.Messages, msg)
	}
	for _, t := range req.Tools {
		schema := t.Parameters
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		body.Tools = append(body.Tools, anTool{Name: t.Name, Description: t.Description, InputSchema: schema})
	}
	level := cacheFull
	if v, ok := cacheSupport.Load(c.base); ok {
		level = v.(int)
	}
	markCache(&body, level, req.CacheTTL)
	out, err := c.send(ctx, key, body)
	// Rejections teach the ceiling and the accepted cache markup
	for attempt := 0; err != nil && attempt < 3; attempt++ {
		switch {
		case rejectedCeiling(err) > 0 && rejectedCeiling(err) < body.MaxTokens:
			body.MaxTokens = rejectedCeiling(err)
			learnedCeilings.Store(key, body.MaxTokens)
		case rejectsCache(err) && level < cacheNone:
			level++
			cacheSupport.Store(c.base, level)
			markCache(&body, level, req.CacheTTL)
		case rejectsTemperature(err) && body.Temperature != nil:
			body.Temperature = nil
			noTemperature.Store(key, true)
		default:
			return nil, err
		}
		out, err = c.send(ctx, key, body)
	}
	if err != nil {
		return nil, err
	}
	resp := &Response{Model: out.Model, Usage: Usage{
		InputTokens: out.Usage.prompt(), OutputTokens: out.Usage.OutputTokens,
		CacheReadTokens: out.Usage.CacheReadTokens, CacheWriteTokens: out.Usage.CacheCreationTokens,
	}}
	var reasoning []anBlock
	for _, b := range out.Content {
		switch b.Type {
		case "text":
			if resp.Content != "" {
				resp.Content += "\n"
			}
			resp.Content += b.Text
		case "tool_use":
			resp.ToolCalls = append(resp.ToolCalls, ToolCall{ID: b.ID, Name: b.Name, Arguments: rawArguments(b.Input)})
		case blockThinking, blockRedacted:
			reasoning = append(reasoning, anBlock{Type: b.Type, Thinking: b.Thinking, Signature: b.Signature, Data: b.Data})
		}
	}
	if len(reasoning) > 0 {
		resp.Reasoning, _ = json.Marshal(reasoning)
	}
	switch {
	case len(resp.ToolCalls) > 0:
		resp.StopReason = StopToolCalls
	case out.StopReason == "max_tokens":
		resp.StopReason = StopLength
	case out.StopReason == "end_turn" || out.StopReason == "stop_sequence" || out.StopReason == "":
		resp.StopReason = StopEnd
	default:
		resp.StopReason = StopOther
	}
	return resp, nil
}

// One request, streamed once the api has asked for it
func (c *anthropicClient) send(ctx context.Context, key string, body anRequest) (*anResponse, error) {
	if _, ok := streamOnly.Load(key); ok {
		return c.stream(ctx, body)
	}
	var out anResponse
	err := postJSON(ctx, c.http, c.cfg, c.base+"/v1/messages", c.auth, body, &out)
	if err == nil {
		return &out, nil
	}
	if !wantsStream(err) {
		return nil, err
	}
	streamOnly.Store(key, true)
	return c.stream(ctx, body)
}

// System marker caches tools and prompt, tail marker the transcript
func markCache(body *anRequest, level int, ttl string) {
	for i := range body.System {
		body.System[i].CacheControl = nil
	}
	for i := range body.Messages {
		for j := range body.Messages[i].Content {
			body.Messages[i].Content[j].CacheControl = nil
		}
	}
	if level >= cacheNone {
		return
	}
	mark := func() *anCache {
		cc := &anCache{Type: "ephemeral"}
		if level == cacheFull {
			cc.TTL = ttl
		}
		return cc
	}
	if n := len(body.System); n > 0 {
		body.System[n-1].CacheControl = mark()
	}
	// Reasoning blocks do not get markers
	if n := len(body.Messages); n > 0 {
		blocks := body.Messages[n-1].Content
		for j := len(blocks) - 1; j >= 0; j-- {
			if !blocks[j].reasoning() {
				blocks[j].CacheControl = mark()
				break
			}
		}
	}
}

// A 400 naming cache markup, older gateways lack it
func rejectsCache(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadRequest {
		return false
	}
	return cachePattern.MatchString(strings.ToLower(apiErr.Message))
}

func rejectsTemperature(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusBadRequest && strings.Contains(strings.ToLower(apiErr.Message), "temperature")
}

// The api demands the event stream for long answers
func wantsStream(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusBadRequest && strings.Contains(strings.ToLower(apiErr.Message), "stream")
}

// Output ceiling named in a rejection, else zero
func rejectedCeiling(err error) int {
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadRequest {
		return 0
	}
	m := ceilingPattern.FindStringSubmatch(apiErr.Message)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

func (c *anthropicClient) stream(ctx context.Context, body anRequest) (*anResponse, error) {
	payload, err := json.Marshal(anStreamRequest{anRequest: body, Stream: true})
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/v1/messages", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	applyHeaders(httpReq, c.cfg)
	c.auth(httpReq)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, &APIError{Message: err.Error(), class: ErrUnavailable}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, newAPIError(resp.StatusCode, string(raw))
	}
	return readEventStream(resp.Body)
}

// Assembles streamed events back into one message
func readEventStream(r io.Reader) (*anResponse, error) {
	out := &anResponse{}
	var order []int
	blocks := map[int]*anBlock{}
	inputs := map[int]*strings.Builder{}
	apply := func(payload string) error {
		var ev struct {
			Type         string          `json:"type"`
			Index        int             `json:"index"`
			Message      *anResponse     `json:"message"`
			ContentBlock *anBlock        `json:"content_block"`
			Delta        json.RawMessage `json:"delta"`
			Usage        *struct {
				OutputTokens int64 `json:"output_tokens"`
			} `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			return &APIError{Message: fmt.Sprintf("invalid event from provider: %v", err), class: ErrUnavailable}
		}
		switch ev.Type {
		case "message_start":
			if ev.Message != nil {
				out.Model, out.Usage = ev.Message.Model, ev.Message.Usage
			}
		case "content_block_start":
			if ev.ContentBlock != nil {
				b := *ev.ContentBlock
				blocks[ev.Index] = &b
				inputs[ev.Index] = &strings.Builder{}
				order = append(order, ev.Index)
			}
		case "content_block_delta":
			var d struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
				Thinking    string `json:"thinking"`
				Signature   string `json:"signature"`
			}
			_ = json.Unmarshal(ev.Delta, &d)
			if b := blocks[ev.Index]; b != nil {
				switch d.Type {
				case "text_delta":
					b.Text += d.Text
				case "input_json_delta":
					inputs[ev.Index].WriteString(d.PartialJSON)
				case "thinking_delta":
					b.Thinking += d.Thinking
				case "signature_delta":
					b.Signature += d.Signature
				}
			}
		case "message_delta":
			var d struct {
				StopReason string `json:"stop_reason"`
			}
			_ = json.Unmarshal(ev.Delta, &d)
			if d.StopReason != "" {
				out.StopReason = d.StopReason
			}
			if ev.Usage != nil && ev.Usage.OutputTokens > 0 {
				out.Usage.OutputTokens = ev.Usage.OutputTokens
			}
		case "error":
			msg := "provider reported an error mid stream"
			if ev.Error != nil && ev.Error.Message != "" {
				msg = ev.Error.Message
			}
			return &APIError{Message: msg, class: ErrUnavailable}
		}
		return nil
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), math.MaxInt32)
	var data []string
	flush := func() error {
		if len(data) == 0 {
			return nil
		}
		payload := strings.Join(data, "\n")
		data = data[:0]
		if strings.TrimSpace(payload) == "[DONE]" {
			return nil
		}
		return apply(payload)
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if err := flush(); err != nil {
				return nil, err
			}
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := sc.Err(); err != nil {
		return nil, &APIError{Message: err.Error(), class: ErrUnavailable}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	for _, i := range order {
		b := blocks[i]
		if b.Type == "tool_use" {
			if in := inputs[i].String(); in != "" {
				b.Input = json.RawMessage(in)
			} else if len(b.Input) == 0 {
				b.Input = json.RawMessage(`{}`)
			}
		}
		out.Content = append(out.Content, *b)
	}
	return out, nil
}

func (c *anthropicClient) ListModels(ctx context.Context) ([]Model, error) {
	var out struct {
		Data []struct {
			ID             string `json:"id"`
			DisplayName    string `json:"display_name"`
			MaxInputTokens int64  `json:"max_input_tokens"`
		} `json:"data"`
	}
	if err := getJSON(ctx, c.http, c.cfg, c.base+"/v1/models?limit=100", c.auth, &out); err != nil {
		return nil, err
	}
	models := make([]Model, 0, len(out.Data))
	for _, d := range out.Data {
		name := d.DisplayName
		if name == "" {
			name = d.ID
		}
		models = append(models, Model{ID: d.ID, Name: name, ContextWindow: d.MaxInputTokens})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}

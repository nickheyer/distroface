package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAICachedTokensAndCacheKey(t *testing.T) {
	var sentKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			PromptCacheKey string `json:"prompt_cache_key"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		sentKey = body.PromptCacheKey
		fmt.Fprint(w, `{"model":"m","choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1200,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":1024}}}`)
	}))
	defer server.Close()
	c, err := New(Config{Kind: KindOpenAI, BaseURL: server.URL, APIKey: "k", ToolsSupported: true, AllowPrivate: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Complete(context.Background(), &Request{Model: "m", CacheKey: "sess-1", Messages: []Message{{Role: RoleUser, Content: "hello"}}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Usage.InputTokens != 1200 || resp.Usage.CacheReadTokens != 1024 || resp.Usage.OutputTokens != 5 {
		t.Fatalf("usage %+v", resp.Usage)
	}
	// Only the first party api sees the key
	if sentKey != "" {
		t.Fatalf("cache key %q sent to a compatible server", sentKey)
	}
	for base, want := range map[string]bool{"https://api.openai.com/v1": true, "https://API.openai.com": true, server.URL: false, "https://example.com/v1": false} {
		if got := (&openAIClient{base: base}).routesCacheByKey(); got != want {
			t.Fatalf("%s routes by key %v", base, got)
		}
	}
	if (&openAIClient{base: "https://api.openai.com/v1", azure: true}).routesCacheByKey() {
		t.Fatal("azure must not send the key")
	}
}

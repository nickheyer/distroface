package actions

import (
	"testing"
	"time"

	"github.com/nickheyer/distroface/internal/db"
)

func TestCacheSignature(t *testing.T) {
	key := []byte("test-key")
	now := time.Unix(1_700_000_000, 0)
	exp := now.Add(time.Hour).Unix()
	sig := cacheSignature(key, 42, exp)
	if !verifyCacheSignature(key, 42, exp, sig, now) {
		t.Fatal("valid signature rejected")
	}
	if verifyCacheSignature(key, 43, exp, sig, now) {
		t.Error("signature accepted for another id")
	}
	if verifyCacheSignature(key, 42, exp+1, sig, now) {
		t.Error("signature accepted for another expiry")
	}
	if verifyCacheSignature([]byte("other"), 42, exp, sig, now) {
		t.Error("signature accepted under another key")
	}
	if verifyCacheSignature(key, 42, exp, sig, now.Add(2*time.Hour)) {
		t.Error("expired signature accepted")
	}
	if verifyCacheSignature(key, 42, exp, "zz", now) {
		t.Error("malformed signature accepted")
	}
}

func TestParseCacheKeys(t *testing.T) {
	got := parseCacheKeys(" npm-linux-abc , npm-linux-,, npm- ")
	want := []string{"npm-linux-abc", "npm-linux-", "npm-"}
	if len(got) != len(want) {
		t.Fatalf("keys = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("key %d = %q, want %q", i, got[i], want[i])
		}
	}
	if len(parseCacheKeys(" , ")) != 0 {
		t.Error("blank keys should parse to nothing")
	}
}

func TestParseContentRange(t *testing.T) {
	start, end, err := parseContentRange("bytes 1024-2047/*")
	if err != nil || start != 1024 || end != 2047 {
		t.Fatalf("got %d %d %v", start, end, err)
	}
	for _, bad := range []string{"", "1024-2047", "bytes x-y/*", "bytes 10-5/*", "bytes -1-5/*"} {
		if _, _, err := parseContentRange(bad); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
}

func TestPickCache(t *testing.T) {
	mainRef, featRef, otherRef := "refs/heads/main", "refs/heads/feat", "refs/heads/other"
	// Newest first, the way the store lists them
	entries := []*db.ActionsCache{
		{ID: 1, Key: "npm-linux-zzz", Ref: otherRef},
		{ID: 2, Key: "npm-linux-abc", Ref: mainRef},
		{ID: 3, Key: "npm-linux-abc", Ref: featRef},
		{ID: 4, Key: "npm-linux-old", Ref: featRef},
	}
	prefer := []string{featRef, mainRef}
	if got := pickCache(entries, "npm-linux-abc", true, prefer); got == nil || got.ID != 3 {
		t.Errorf("exact match should prefer the run ref, got %+v", got)
	}
	if got := pickCache(entries, "npm-linux-abc", true, []string{"refs/heads/none", mainRef}); got == nil || got.ID != 2 {
		t.Errorf("exact match should fall back to the default branch, got %+v", got)
	}
	if got := pickCache(entries, "npm-linux-", false, prefer); got == nil || got.ID != 3 {
		t.Errorf("prefix match should prefer the run ref and newest, got %+v", got)
	}
	if got := pickCache(entries, "npm-linux-", false, []string{"refs/heads/none", "refs/heads/nope"}); got == nil || got.ID != 1 {
		t.Errorf("prefix match without preference should take the newest, got %+v", got)
	}
	if got := pickCache(entries, "pip-", false, prefer); got != nil {
		t.Errorf("unrelated prefix matched %+v", got)
	}
	if got := pickCache(entries, "npm-linux", true, prefer); got != nil {
		t.Errorf("exact lookup matched a prefix %+v", got)
	}
}

package ratelimit

import (
	"net/http"
	"testing"
	"time"
)

func TestTakeQuota(t *testing.T) {
	l := New(3, time.Minute)

	for i := 0; i < 3; i++ {
		allowed, remaining, _ := l.Take("k")
		if !allowed {
			t.Fatalf("event %d: expected allowed", i+1)
		}
		if want := 3 - (i + 1); remaining != want {
			t.Fatalf("event %d: remaining = %d, want %d", i+1, remaining, want)
		}
	}

	allowed, remaining, resetAt := l.Take("k")
	if allowed {
		t.Fatal("4th event: expected blocked")
	}
	if remaining != 0 {
		t.Fatalf("4th event: remaining = %d, want 0", remaining)
	}
	if resetAt.Before(time.Now()) {
		t.Fatal("resetAt should be in the future")
	}

	// Other keys are unaffected
	if allowed, _, _ := l.Take("other"); !allowed {
		t.Fatal("independent key should be allowed")
	}
}

func TestFailureLockout(t *testing.T) {
	l := New(2, time.Minute)

	if l.Blocked("ip") {
		t.Fatal("fresh key should not be blocked")
	}
	l.Record("ip")
	if l.Blocked("ip") {
		t.Fatal("one failure of two should not block")
	}
	l.Record("ip")
	if !l.Blocked("ip") {
		t.Fatal("two failures should block")
	}

	l.Reset("ip")
	if l.Blocked("ip") {
		t.Fatal("reset should clear the lockout")
	}
}

func TestWindowExpiry(t *testing.T) {
	l := New(1, 50*time.Millisecond)

	l.Record("k")
	if !l.Blocked("k") {
		t.Fatal("should be blocked inside the window")
	}
	time.Sleep(60 * time.Millisecond)
	if l.Blocked("k") {
		t.Fatal("events should age out of the window")
	}
}

func TestClientIP(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		headers    map[string]string
		want       string
	}{
		{"remote addr only", "10.1.2.3:5555", nil, "10.1.2.3"},
		{"remote addr no port", "10.1.2.3", nil, "10.1.2.3"},
		{"x-real-ip wins", "10.1.2.3:5555", map[string]string{"X-Real-IP": "203.0.113.9"}, "203.0.113.9"},
		{"first xff hop", "10.1.2.3:5555", map[string]string{"X-Forwarded-For": "198.51.100.4, 10.0.0.1"}, "198.51.100.4"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			for k, v := range tc.headers {
				h.Set(k, v)
			}
			if got := ClientIP(tc.remoteAddr, h); got != tc.want {
				t.Fatalf("ClientIP = %q, want %q", got, tc.want)
			}
		})
	}
}

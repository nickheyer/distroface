package mirror

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"google.golang.org/protobuf/proto"
)

func respWith(status int, headers map[string]string) *http.Response {
	h := http.Header{}
	for k, v := range headers {
		h.Set(k, v)
	}
	return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader("nope"))}
}

func TestClassifyResponseRateLimits(t *testing.T) {
	cases := []struct {
		name    string
		resp    *http.Response
		limited bool
	}{
		{"ok", respWith(200, nil), false},
		{"not modified", respWith(304, nil), false},
		{"plain 404", respWith(404, nil), false},
		{"429", respWith(429, map[string]string{"Retry-After": "120"}), true},
		{"github quota 403", respWith(403, map[string]string{"X-RateLimit-Remaining": "0"}), true},
		{"scope 403", respWith(403, map[string]string{"X-RateLimit-Remaining": "55"}), false},
	}
	for _, tc := range cases {
		err := classifyResponse(tc.resp, "http://x")
		if _, limited := RetryAfter(err); limited != tc.limited {
			t.Errorf("%s: limited = %v, want %v (err %v)", tc.name, limited, tc.limited, err)
		}
		if tc.resp.StatusCode < 300 && err != nil {
			t.Errorf("%s: unexpected error %v", tc.name, err)
		}
	}
}

func TestRetryDeadlineHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "300")
	until := retryDeadline(h)
	if d := time.Until(until); d < 4*time.Minute || d > 6*time.Minute {
		t.Errorf("retry-after seconds gave %v out", d)
	}

	h = http.Header{}
	h.Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(20*time.Minute).Unix(), 10))
	until = retryDeadline(h)
	if d := time.Until(until); d < 19*time.Minute || d > 22*time.Minute {
		t.Errorf("reset header gave %v out", d)
	}

	// Absurd reset values clamp to the ceiling
	h.Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(48*time.Hour).Unix(), 10))
	if d := time.Until(retryDeadline(h)); d > defaultLimits().MaxCooldown+time.Minute {
		t.Errorf("clamp failed, got %v", d)
	}
}

func TestNextStateTransitions(t *testing.T) {
	st, msg := nextState(SyncState{Failures: 3, RateLimited: true, CooldownUntil: time.Now().Add(time.Hour)}, nil)
	if st.Failures != 0 || st.RateLimited || !st.CooldownUntil.IsZero() || msg != "" {
		t.Errorf("success should clear state, got %+v %q", st, msg)
	}

	until := time.Now().Add(30 * time.Minute)
	st, msg = nextState(SyncState{}, &rateLimitedError{until: until})
	if !st.RateLimited || !st.CooldownUntil.Equal(until) || msg == "" {
		t.Errorf("rate limit not recorded: %+v %q", st, msg)
	}

	st, _ = nextState(SyncState{Failures: 1}, errors.New("boom"))
	if st.Failures != 2 || st.RateLimited || !st.CoolingDown(time.Now()) {
		t.Errorf("failure backoff not recorded: %+v", st)
	}
}

func TestBackoffGrowsAndCaps(t *testing.T) {
	ceiling := defaultLimits().MaxCooldown
	prev := time.Duration(0)
	for i := 1; i < 12; i++ {
		d := backoffFor(i)
		if d < prev/2 {
			t.Fatalf("backoff shrank at %d: %v after %v", i, d, prev)
		}
		if d > ceiling+ceiling/4 {
			t.Fatalf("backoff exceeded cap at %d: %v", i, d)
		}
		prev = d
	}
}

func TestPacerSpacesSameHost(t *testing.T) {
	p := newPacer()
	ctx := context.Background()
	start := time.Now()
	for range 3 {
		if err := p.wait(ctx, "example.com"); err != nil {
			t.Fatal(err)
		}
	}
	if elapsed := time.Since(start); elapsed < 2*defaultLimits().HostInterval-50*time.Millisecond {
		t.Errorf("three same host requests took only %v", elapsed)
	}
	if err := p.wait(ctx, "other.com"); err != nil {
		t.Fatal(err)
	}
}

func TestPacerHonorsCancel(t *testing.T) {
	p := newPacer()
	ctx, cancel := context.WithCancel(context.Background())
	_ = p.wait(ctx, "h")
	cancel()
	if err := p.wait(ctx, "h"); err == nil {
		t.Error("cancelled wait should error")
	}
}

func TestLimitsFromSettingsSanitizes(t *testing.T) {
	if got := LimitsFromSettings(nil); got != defaultLimits() {
		t.Errorf("nil settings should give defaults, got %+v", got)
	}

	s := &v1.MirrorSettings{
		PerHostSpacingMs:         proto.Int32(0),
		MaxConcurrentSyncs:       proto.Int32(500),
		SyncTimeoutMinutes:       proto.Int32(10),
		RateLimitCooldownMinutes: proto.Int32(30),
		MaxCooldownMinutes:       proto.Int32(5),
		FailureBackoffMinutes:    proto.Int32(2),
		MinIntervalMinutes:       proto.Int32(0),
		MaxSyncDepth:             proto.Int32(7),
	}
	l := LimitsFromSettings(s)
	if l.HostInterval != 0 {
		t.Errorf("explicit zero spacing should stick, got %v", l.HostInterval)
	}
	if l.Workers != 32 {
		t.Errorf("workers should cap at 32, got %d", l.Workers)
	}
	if l.SyncTimeout != 10*time.Minute || l.BackoffBase != 2*time.Minute {
		t.Errorf("durations not applied: %+v", l)
	}
	if l.MaxCooldown != l.RateLimitCooldown {
		t.Errorf("max cooldown should rise to the rate limit cooldown, got %v vs %v", l.MaxCooldown, l.RateLimitCooldown)
	}
	if l.MinInterval != 0 {
		t.Errorf("explicit zero floor should stick, got %v", l.MinInterval)
	}
	if l.MaxSyncDepth != 7 {
		t.Errorf("depth cap not applied, got %d", l.MaxSyncDepth)
	}
}

func TestEffectiveDepthAppliesAdminCap(t *testing.T) {
	lim := defaultLimits()
	lim.MaxSyncDepth = 3
	setLimits(lim)
	defer setLimits(defaultLimits())

	cases := []struct{ repo, want int32 }{{0, 3}, {10, 3}, {2, 2}}
	for _, tc := range cases {
		cfg := &v1.MirrorConfig{SyncDepth: tc.repo}
		if got := effectiveDepth(cfg); got != int(tc.want) {
			t.Errorf("repo depth %d gave %d, want %d", tc.repo, got, tc.want)
		}
	}
}

func TestSyncStateRoundTrip(t *testing.T) {
	if got := (SyncState{}).Encode(); got != "" {
		t.Errorf("zero state should encode empty, got %q", got)
	}
	st := SyncState{ListETag: `W/"abc"`, Failures: 2, CooldownUntil: time.Now().Add(time.Hour).UTC(), RateLimited: true}
	back := ParseState(st.Encode())
	if back.ListETag != st.ListETag || back.Failures != 2 || !back.RateLimited || !back.CooldownUntil.Equal(st.CooldownUntil) {
		t.Errorf("round trip lost data: %+v vs %+v", back, st)
	}
	if !back.CoolingDown(time.Now()) || back.CoolingDown(time.Now().Add(2*time.Hour)) {
		t.Error("cooldown window wrong")
	}
}

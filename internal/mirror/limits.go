package mirror

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Limits are the admin tunable clamps on upstream syncing
type Limits struct {
	// Spacing between requests to one upstream host
	HostInterval time.Duration
	// Cooldown when the upstream rate limits without saying for how long
	RateLimitCooldown time.Duration
	// Cooldowns never exceed this, headers lie sometimes
	MaxCooldown time.Duration
	// Base of the exponential failure backoff
	BackoffBase time.Duration
	// Bounds a single repo sync so one repo cannot stall the schedule
	SyncTimeout time.Duration
	// Repos synced concurrently per sweep
	Workers int
	// Floor applied to per repo sync intervals, zero disables
	MinInterval time.Duration
	// Cap on releases or tags per sync, zero means unlimited
	MaxSyncDepth int
}

func defaultLimits() Limits {
	return Limits{
		HostInterval:      750 * time.Millisecond,
		RateLimitCooldown: 15 * time.Minute,
		MaxCooldown:       6 * time.Hour,
		BackoffBase:       5 * time.Minute,
		SyncTimeout:       45 * time.Minute,
		Workers:           3,
		MinInterval:       5 * time.Minute,
		MaxSyncDepth:      0,
	}
}

// LimitsFromSettings sanitizes admin values into safe ranges
func LimitsFromSettings(s *v1.MirrorSettings) Limits {
	l := defaultLimits()
	if s == nil {
		return l
	}
	if s.PerHostSpacingMs != nil && s.GetPerHostSpacingMs() >= 0 {
		l.HostInterval = clampDur(time.Duration(s.GetPerHostSpacingMs())*time.Millisecond, 0, time.Minute)
	}
	if s.GetRateLimitCooldownMinutes() > 0 {
		l.RateLimitCooldown = clampDur(time.Duration(s.GetRateLimitCooldownMinutes())*time.Minute, time.Minute, 24*time.Hour)
	}
	if s.GetMaxCooldownMinutes() > 0 {
		l.MaxCooldown = clampDur(time.Duration(s.GetMaxCooldownMinutes())*time.Minute, time.Minute, 7*24*time.Hour)
	}
	if l.MaxCooldown < l.RateLimitCooldown {
		l.MaxCooldown = l.RateLimitCooldown
	}
	if s.GetFailureBackoffMinutes() > 0 {
		l.BackoffBase = clampDur(time.Duration(s.GetFailureBackoffMinutes())*time.Minute, time.Minute, 24*time.Hour)
	}
	if s.GetSyncTimeoutMinutes() > 0 {
		l.SyncTimeout = clampDur(time.Duration(s.GetSyncTimeoutMinutes())*time.Minute, time.Minute, 12*time.Hour)
	}
	if s.GetMaxConcurrentSyncs() > 0 {
		l.Workers = int(min(s.GetMaxConcurrentSyncs(), 32))
	}
	if s.MinIntervalMinutes != nil && s.GetMinIntervalMinutes() >= 0 {
		l.MinInterval = clampDur(time.Duration(s.GetMinIntervalMinutes())*time.Minute, 0, 24*time.Hour)
	}
	if s.GetMaxSyncDepth() > 0 {
		l.MaxSyncDepth = int(s.GetMaxSyncDepth())
	}
	return l
}

func clampDur(d, lo, hi time.Duration) time.Duration {
	if d < lo {
		return lo
	}
	if d > hi {
		return hi
	}
	return d
}

// Snapshot shared with the driver helpers outside the monitor
var activeLimits atomic.Pointer[Limits]

func setLimits(l Limits) {
	activeLimits.Store(&l)
}

func currentLimits() Limits {
	if l := activeLimits.Load(); l != nil {
		return *l
	}
	return defaultLimits()
}

// Admin cap folded over the per repo depth knob
func effectiveDepth(cfg *v1.MirrorConfig) int {
	depth := int(cfg.GetSyncDepth())
	if adm := currentLimits().MaxSyncDepth; adm > 0 && (depth == 0 || depth > adm) {
		return adm
	}
	return depth
}

// Upstream told us to stop, holds the earliest retry instant
type rateLimitedError struct {
	until time.Time
}

func (e *rateLimitedError) Error() string {
	return fmt.Sprintf("upstream rate limited this server until %s", e.until.UTC().Format(time.RFC3339))
}

// RetryAfter surfaces the cooldown deadline from any wrapped error
func RetryAfter(err error) (time.Time, bool) {
	var rl *rateLimitedError
	if errors.As(err, &rl) {
		return rl.until, true
	}
	return time.Time{}, false
}

// Backoff grows exponentially with consecutive failures
func backoffFor(failures int) time.Duration {
	if failures < 1 {
		return 0
	}
	lim := currentLimits()
	d := lim.BackoffBase << (failures - 1)
	if d > lim.MaxCooldown || d < 0 {
		d = lim.MaxCooldown
	}
	// Jitter keeps herds of repos from thundering together
	return d + time.Duration(rand.Int63n(int64(d)/4+1))
}

// Reads 429s and quota exhausted 403s into a typed cooldown
func classifyResponse(resp *http.Response, url string) error {
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotModified {
		return nil
	}
	limited := resp.StatusCode == http.StatusTooManyRequests
	if resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0" {
		limited = true
	}
	if limited {
		return &rateLimitedError{until: retryDeadline(resp.Header)}
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return &upstreamError{status: resp.StatusCode, body: string(body), url: url}
}

// Best effort deadline from Retry-After and vendor reset headers
func retryDeadline(h http.Header) time.Time {
	now := time.Now()
	if ra := strings.TrimSpace(h.Get("Retry-After")); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
			return clampDeadline(now, now.Add(time.Duration(secs)*time.Second))
		}
		if t, err := http.ParseTime(ra); err == nil {
			return clampDeadline(now, t)
		}
	}
	// GitHub and GitLab expose the window reset as unix seconds
	for _, key := range []string{"X-RateLimit-Reset", "RateLimit-Reset"} {
		if v := strings.TrimSpace(h.Get(key)); v != "" {
			if unix, err := strconv.ParseInt(v, 10, 64); err == nil && unix > 0 {
				return clampDeadline(now, time.Unix(unix, 0).Add(30*time.Second))
			}
		}
	}
	return now.Add(currentLimits().RateLimitCooldown)
}

func clampDeadline(now, t time.Time) time.Time {
	maxCooldown := currentLimits().MaxCooldown
	if t.Before(now.Add(time.Minute)) {
		return now.Add(time.Minute)
	}
	if t.After(now.Add(maxCooldown)) {
		return now.Add(maxCooldown)
	}
	return t
}

// Spaces requests per host so upstream abuse detection stays quiet
type pacer struct {
	mu   sync.Mutex
	next map[string]time.Time
}

func newPacer() *pacer {
	return &pacer{next: make(map[string]time.Time)}
}

func (p *pacer) wait(ctx context.Context, host string) error {
	p.mu.Lock()
	now := time.Now()
	at := p.next[host]
	if at.Before(now) {
		at = now
	}
	p.next[host] = at.Add(currentLimits().HostInterval)
	p.mu.Unlock()

	d := time.Until(at)
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Applies pacing to every request including redirect hops
type pacedTransport struct {
	inner http.RoundTripper
	pace  *pacer
}

func (t *pacedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.pace.wait(req.Context(), req.URL.Host); err != nil {
		return nil, err
	}
	return t.inner.RoundTrip(req)
}

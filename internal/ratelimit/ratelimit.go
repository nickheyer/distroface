package ratelimit

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Counts events per key inside sliding window
type Limiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	events map[string][]time.Time

	lastPrune time.Time
}

// Make limiter with given limit and window
func New(limit int, window time.Duration) *Limiter {
	return &Limiter{
		limit:     limit,
		window:    window,
		events:    make(map[string][]time.Time),
		lastPrune: time.Now(),
	}
}

// Max events per window
func (l *Limiter) Limit() int {
	return l.limit
}

// Count event and say if key within quota
func (l *Limiter) Take(key string) (allowed bool, remaining int, resetAt time.Time) {
	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(now)

	kept := l.trimKeyLocked(key, now)
	if len(kept) >= l.limit {
		l.events[key] = kept
		return false, 0, kept[0].Add(l.window)
	}

	kept = append(kept, now)
	l.events[key] = kept
	return true, l.limit - len(kept), kept[0].Add(l.window)
}

// Count failure without checking quota
func (l *Limiter) Record(key string) {
	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(now)
	l.events[key] = append(l.trimKeyLocked(key, now), now)
}

// Says if key hit limit inside window
func (l *Limiter) Blocked(key string) bool {
	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	kept := l.trimKeyLocked(key, now)
	if len(kept) == 0 {
		delete(l.events, key)
		return false
	}
	l.events[key] = kept
	return len(kept) >= l.limit
}

// Wipe events for key after good login
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.events, key)
}

// Drop old events for one key
func (l *Limiter) trimKeyLocked(key string, now time.Time) []time.Time {
	cutoff := now.Add(-l.window)
	events := l.events[key]
	start := 0
	for start < len(events) && events[start].Before(cutoff) {
		start++
	}
	return events[start:]
}

// Drop dead keys so map cannot grow forever
func (l *Limiter) pruneLocked(now time.Time) {
	if now.Sub(l.lastPrune) < l.window {
		return
	}
	l.lastPrune = now
	cutoff := now.Add(-l.window)
	for key, events := range l.events {
		if len(events) == 0 || events[len(events)-1].Before(cutoff) {
			delete(l.events, key)
		}
	}
}

// Client ip from proxy headers then remote addr
func ClientIP(remoteAddr string, h http.Header) string {
	if h != nil {
		if ip := strings.TrimSpace(h.Get("X-Real-IP")); ip != "" {
			return ip
		}
		if xff := h.Get("X-Forwarded-For"); xff != "" {
			if first := strings.TrimSpace(strings.Split(xff, ",")[0]); first != "" {
				return first
			}
		}
	}
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return host
	}
	return remoteAddr
}

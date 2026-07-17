package admin

import (
	"fmt"
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
func NewLimiter(limit int, window time.Duration) *Limiter {
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

var (
	trustedProxiesMu sync.RWMutex
	trustedProxies   []*net.IPNet
)

// Parses cidrs whose forwarded headers are believed, empty trusts none
func SetTrustedProxies(cidrs []string) error {
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, ipnet, err := net.ParseCIDR(strings.TrimSpace(c))
		if err != nil {
			return fmt.Errorf("invalid trusted proxy cidr %q: %w", c, err)
		}
		nets = append(nets, ipnet)
	}
	trustedProxiesMu.Lock()
	trustedProxies = nets
	trustedProxiesMu.Unlock()
	return nil
}

func isTrustedProxy(remoteIP string) bool {
	ip := net.ParseIP(remoteIP)
	if ip == nil {
		return false
	}
	trustedProxiesMu.RLock()
	defer trustedProxiesMu.RUnlock()
	for _, n := range trustedProxies {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// Bare ip from an xff hop, empty when unparsable
func parseHop(hop string) string {
	if net.ParseIP(hop) != nil {
		return hop
	}
	if host, _, err := net.SplitHostPort(hop); err == nil && net.ParseIP(host) != nil {
		return host
	}
	return ""
}

// Rightmost untrusted xff hop from trusted peers else remote addr
func ClientIP(remoteAddr string, h http.Header) string {
	remoteIP := remoteAddr
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		remoteIP = host
	}
	if h == nil || !isTrustedProxy(remoteIP) {
		return remoteIP
	}

	var hops []string
	for _, v := range h.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(v, ",")...)
	}

	// Trusted proxies append, so the client is the first hop from
	// the right that is not a trusted proxy itself
	leftmost := ""
	for i := len(hops) - 1; i >= 0; i-- {
		hop := parseHop(strings.TrimSpace(hops[i]))
		if hop == "" {
			// Unparsable hop means a forged header, believe nothing left of it
			return remoteIP
		}
		if !isTrustedProxy(hop) {
			return hop
		}
		leftmost = hop
	}
	if leftmost != "" {
		return leftmost
	}
	return remoteIP
}

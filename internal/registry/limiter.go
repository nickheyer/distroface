package registry

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nickheyer/distroface/internal/admin"
	"github.com/nickheyer/distroface/pkg/logger"
)

// Checks bearer token and gives back subject
type SubjectVerifier interface {
	VerifyTokenSubject(raw string) (string, error)
}

var manifestPathRe = regexp.MustCompile(`^/v2/.+/manifests/[^/]+$`)

// Throttle manifest pulls per user or per ip
func PullRateLimit(next http.Handler, verifier SubjectVerifier, userLimiter, anonLimiter *admin.Limiter, log *logger.Logger) http.Handler {
	if userLimiter == nil && anonLimiter == nil {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.Method != http.MethodGet && r.Method != http.MethodHead) || !manifestPathRe.MatchString(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		raw, isBearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !isBearer {
			next.ServeHTTP(w, r)
			return
		}

		limiter := anonLimiter
		key := "ip:" + admin.ClientIP(r.RemoteAddr, r.Header)
		if verifier != nil {
			if sub, err := verifier.VerifyTokenSubject(strings.TrimSpace(raw)); err == nil && sub != "" {
				limiter = userLimiter
				key = "user:" + sub
			}
		}
		if limiter == nil {
			next.ServeHTTP(w, r)
			return
		}

		allowed, remaining, resetAt := limiter.Take(key)
		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limiter.Limit()))
		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetAt.Unix(), 10))

		if !allowed {
			retryAfter := int(time.Until(resetAt).Seconds()) + 1
			if retryAfter < 1 {
				retryAfter = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"errors":[{"code":"TOOMANYREQUESTS","message":"pull rate limit exceeded"}]}`))
			log.Warn("registry: pull rate limit exceeded for %s", key)
			return
		}

		next.ServeHTTP(w, r)
	})
}

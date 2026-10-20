package registry

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/nickheyer/distroface/pkg/logger"
)

// Answers whether a job may still use the registry
type JobChecker interface {
	JobRunning(ctx context.Context, jobID string) bool
}

// Job id behind a signed registry token
type JobVerifier interface {
	JobFromToken(raw string) (string, bool)
}

// Set once actions exist, requests before that pass through.
// Atomic so a late SetJobChecker never races per request reads.
var jobCheckerPtr atomic.Pointer[JobChecker]

// SetJobChecker installs the live job status source
func SetJobChecker(c JobChecker) {
	jobCheckerPtr.Store(&c)
}

// Registry tokens minted from a job token die with the job
func JobTokenGuard(next http.Handler, verifier JobVerifier, log *logger.Logger) http.Handler {
	if verifier == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, isBearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !isBearer || !strings.HasPrefix(r.URL.Path, "/v2/") {
			next.ServeHTTP(w, r)
			return
		}
		jobID, ok := verifier.JobFromToken(strings.TrimSpace(raw))
		checker := jobCheckerPtr.Load()
		if !ok || checker == nil || (*checker).JobRunning(r.Context(), jobID) {
			next.ServeHTTP(w, r)
			return
		}
		log.Warn("registry: refused token of finished job %s for %s %s", jobID, r.Method, r.URL.Path)
		w.Header().Set("Www-Authenticate", fmt.Sprintf(`Bearer realm="%s://%s/auth/token",service="distroface-registry",error="invalid_token"`, requestScheme(r), r.Host))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"errors":[{"code":"UNAUTHORIZED","message":"the job behind this token has finished"}]}`))
	})
}

func requestScheme(r *http.Request) string {
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		return strings.TrimSpace(strings.SplitN(proto, ",", 2)[0])
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

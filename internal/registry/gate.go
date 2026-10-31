package registry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/distribution/distribution/v3/registry/api/errcode"
)

var ErrGateFrozen = errors.New("registry writes are already paused")

// Pauses registry writes so garbage collection sweeps a still tree
type WriteGate struct {
	mu       sync.Mutex
	frozen   bool
	inflight int
	drained  chan struct{}
}

func NewWriteGate() *WriteGate {
	return &WriteGate{}
}

// Rejects new writes then waits for running ones
func (g *WriteGate) Freeze(ctx context.Context) error {
	g.mu.Lock()
	if g.frozen {
		g.mu.Unlock()
		return ErrGateFrozen
	}
	g.frozen = true
	if g.inflight == 0 {
		g.mu.Unlock()
		return nil
	}
	drained := make(chan struct{})
	g.drained = drained
	g.mu.Unlock()

	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		g.Thaw()
		return fmt.Errorf("waiting for in flight registry writes: %w", ctx.Err())
	}
}

// Lets writes through again
func (g *WriteGate) Thaw() {
	g.mu.Lock()
	g.frozen = false
	g.drained = nil
	g.mu.Unlock()
}

func (g *WriteGate) enter() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.frozen {
		return false
	}
	g.inflight++
	return true
}

func (g *WriteGate) leave() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.inflight--
	if g.frozen && g.inflight == 0 && g.drained != nil {
		close(g.drained)
		g.drained = nil
	}
}

// Reads pass, writes bounce while frozen
func (g *WriteGate) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isRegistryWrite(r) {
			next.ServeHTTP(w, r)
			return
		}
		if !g.enter() {
			w.Header().Set("Retry-After", "60")
			_ = errcode.ServeJSON(w, errcode.ErrorCodeUnavailable.WithMessage("registry is read only while garbage collection runs, retry shortly"))
			return
		}
		defer g.leave()
		next.ServeHTTP(w, r)
	})
}

func isRegistryWrite(r *http.Request) bool {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return strings.HasPrefix(r.URL.Path, "/v2/")
	}
	return false
}

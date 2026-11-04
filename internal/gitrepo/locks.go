package gitrepo

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Returned when the transfer cap stays full for the whole wait
var ErrBusy = errors.New("server busy, too many concurrent transfers")

// Counting gate whose capacity is read live from settings
type limiter struct {
	mu     sync.Mutex
	cond   *sync.Cond
	active int
	limit  func() int
}

func newLimiter(limit func() int) *limiter {
	l := &limiter{limit: limit}
	l.cond = sync.NewCond(&l.mu)
	return l
}

// Waits up to the timeout for a slot, the returned func releases it
func (l *limiter) Acquire(ctx context.Context, timeout time.Duration) (func(), error) {
	deadline := time.Now().Add(timeout)
	timer := time.AfterFunc(timeout, func() {
		l.mu.Lock()
		l.cond.Broadcast()
		l.mu.Unlock()
	})
	defer timer.Stop()
	stop := context.AfterFunc(ctx, func() {
		l.mu.Lock()
		l.cond.Broadcast()
		l.mu.Unlock()
	})
	defer stop()

	l.mu.Lock()
	defer l.mu.Unlock()
	for {
		limit := l.limit()
		if limit <= 0 || l.active < limit {
			l.active++
			return l.release, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if time.Now().After(deadline) {
			return nil, ErrBusy
		}
		l.cond.Wait()
	}
}

func (l *limiter) release() {
	l.mu.Lock()
	l.active--
	l.cond.Broadcast()
	l.mu.Unlock()
}

// Transfers running right now
func (l *limiter) Active() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.active
}

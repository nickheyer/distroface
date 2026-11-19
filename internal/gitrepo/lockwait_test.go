package gitrepo

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLockRepoWait(t *testing.T) {
	e := &Engine{locks: newLockTable()}
	held := e.LockRepo("r")
	if _, err := e.LockRepoWait(context.Background(), "r", 20*time.Millisecond); !errors.Is(err, ErrRepoBusy) {
		t.Fatalf("want ErrRepoBusy, got %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.LockRepoWait(ctx, "r", time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	held.Unlock()
	lock, err := e.LockRepoWait(context.Background(), "r", time.Second)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	lock.Unlock()
	if n := len(e.locks.locks); n != 0 {
		t.Fatalf("lock table leaked %d entries", n)
	}
}

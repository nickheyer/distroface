package gitrepo

import (
	"context"
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

// Transfers with no bytes moving for this long are cut
const transferIdleTimeout = 5 * time.Minute

// Pushes may carry this much beyond the free quota for pkt-line framing
const pushSlackBytes = 1 << 20

// Tracks the last moment bytes moved on a session
type idleWatch struct {
	last atomic.Int64
}

func (w *idleWatch) touch() { w.last.Store(time.Now().UnixNano()) }

// Fires onIdle once nothing moved for the timeout, stops with the context
func (w *idleWatch) run(ctx context.Context, idle time.Duration, onIdle func()) {
	w.touch()
	t := time.NewTicker(idle / 4)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if time.Since(time.Unix(0, w.last.Load())) > idle {
				onIdle()
				return
			}
		}
	}
}

type touchReader struct {
	r io.Reader
	w *idleWatch
}

func (t *touchReader) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if n > 0 {
		t.w.touch()
	}
	return n, err
}

// Pushes the read deadline out before every read, so stalled clients time out
type deadlineReader struct {
	io.ReadCloser
	rc   *http.ResponseController
	idle time.Duration
}

func (d *deadlineReader) Read(p []byte) (int, error) {
	_ = d.rc.SetReadDeadline(time.Now().Add(d.idle))
	return d.ReadCloser.Read(p)
}

// Reader that fails with a fixed error once more than the budget flows through
type budgetReader struct {
	io.ReadCloser
	left int64
	err  error
}

func (b *budgetReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if b.left <= 0 {
		n, err := b.ReadCloser.Read(p[:1])
		if n > 0 {
			return 0, b.err
		}
		return 0, err
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n, err := b.ReadCloser.Read(p)
	b.left -= int64(n)
	return n, err
}

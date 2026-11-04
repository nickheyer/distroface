package logger

import (
	"io"
	"strconv"
	"sync"
	"sync/atomic"
)

// Lines queued before callers start dropping
const asyncDepth = 8192

// One queued line and its sinks, or a flush marker when line is nil
type logItem struct {
	line []byte
	out  io.Writer
	ack  chan struct{}
}

// Decouples callers from the sinks so no lock is held across a write.
// One goroutine drains every sink in arrival order, so lines from
// different modules keep their relative order. Overflow drops and is
// reported inline
type asyncWriter struct {
	out     io.Writer
	queue   chan logItem
	dropped atomic.Int64
	done    chan struct{}

	// Guards closed so no send races the channel close
	mu     sync.RWMutex
	closed bool
	once   sync.Once
}

func newAsyncWriter(out io.Writer, depth int) *asyncWriter {
	w := &asyncWriter{out: out, queue: make(chan logItem, depth), done: make(chan struct{})}
	go w.drain()
	return w
}

// Extra sinks share the queue so module lines stay ordered
func (w *asyncWriter) sink(out io.Writer) io.Writer {
	return &asyncSink{w: w, out: out}
}

type asyncSink struct {
	w   *asyncWriter
	out io.Writer
}

func (s *asyncSink) Write(p []byte) (int, error) {
	return s.w.write(s.out, p)
}

func (w *asyncWriter) Write(p []byte) (int, error) {
	return w.write(w.out, p)
}

// Never blocks, a full queue drops the line and counts it.
// After Close lines go straight through so late shutdown logs still land
func (w *asyncWriter) write(out io.Writer, p []byte) (int, error) {
	line := make([]byte, len(p))
	copy(line, p)
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.closed {
		_, err := out.Write(line)
		return len(p), err
	}
	select {
	case w.queue <- logItem{line: line, out: out}:
	default:
		w.dropped.Add(1)
	}
	return len(p), nil
}

func (w *asyncWriter) drain() {
	defer close(w.done)
	for item := range w.queue {
		if item.ack != nil {
			close(item.ack)
			continue
		}
		if n := w.dropped.Swap(0); n > 0 {
			_, _ = item.out.Write([]byte("[logger] dropped " + strconv.FormatInt(n, 10) + " log lines under load\n"))
		}
		_, _ = item.out.Write(item.line)
	}
}

// Blocks until every line queued so far reached the sinks
func (w *asyncWriter) Flush() {
	w.mu.RLock()
	if w.closed {
		w.mu.RUnlock()
		return
	}
	ack := make(chan struct{})
	w.queue <- logItem{ack: ack}
	w.mu.RUnlock()
	<-ack
}

// Drains what is queued and stops the goroutine
func (w *asyncWriter) Close() {
	w.once.Do(func() {
		w.mu.Lock()
		w.closed = true
		close(w.queue)
		w.mu.Unlock()
		<-w.done
	})
}

// Lines lost to a full queue so far, for tests and diagnostics
func (w *asyncWriter) Dropped() int64 {
	return w.dropped.Load()
}

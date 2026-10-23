package runner

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
)

const (
	logFlushEvery = time.Second
	logFlushBytes = 32 * 1024
	logSendLimit  = 1024 * 1024
)

// Buffers job output and ships it in ordered chunks
type logStream struct {
	client *Client
	jobID  string

	mu      sync.Mutex
	buf     bytes.Buffer
	partial []byte
	masks   []string
	// Rebuilt lazily after the mask set changes
	replacer *strings.Replacer
	index    int
	failed   error
	dead     bool
	wake     chan struct{}
	done     chan struct{}
	stopped  chan struct{}
}

func newLogStream(client *Client, jobID string) *logStream {
	return &logStream{client: client, jobID: jobID, wake: make(chan struct{}, 1), done: make(chan struct{}), stopped: make(chan struct{})}
}

// Runs the periodic flusher until closed
func (l *logStream) Start(ctx context.Context) {
	go func() {
		defer close(l.stopped)
		t := time.NewTicker(logFlushEvery)
		defer t.Stop()
		for {
			select {
			case <-l.done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				l.flush(ctx, true)
			case <-l.wake:
				l.flush(ctx, false)
			}
		}
	}()
}

// Masks a value, its encodings, and each line
func (l *logStream) AddMask(v string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.addValue(v)
	if strings.ContainsAny(v, "\r\n") {
		for _, line := range strings.FieldsFunc(v, func(r rune) bool { return r == '\n' || r == '\r' }) {
			l.addValue(line)
		}
	}
}

func (l *logStream) addValue(v string) {
	l.addMask(v)
	for _, variant := range maskVariants(v) {
		l.addMask(variant)
	}
}

// Encodings the hosted runner also masks
func maskVariants(v string) []string {
	var out []string
	add := func(s string) {
		if s != "" && s != v {
			out = append(out, s)
		}
	}
	if b, err := json.Marshal(v); err == nil && len(b) >= 2 {
		add(string(b[1 : len(b)-1]))
	}
	add(uriEscape(v))
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		add(v[1 : len(v)-1])
	}
	for shift := 0; shift < 3 && shift < len(v); shift++ {
		add(base64.StdEncoding.EncodeToString([]byte(v[shift:])))
	}
	return out
}

// Percent encodes everything but unreserved characters
func uriEscape(s string) string {
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.', c == '~':
			sb.WriteByte(c)
		default:
			sb.WriteString(fmt.Sprintf("%%%02X", c))
		}
	}
	return sb.String()
}

func (l *logStream) addMask(v string) {
	v = strings.TrimSpace(v)
	if len(v) < 3 {
		return
	}
	for _, m := range l.masks {
		if m == v {
			return
		}
	}
	l.masks = append(l.masks, v)
	l.replacer = nil
}

// One pass over the line, longest mask wins where they overlap
func (l *logStream) mask(s string) string {
	if len(l.masks) == 0 {
		return s
	}
	if l.replacer == nil {
		ordered := make([]string, len(l.masks))
		copy(ordered, l.masks)
		sort.SliceStable(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
		pairs := make([]string, 0, 2*len(ordered))
		for _, m := range ordered {
			pairs = append(pairs, m, "***")
		}
		l.replacer = strings.NewReplacer(pairs...)
	}
	return l.replacer.Replace(s)
}

// Masks text that leaves the log another way
func (l *logStream) Masked(s string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.mask(s)
}

// Masks complete lines, partial lines wait for their newline
func (l *logStream) Write(p []byte) (int, error) {
	l.mu.Lock()
	data := append(l.partial, p...)
	l.partial = nil
	for {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			break
		}
		l.buf.WriteString(l.mask(string(data[:i+1])))
		data = data[i+1:]
	}
	if len(data) > 0 {
		l.partial = append([]byte(nil), data...)
	}
	big := l.buf.Len() >= logFlushBytes
	l.mu.Unlock()
	if big {
		select {
		case l.wake <- struct{}{}:
		default:
		}
	}
	return len(p), nil
}

func (l *logStream) Line(format string, args ...any) {
	s := fmt.Sprintf(format, args...)
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	_, _ = l.Write([]byte(s))
}

// Pending bytes, partial line included when forced
func (l *logStream) take(force bool) []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	if force && len(l.partial) > 0 {
		l.buf.WriteString(l.mask(string(l.partial)))
		l.partial = nil
	}
	if l.buf.Len() == 0 {
		return nil
	}
	n := l.buf.Len()
	if n > logSendLimit {
		n = logSendLimit
	}
	out := make([]byte, n)
	copy(out, l.buf.Bytes()[:n])
	l.buf.Next(n)
	return out
}

func (l *logStream) flush(ctx context.Context, force bool) {
	for {
		chunk := l.take(force)
		if chunk == nil {
			return
		}
		l.mu.Lock()
		dead := l.dead
		l.mu.Unlock()
		if dead {
			continue
		}
		if err := l.send(ctx, chunk); err != nil {
			l.mu.Lock()
			l.failed = err
			l.dead = permanent(err)
			l.mu.Unlock()
			return
		}
		force = false
	}
}

// Explicit indexes dedupe retries, conflicts defer to the server
func (l *logStream) send(ctx context.Context, chunk []byte) error {
	var lastErr error
	for attempt := range 3 {
		sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		next, err := l.client.AppendLog(sctx, l.jobID, l.index, chunk)
		cancel()
		if err == nil {
			l.index = next
			return nil
		}
		lastErr = err
		if isCode(err, connect.CodeFailedPrecondition) {
			sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			next, err = l.client.AppendLog(sctx, l.jobID, -1, chunk)
			cancel()
			if err == nil {
				l.index = next
				return nil
			}
			return err
		}
		if permanent(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt+1) * time.Second):
		}
	}
	return lastErr
}

// Flushes everything left and stops the flusher
func (l *logStream) Close(ctx context.Context) error {
	select {
	case <-l.done:
	default:
		close(l.done)
	}
	<-l.stopped
	l.flush(ctx, true)
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.failed
}

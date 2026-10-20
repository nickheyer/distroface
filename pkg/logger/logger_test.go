package logger

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

func TestAsyncWriterKeepsOrderAndFlushesOnClose(t *testing.T) {
	var buf bytes.Buffer
	w := newAsyncWriter(&buf, 16)
	for i := range 200 {
		if _, err := fmt.Fprintf(w, "line %d\n", i); err != nil {
			t.Fatal(err)
		}
		// Keep the queue from overflowing, this test is about order
		if i%8 == 7 {
			w.Flush()
		}
	}
	w.Close()
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 200 {
		t.Fatalf("got %d lines, want 200 (dropped %d)", len(lines), w.Dropped())
	}
	for i, line := range lines {
		if want := fmt.Sprintf("line %d", i); line != want {
			t.Fatalf("line %d = %q, want %q", i, line, want)
		}
	}
}

func TestAsyncWriterDropsWhenFullAndReportsIt(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	var got []string
	var first sync.Once
	slow := writerFunc(func(p []byte) (int, error) {
		first.Do(func() {
			close(entered)
			<-release
		})
		mu.Lock()
		got = append(got, strings.TrimSpace(string(p)))
		mu.Unlock()
		return len(p), nil
	})
	w := newAsyncWriter(slow, 2)
	_, _ = w.Write([]byte("one\n"))
	<-entered
	_, _ = w.Write([]byte("two\n"))
	_, _ = w.Write([]byte("three\n"))
	_, _ = w.Write([]byte("four\n"))
	if w.Dropped() != 1 {
		t.Fatalf("dropped = %d, want 1", w.Dropped())
	}
	close(release)
	w.Close()
	mu.Lock()
	defer mu.Unlock()
	want := []string{"one", "[logger] dropped 1 log lines under load", "two", "three"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("sink saw %q, want %q", got, want)
	}
}

func TestAsyncWriterWritesThroughAfterClose(t *testing.T) {
	var buf bytes.Buffer
	w := newAsyncWriter(&buf, 4)
	w.Close()
	w.Close()
	if _, err := w.Write([]byte("late\n")); err != nil {
		t.Fatal(err)
	}
	w.Flush()
	if buf.String() != "late\n" {
		t.Errorf("after close sink = %q", buf.String())
	}
}

func TestLoggerConcurrentWritersAllReachTheFile(t *testing.T) {
	dir := t.TempDir()
	l := NewWithConfig(&Config{Enabled: true, Dir: dir, DefaultModule: "app", MaxSize: 10})
	const writers, perWriter = 8, 50
	var wg sync.WaitGroup
	for w := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range perWriter {
				l.Info("writer %d line %d", w, i)
			}
		}()
	}
	wg.Wait()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "app.log"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != writers*perWriter {
		t.Fatalf("file has %d lines, want %d", len(lines), writers*perWriter)
	}
	for _, line := range lines {
		if !strings.Contains(line, "[app] INFO: writer ") {
			t.Fatalf("malformed line %q", line)
		}
	}
	if recent := l.GetRecentLogs(); len(recent) != writers*perWriter {
		t.Errorf("recent buffer holds %d lines", len(recent))
	}
}

func TestLoggerModuleFlushesWithParent(t *testing.T) {
	dir := t.TempDir()
	root := NewWithConfig(&Config{Enabled: true, Dir: dir, DefaultModule: "root", MaxSize: 10})
	child := root.Module("child")
	child.Warn("from the child")
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "child.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "[child] WARN: from the child") {
		t.Errorf("child file = %q", raw)
	}
}

func TestLoggerWriteWrapsExternalLines(t *testing.T) {
	dir := t.TempDir()
	l := NewWithConfig(&Config{Enabled: true, Dir: dir, DefaultModule: "ext", MaxSize: 10})
	if _, err := l.Write([]byte("level=info msg=\"authorized request\"\n")); err != nil {
		t.Fatal(err)
	}
	l.Flush()
	raw, err := os.ReadFile(filepath.Join(dir, "ext.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "[ext] INFO: level=info msg=\"authorized request\"") {
		t.Errorf("file = %q", raw)
	}
	_ = l.Close()
}

package runner

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
)

type ctxKey struct{}

// Reports a request that never left the process
func InProcess(ctx context.Context) bool {
	v, _ := ctx.Value(ctxKey{}).(bool)
	return v
}

// Routes one host into a handler, the rest go out
type Transport struct {
	Host    string
	Handler http.Handler
	// Fallback for every other host, the default transport when nil
	Next http.RoundTripper
}

func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != t.Host {
		next := t.Next
		if next == nil {
			next = http.DefaultTransport
		}
		return next.RoundTrip(req)
	}
	// No tls state, a faked one would leak https into built links
	r2 := req.Clone(context.WithValue(req.Context(), ctxKey{}, true))
	r2.RequestURI = r2.URL.RequestURI()
	r2.RemoteAddr = "127.0.0.1:0"
	if r2.Body == nil {
		r2.Body = http.NoBody
	}
	pr, pw := io.Pipe()
	w := &pipeWriter{header: make(http.Header), pw: pw, ready: make(chan struct{})}
	go func() {
		// RoundTripper contract requires closing the request body
		defer r2.Body.Close()
		// A panicking handler must not take the process down
		defer func() {
			if r := recover(); r != nil {
				w.abort(fmt.Errorf("handler panic: %v", r))
				return
			}
			w.close()
		}()
		t.Handler.ServeHTTP(w, r2)
	}()
	select {
	case <-w.ready:
	case <-req.Context().Done():
		_ = pr.Close()
		return nil, req.Context().Err()
	}
	length := int64(-1)
	if v := w.header.Get("Content-Length"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			length = n
		}
	}
	return &http.Response{
		Status: http.StatusText(w.status), StatusCode: w.status,
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: w.header, Body: pr, ContentLength: length, Request: req,
	}, nil
}

// Streams a handler response without buffering it in memory
type pipeWriter struct {
	header http.Header
	pw     *io.PipeWriter
	status int
	once   sync.Once
	ready  chan struct{}
}

func (w *pipeWriter) Header() http.Header { return w.header }

func (w *pipeWriter) WriteHeader(code int) {
	w.once.Do(func() {
		w.status = code
		close(w.ready)
	})
}

func (w *pipeWriter) Write(b []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	return w.pw.Write(b)
}

func (w *pipeWriter) Flush() {}

func (w *pipeWriter) close() {
	w.WriteHeader(http.StatusOK)
	_ = w.pw.Close()
}

// Fails the response, a 500 when headers never went out
func (w *pipeWriter) abort(err error) {
	w.once.Do(func() {
		w.status = http.StatusInternalServerError
		close(w.ready)
	})
	_ = w.pw.CloseWithError(err)
}

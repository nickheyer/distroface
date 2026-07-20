package mirror

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
)

// Serves oci requests straight into the embedded registry handler
type inprocTransport struct {
	handler http.Handler
	token   func() (string, error)
}

func (t *inprocTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tok, err := t.token()
	if err != nil {
		return nil, fmt.Errorf("minting registry token: %w", err)
	}
	r2 := req.Clone(req.Context())
	r2.Header.Set("Authorization", "Bearer "+tok)
	r2.RequestURI = r2.URL.RequestURI()
	if r2.Body == nil {
		r2.Body = http.NoBody
	}

	pr, pw := io.Pipe()
	w := &pipeResponseWriter{header: make(http.Header), pw: pw, ready: make(chan struct{})}
	go func() {
		// RoundTripper contract requires closing the request body
		defer r2.Body.Close()
		// A panicking handler must not take the process down
		defer func() {
			if r := recover(); r != nil {
				w.abort(fmt.Errorf("registry handler panic: %v", r))
				return
			}
			w.close()
		}()
		t.handler.ServeHTTP(w, r2)
	}()
	<-w.ready

	// Clients like gcr read the struct field, not the header
	length := int64(-1)
	if v := w.header.Get("Content-Length"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			length = n
		}
	}

	return &http.Response{
		Status:        http.StatusText(w.status),
		StatusCode:    w.status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        w.header,
		Body:          pr,
		ContentLength: length,
		Request:       req,
	}, nil
}

// Streams a handler response without buffering blobs in memory
type pipeResponseWriter struct {
	header http.Header
	pw     *io.PipeWriter
	status int
	once   sync.Once
	ready  chan struct{}
}

func (w *pipeResponseWriter) Header() http.Header { return w.header }

func (w *pipeResponseWriter) WriteHeader(code int) {
	w.once.Do(func() {
		w.status = code
		close(w.ready)
	})
}

func (w *pipeResponseWriter) Write(b []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	return w.pw.Write(b)
}

func (w *pipeResponseWriter) Flush() {}

func (w *pipeResponseWriter) close() {
	w.WriteHeader(http.StatusOK)
	w.pw.Close()
}

// Fails the response, a 500 when headers never went out
func (w *pipeResponseWriter) abort(err error) {
	w.once.Do(func() {
		w.status = http.StatusInternalServerError
		close(w.ready)
	})
	w.pw.CloseWithError(err)
}

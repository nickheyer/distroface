package runner

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTransportRoutesOnlyItsHost(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil {
			t.Error("in process requests must not claim tls")
		}
		if !InProcess(r.Context()) {
			t.Error("in process requests must be marked")
		}
		w.Header().Set("X-Seen", r.Host+r.URL.Path)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, "body")
	})
	outside := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "outside")
	}))
	defer outside.Close()
	client := &http.Client{Transport: &Transport{Host: "inner.test", Handler: inner}}

	resp, err := client.Post("http://inner.test/rpc", "text/plain", strings.NewReader("hi"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated || string(body) != "body" || resp.Header.Get("X-Seen") != "inner.test/rpc" {
		t.Fatalf("in process response = %d %q %q", resp.StatusCode, body, resp.Header.Get("X-Seen"))
	}

	resp, err = client.Get(outside.URL + "/x")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "outside" {
		t.Fatalf("other hosts must reach the network, got %q", body)
	}
}

func TestTransportSurvivesHandlerPanic(t *testing.T) {
	client := &http.Client{Transport: &Transport{Host: "inner.test", Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})}}
	resp, err := client.Get("http://inner.test/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

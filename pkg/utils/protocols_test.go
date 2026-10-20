package utils

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCleartextProtocolsServeBothVersions(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.Proto)
	}))
	srv.Config.Protocols = CleartextProtocols()
	srv.Start()
	defer srv.Close()

	h2 := &http.Transport{Protocols: new(http.Protocols)}
	h2.Protocols.SetUnencryptedHTTP2(true)
	for name, client := range map[string]*http.Client{"http1": srv.Client(), "h2c": {Transport: h2}} {
		resp, err := client.Get(srv.URL)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		want := "HTTP/1.1"
		if name == "h2c" {
			want = "HTTP/2.0"
		}
		if string(body) != want {
			t.Fatalf("%s: served %s, want %s", name, body, want)
		}
	}
}

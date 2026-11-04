package container

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/nickheyer/distroface/pkg/logger"
)

func TestProfilerOffWithoutAddress(t *testing.T) {
	p, err := startProfiler("", logger.New())
	if err != nil || p != nil {
		t.Fatalf("startProfiler(\"\") = %v, %v", p, err)
	}
	p.Close()
}

func TestProfilerServesIndexAndNamedProfiles(t *testing.T) {
	p, err := startProfiler("127.0.0.1:0", logger.New())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for _, path := range []string{"/debug/pprof/", "/debug/pprof/mutex?debug=1", "/debug/pprof/block?debug=1", "/debug/pprof/goroutine?debug=1"} {
		resp, err := http.Get("http://" + p.Addr() + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s = %d %s", path, resp.StatusCode, body)
		}
	}
	resp, err := http.Get("http://" + p.Addr() + "/debug/pprof/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "heap") {
		t.Errorf("index lacks the heap profile: %s", body)
	}
}

func TestProfilerRefusesTakenPort(t *testing.T) {
	first, err := startProfiler("127.0.0.1:0", logger.New())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := startProfiler(first.Addr(), logger.New()); err == nil {
		t.Error("second profiler bound a taken port")
	}
}

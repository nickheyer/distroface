package runner

import (
	"context"
	"errors"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
)

// Append only fake, the embedded interface answers nothing else
type fakeLogRPC struct {
	distrofacev1connect.RunnerServiceClient
	fail  error
	got   []string
	index int
}

func (f *fakeLogRPC) AppendTaskLog(_ context.Context, req *v1.AppendTaskLogRequest) (*v1.AppendTaskLogResponse, error) {
	if f.fail != nil {
		return nil, f.fail
	}
	if int(req.Index) != f.index {
		return nil, connect.NewError(connect.CodeFailedPrecondition, "bad index")
	}
	f.got = append(f.got, string(req.Data))
	f.index++
	return &v1.AppendTaskLogResponse{NextIndex: int32(f.index)}, nil
}

func TestLogStreamKeepsChunkAfterTransientFailure(t *testing.T) {
	rpc := &fakeLogRPC{fail: connect.NewError(connect.CodeUnavailable, errors.New("down").Error())}
	l := newLogStream(&Client{rpc: rpc}, "job1")
	l.Line("first line")
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	l.flush(cancelled, true)
	if l.failed == nil || l.dead {
		t.Fatalf("failed = %v dead = %v", l.failed, l.dead)
	}
	l.Line("second line")
	rpc.fail = nil
	l.flush(context.Background(), true)
	if strings.Join(rpc.got, "") != "first line\nsecond line\n" {
		t.Fatalf("server got %q", rpc.got)
	}
	if l.buf.Len() != 0 {
		t.Fatalf("buffer still holds %q", l.buf.String())
	}
}

func TestLogStreamDropsAfterPermanentFailure(t *testing.T) {
	rpc := &fakeLogRPC{fail: connect.NewError(connect.CodePermissionDenied, "no")}
	l := newLogStream(&Client{rpc: rpc}, "job1")
	l.Line("gone")
	l.flush(context.Background(), true)
	if !l.dead || l.buf.Len() != 0 {
		t.Fatalf("dead = %v buffered = %q", l.dead, l.buf.String())
	}
}

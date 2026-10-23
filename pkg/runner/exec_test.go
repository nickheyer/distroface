package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Fake server serving a fixed command list
type fakeRunnerRPC struct {
	mu        sync.Mutex
	log       strings.Builder
	queue     []*v1.ExecRequest
	completed []*v1.CompleteExecRequest
	finished  *v1.CompleteTaskRequest
}

var errNotServed = errors.New("not served by the fake")

func (f *fakeRunnerRPC) RegisterRunner(context.Context, *v1.RegisterRunnerRequest) (*v1.RegisterRunnerResponse, error) {
	return nil, errNotServed
}
func (f *fakeRunnerRPC) Heartbeat(context.Context, *v1.HeartbeatRequest) (*v1.HeartbeatResponse, error) {
	return &v1.HeartbeatResponse{}, nil
}
func (f *fakeRunnerRPC) FetchTask(context.Context, *v1.FetchTaskRequest) (*v1.FetchTaskResponse, error) {
	return nil, errNotServed
}
func (f *fakeRunnerRPC) UpdateTask(context.Context, *v1.UpdateTaskRequest) (*v1.UpdateTaskResponse, error) {
	return &v1.UpdateTaskResponse{}, nil
}
func (f *fakeRunnerRPC) AppendTaskLog(_ context.Context, req *v1.AppendTaskLogRequest) (*v1.AppendTaskLogResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log.Write(req.Data)
	return &v1.AppendTaskLogResponse{NextIndex: req.Index + 1}, nil
}
func (f *fakeRunnerRPC) CompleteTask(_ context.Context, req *v1.CompleteTaskRequest) (*v1.CompleteTaskResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finished = req
	return &v1.CompleteTaskResponse{}, nil
}
func (f *fakeRunnerRPC) UploadTaskArtifact(context.Context, *v1.UploadTaskArtifactRequest) (*v1.UploadTaskArtifactResponse, error) {
	return nil, errNotServed
}
func (f *fakeRunnerRPC) FinishTaskArtifact(context.Context, *v1.FinishTaskArtifactRequest) (*v1.FinishTaskArtifactResponse, error) {
	return nil, errNotServed
}
func (f *fakeRunnerRPC) ListTaskArtifacts(context.Context, *v1.ListTaskArtifactsRequest) (*v1.ListTaskArtifactsResponse, error) {
	return &v1.ListTaskArtifactsResponse{}, nil
}
func (f *fakeRunnerRPC) FetchExec(context.Context, *v1.FetchExecRequest) (*v1.FetchExecResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.queue) == 0 {
		return &v1.FetchExecResponse{Done: true}, nil
	}
	next := f.queue[0]
	f.queue = f.queue[1:]
	return &v1.FetchExecResponse{Exec: next}, nil
}
func (f *fakeRunnerRPC) CompleteExec(_ context.Context, req *v1.CompleteExecRequest) (*v1.CompleteExecResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completed = append(f.completed, req)
	return &v1.CompleteExecResponse{}, nil
}

func TestInteractiveJobServesCommands(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("needs a posix shell")
	}
	dir := t.TempDir()
	fake := &fakeRunnerRPC{queue: []*v1.ExecRequest{
		{Id: "e1", Number: 1, Script: "echo prepared > prepared.txt\ncat prepared.txt\necho hello from exec\nexit 3"},
		{Id: "e2", Number: 2, Script: "cat prepared.txt\necho second"},
	}}
	r := &Runner{
		cfg:     &Config{Name: "r", HostEnabled: true, Images: map[string]string{"host": HostExecutor}, WorkDir: dir, Concurrency: 1},
		client:  &Client{rpc: fake, http: &http.Client{}},
		out:     log.New(io.Discard, "", 0),
		running: map[string]*job{},
		pulled:  map[string]bool{},
		ext:     newExternals(filepath.Join(dir, "_externals"), &http.Client{}),
	}
	task := &v1.Task{JobId: "job1", RunId: "run1", RepoFullName: "acme/app", JobName: "session", RunsOn: []string{"host"}, Interactive: true, Context: "{}", TimeoutMinutes: 5}
	j := newJob(r, task)
	j.run(context.Background())

	if fake.finished == nil || fake.finished.Conclusion != v1.ActionsConclusion_ACTIONS_CONCLUSION_SUCCESS {
		t.Fatalf("job must finish with success once the server says done, got %+v", fake.finished)
	}
	if len(fake.completed) != 2 {
		t.Fatalf("completed execs = %d", len(fake.completed))
	}
	first := fake.completed[0]
	if first.ExecId != "e1" || first.ExitCode != 3 || first.Conclusion != v1.ActionsConclusion_ACTIONS_CONCLUSION_FAILURE || first.Error != "" {
		t.Fatalf("first = %+v", first)
	}
	if first.Output != "prepared\nhello from exec\n" {
		t.Fatalf("first output = %q", first.Output)
	}
	// The working tree survives between commands
	second := fake.completed[1]
	if second.ExitCode != 0 || second.Conclusion != v1.ActionsConclusion_ACTIONS_CONCLUSION_SUCCESS || second.Output != "prepared\nsecond\n" {
		t.Fatalf("second = %+v", second)
	}
	if !strings.Contains(fake.log.String(), "hello from exec") {
		t.Fatal("command output must also reach the job log")
	}
	names := make([]string, 0, len(fake.finished.Steps))
	for _, s := range fake.finished.Steps {
		names = append(names, s.Name)
	}
	if strings.Join(names, ",") != "Set up job,Command 1,Command 2,Complete job" {
		t.Fatalf("steps = %v", names)
	}
}

func TestHeadTailKeepsBothEnds(t *testing.T) {
	b := &headTail{keep: 1024}
	var full strings.Builder
	for i := range 2000 {
		line := fmt.Sprintf("line of build output number %d\n", i)
		full.WriteString(line)
		_, _ = b.Write([]byte(line))
	}
	got := b.String()
	if len(got) >= full.Len() || !strings.Contains(got, "bytes of output elided") {
		t.Fatalf("long output must be cut, got %d bytes", len(got))
	}
	if !strings.HasPrefix(got, "line of build output number 0\n") || !strings.HasSuffix(got, "number 1999\n") {
		t.Fatalf("both ends must survive whole lines, got %q ... %q", got[:40], got[len(got)-40:])
	}
	short := &headTail{keep: 1024}
	_, _ = short.Write([]byte("short\n"))
	if short.String() != "short\n" {
		t.Fatal("short output must pass through untouched")
	}
}

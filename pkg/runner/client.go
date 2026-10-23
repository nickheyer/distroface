package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
)

// Mirrors the server side runner token header
const runnerTokenHeader = "X-Runner-Token"

// Runner token for rpc, job tokens for the data plane
type Client struct {
	BaseURL string
	token   string
	http    *http.Client
	rpc     distrofacev1connect.RunnerServiceClient
}

func NewClient(baseURL, token string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	baseURL = strings.TrimRight(baseURL, "/")
	c := &Client{BaseURL: baseURL, token: token, http: httpClient}
	c.rpc = distrofacev1connect.NewRunnerServiceClient(connect.NewClient(connecthttp.NewTransport(httpClient, baseURL), c.tokenInterceptor()))
	return c
}

func (c *Client) tokenInterceptor() connect.ClientInterceptor {
	return func(next connect.ClientFunc) connect.ClientFunc {
		return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
			if info, ok := connect.CallInfoForClientContext(ctx); ok && c.token != "" {
				info.RequestHeader().Set(runnerTokenHeader, c.token)
			}
			return next(ctx, spec)
		}
	}
}

func (c *Client) Register(ctx context.Context, registration, name string, labels []string, version string) (*v1.Runner, string, error) {
	resp, err := c.rpc.RegisterRunner(ctx, &v1.RegisterRunnerRequest{RegistrationToken: registration, RunnerName: name, Labels: labels, Version: version, Capabilities: Capabilities})
	if err != nil {
		return nil, "", rpcErr(err)
	}
	return resp.Runner, resp.RunnerToken, nil
}

func (c *Client) Heartbeat(ctx context.Context, version string, running []string, reportsJobs bool) error {
	_, err := c.rpc.Heartbeat(ctx, &v1.HeartbeatRequest{Version: version, RunningJobIds: running, ReportsJobs: reportsJobs, Capabilities: Capabilities})
	return rpcErr(err)
}

// Nil task when the poll window closes without work
func (c *Client) FetchTask(ctx context.Context, wait time.Duration) (*v1.Task, error) {
	resp, err := c.rpc.FetchTask(ctx, &v1.FetchTaskRequest{WaitSeconds: int32(wait / time.Second)})
	if err != nil {
		return nil, rpcErr(err)
	}
	return resp.Task, nil
}

func (c *Client) UpdateTask(ctx context.Context, jobID string, steps []*v1.JobStep, outputs, annotations string) (bool, error) {
	resp, err := c.rpc.UpdateTask(ctx, &v1.UpdateTaskRequest{JobId: jobID, Steps: steps, Outputs: outputs, Annotations: annotations})
	if err != nil {
		return false, rpcErr(err)
	}
	return resp.Cancelled, nil
}

func (c *Client) AppendLog(ctx context.Context, jobID string, index int, data []byte) (int, error) {
	resp, err := c.rpc.AppendTaskLog(ctx, &v1.AppendTaskLogRequest{JobId: jobID, Index: int32(index), Data: data})
	if err != nil {
		return 0, rpcErr(err)
	}
	return int(resp.NextIndex), nil
}

func (c *Client) CompleteTask(ctx context.Context, jobID string, conclusion v1.ActionsConclusion, steps []*v1.JobStep, outputs, annotations string) error {
	_, err := c.rpc.CompleteTask(ctx, &v1.CompleteTaskRequest{JobId: jobID, Conclusion: conclusion, Steps: steps, Outputs: outputs, Annotations: annotations})
	return rpcErr(err)
}

// Streams one file through an upload session
// Uploads one zipped artifact for the job's run
func (c *Client) UploadArtifact(ctx context.Context, jobID, jobToken, name string, body io.Reader, fileCount, retentionDays int32, overwrite bool) (*v1.RunArtifact, error) {
	begin, err := c.rpc.UploadTaskArtifact(ctx, &v1.UploadTaskArtifactRequest{JobId: jobID, ArtifactName: name, Overwrite: overwrite})
	if err != nil {
		return nil, rpcErr(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, c.BaseURL+begin.UploadUrl, body)
	if err != nil {
		return nil, err
	}
	if jobToken != "" {
		req.Header.Set("Authorization", "Bearer "+jobToken)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upload %s: %w", name, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("upload %s: server returned %d", name, resp.StatusCode)
	}
	fin, err := c.rpc.FinishTaskArtifact(ctx, &v1.FinishTaskArtifactRequest{
		JobId: jobID, ArtifactName: name, UploadId: begin.UploadId, FileCount: fileCount, RetentionDays: retentionDays, Overwrite: overwrite,
	})
	if err != nil {
		return nil, rpcErr(err)
	}
	return fin.Artifact, nil
}

// Artifacts the job's run has uploaded so far
func (c *Client) ListTaskArtifacts(ctx context.Context, jobID string) ([]*v1.RunArtifact, error) {
	resp, err := c.rpc.ListTaskArtifacts(ctx, &v1.ListTaskArtifactsRequest{JobId: jobID})
	if err != nil {
		return nil, rpcErr(err)
	}
	return resp.Artifacts, nil
}

// Streams an artifact zip from its data plane path
func (c *Client) DownloadArtifact(ctx context.Context, jobToken, downloadPath string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+downloadPath, nil)
	if err != nil {
		return nil, err
	}
	if jobToken != "" {
		req.Header.Set("Authorization", "Bearer "+jobToken)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return resp.Body, nil
}

// Connect errors already carry the code and stay matchable
func rpcErr(err error) error {
	return err
}

func isCode(err error, code connect.Code) bool {
	var ce *connect.Error
	return errors.As(err, &ce) && ce.Code() == code
}

// Codes the server answers deliberately, retrying cannot change them
func permanent(err error) bool {
	var ce *connect.Error
	if !errors.As(err, &ce) {
		return false
	}
	switch ce.Code() {
	case connect.CodeNotFound, connect.CodeInvalidArgument, connect.CodePermissionDenied, connect.CodeUnauthenticated, connect.CodeFailedPrecondition, connect.CodeResourceExhausted:
		return true
	}
	return false
}

// Protocol features this runner announces to the server
var Capabilities = []string{"exec"}

// Nil exec with done false when the poll window closed without a command
func (c *Client) FetchExec(ctx context.Context, jobID string, wait time.Duration) (*v1.FetchExecResponse, error) {
	resp, err := c.rpc.FetchExec(ctx, &v1.FetchExecRequest{JobId: jobID, WaitSeconds: int32(wait / time.Second)})
	if err != nil {
		return nil, rpcErr(err)
	}
	return resp, nil
}

func (c *Client) CompleteExec(ctx context.Context, req *v1.CompleteExecRequest) error {
	_, err := c.rpc.CompleteExec(ctx, req)
	return rpcErr(err)
}

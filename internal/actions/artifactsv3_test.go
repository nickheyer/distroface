package actions

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// One pipeline call with the runtime bearer, body decoded when it is json
func (h *resultsHarness) pipeline(t *testing.T, token, method, target string, headers map[string]string, body []byte) (int, map[string]any, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, target, bytes.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		_ = json.Unmarshal(raw, &out)
	}
	return resp.StatusCode, out, raw
}

// Uploads one file the way the v1 toolkit does, in ranges with md5 and length headers
func (h *resultsHarness) uploadV3File(t *testing.T, token, uploadURL, itemPath string, content []byte, gzipped bool, chunk int) {
	t.Helper()
	wire := content
	if gzipped {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		_, _ = gz.Write(content)
		_ = gz.Close()
		wire = buf.Bytes()
	}
	target := uploadURL + "&itemPath=" + url.QueryEscape(itemPath)
	if !strings.Contains(uploadURL, "?") {
		target = uploadURL + "?itemPath=" + url.QueryEscape(itemPath)
	}
	send := func(start, end int) {
		part := wire[start : end+1]
		sum := md5.Sum(part)
		headers := map[string]string{
			"Content-Type": "application/octet-stream", "Content-Range": fmt.Sprintf("bytes %d-%d/%d", start, end, len(wire)),
			"x-tfs-filelength": fmt.Sprint(len(content)), "x-actions-results-md5": base64.StdEncoding.EncodeToString(sum[:]),
		}
		if gzipped {
			headers["Content-Encoding"] = "gzip"
		}
		if status, body, raw := h.pipeline(t, token, http.MethodPut, target, headers, part); status != http.StatusOK {
			t.Fatalf("chunk %d-%d of %s: %d %v %s", start, end, itemPath, status, body, raw)
		}
	}
	if len(wire) == 0 {
		send(0, -1)
		return
	}
	for start := 0; start < len(wire); start += chunk {
		end := start + chunk - 1
		if end >= len(wire) {
			end = len(wire) - 1
		}
		send(start, end)
	}
}

func readZip(t *testing.T, data []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(b)
	}
	return out
}

func TestPipelineAPIUploadListDownload(t *testing.T) {
	h := newResultsHarness(t)
	token, err := h.m.runtimeToken(h.job.ID, h.run.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	base := h.srv.URL + PipelineAPIPrefix + "/_apis/pipelines/workflows/" + h.run.ID + "/artifacts"
	create := []byte(`{"Type":"actions_storage","Name":"dist","RetentionDays":3}`)
	if status, _, _ := h.pipeline(t, "", http.MethodPost, base+"?api-version=6.0-preview", nil, create); status != http.StatusUnauthorized {
		t.Fatalf("no token answered %d", status)
	}
	foreign := h.srv.URL + PipelineAPIPrefix + "/_apis/pipelines/workflows/other/artifacts"
	if status, _, _ := h.pipeline(t, token, http.MethodPost, foreign+"?api-version=6.0-preview", nil, create); status != http.StatusBadRequest {
		t.Fatalf("foreign run answered %d", status)
	}
	status, body, raw := h.pipeline(t, token, http.MethodPost, base+"?api-version=6.0-preview", nil, create)
	if status != http.StatusOK {
		t.Fatalf("create container: %d %s", status, raw)
	}
	container, _ := body["fileContainerResourceUrl"].(string)
	hash := fmt.Sprintf("%x", md5.Sum([]byte("dist")))
	if !strings.Contains(container, "/artifacts/"+hash+"/upload?retentionDays=3") {
		t.Fatalf("container url = %q", container)
	}
	upload := h.local(container)

	plain := []byte("hello pipeline world")
	nested := bytes.Repeat([]byte("nested content line\n"), 200)
	h.uploadV3File(t, token, upload, "dist/a.txt", plain, false, 8)
	h.uploadV3File(t, token, upload, "dist\\nested\\file.txt", nested, true, 1000)
	h.uploadV3File(t, token, upload, "dist/empty.txt", nil, false, 8)
	// A retried range lands on the same chunk without harm
	h.uploadV3File(t, token, upload, "dist/a.txt", plain, false, 8)

	badSum := map[string]string{"Content-Range": "bytes 0-4/5", "x-actions-results-md5": "AAAAAAAAAAAAAAAAAAAAAA=="}
	if status, _, _ := h.pipeline(t, token, http.MethodPut, upload+"&itemPath=dist%2Fbad.txt", badSum, []byte("12345")); status != http.StatusBadRequest {
		t.Fatalf("bad md5 answered %d", status)
	}
	wrongHash := strings.Replace(upload, hash, strings.Repeat("0", 32), 1)
	if status, _, _ := h.pipeline(t, token, http.MethodPut, wrongHash+"&itemPath=dist%2Fx.txt", map[string]string{"Content-Range": "bytes 0-0/1"}, []byte("x")); status != http.StatusBadRequest {
		t.Fatalf("wrong hash answered %d", status)
	}

	finalize := base + "?api-version=6.0-preview&artifactName=dist"
	if status, _, raw := h.pipeline(t, token, http.MethodPatch, finalize, nil, []byte(`{"Size":4020}`)); status != http.StatusOK {
		t.Fatalf("finalize: %d %s", status, raw)
	}
	if status, _, raw := h.pipeline(t, token, http.MethodPatch, finalize, nil, []byte(`{"Size":4020}`)); status != http.StatusOK {
		t.Fatalf("repeated finalize: %d %s", status, raw)
	}
	rows, err := h.m.RunArtifacts(context.Background(), h.run)
	if err != nil || len(rows) != 1 || rows[0].Name != "dist" || rows[0].Number != 1 || rows[0].ContentType != "application/zip" || rows[0].ExpiresAt == nil {
		t.Fatalf("run artifacts = %+v, %v", rows, err)
	}

	status, body, raw = h.pipeline(t, token, http.MethodGet, base+"?api-version=6.0-preview", nil, nil)
	values, _ := body["value"].([]any)
	if status != http.StatusOK || body["count"] != float64(1) || len(values) != 1 {
		t.Fatalf("list: %d %s", status, raw)
	}
	item := values[0].(map[string]any)
	if item["name"] != "dist" || !strings.HasSuffix(item["fileContainerResourceUrl"].(string), "/"+hash+"/path") {
		t.Fatalf("listed = %v", item)
	}

	status, body, raw = h.pipeline(t, token, http.MethodGet, h.local(item["fileContainerResourceUrl"].(string))+"?itemPath=dist", nil, nil)
	files, _ := body["value"].([]any)
	if status != http.StatusOK || len(files) != 3 {
		t.Fatalf("items: %d %s", status, raw)
	}
	want := map[string][]byte{"dist/a.txt": plain, "dist/nested/file.txt": nested, "dist/empty.txt": {}}
	for _, f := range files {
		entry := f.(map[string]any)
		p := entry["path"].(string)
		expected, ok := want[p]
		if !ok || entry["itemType"] != "file" {
			t.Fatalf("unexpected item %v", entry)
		}
		status, _, data := h.pipeline(t, token, http.MethodGet, h.local(entry["contentLocation"].(string)), nil, nil)
		if status != http.StatusOK || !bytes.Equal(data, expected) {
			t.Fatalf("download %s: %d %d bytes", p, status, len(data))
		}
		delete(want, p)
	}
	if len(want) != 0 {
		t.Fatalf("items missing: %v", want)
	}

	// The v4 toolkit downloads the same upload as one zip
	status, body = h.twirp(t, token, "GetSignedArtifactURL", map[string]any{"workflow_run_backend_id": h.run.ID, "workflow_job_run_backend_id": h.job.ID, "name": "dist"})
	if status != http.StatusOK {
		t.Fatalf("signed url: %d %v", status, body)
	}
	got, err := http.Get(h.local(body["signedUrl"].(string)))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(got.Body)
	got.Body.Close()
	entries := readZip(t, data)
	if entries["a.txt"] != string(plain) || entries["nested/file.txt"] != string(nested) || entries["empty.txt"] != "" || len(entries) != 3 {
		t.Fatalf("zip entries = %v", keys(entries))
	}
	status, _, data = h.pipeline(t, token, http.MethodGet, base+"/1/zip", nil, nil)
	if status != http.StatusOK || len(readZip(t, data)) != 3 {
		t.Fatalf("zip by number: %d", status)
	}

	// A second upload under the same name adds files and replaces one, the rest stay
	oldDigest := rows[0].Digest
	h.uploadV3File(t, token, upload, "dist/a.txt", []byte("replaced"), false, 3)
	h.uploadV3File(t, token, upload, "dist/b.txt", []byte("brand new"), true, 4)
	if status, _, raw := h.pipeline(t, token, http.MethodPatch, finalize, nil, []byte(`{"Size":17}`)); status != http.StatusOK {
		t.Fatalf("second finalize: %d %s", status, raw)
	}
	rows, _ = h.m.RunArtifacts(context.Background(), h.run)
	if len(rows) != 1 || rows[0].Digest == oldDigest {
		t.Fatalf("second upload rows = %+v", rows)
	}
	if _, _, err := h.m.artifacts.Blobs().OpenBlob(oldDigest); err == nil {
		t.Fatal("replaced zip survived")
	}
	ui, _ := http.Get(h.local(h.m.ArtifactDownloadURL(context.Background(), rows[0])))
	data, _ = io.ReadAll(ui.Body)
	ui.Body.Close()
	entries = readZip(t, data)
	if entries["a.txt"] != "replaced" || entries["b.txt"] != "brand new" || entries["nested/file.txt"] != string(nested) || len(entries) != 4 {
		t.Fatalf("merged entries = %v", keys(entries))
	}
}

func TestPipelineAPIRefusesIncompleteUploads(t *testing.T) {
	h := newResultsHarness(t)
	token, _ := h.m.runtimeToken(h.job.ID, h.run.ID, 10)
	base := h.srv.URL + PipelineAPIPrefix + "/_apis/pipelines/workflows/" + h.run.ID + "/artifacts"
	hash := fmt.Sprintf("%x", md5.Sum([]byte("partial")))
	upload := base + "/" + hash + "/upload?itemPath=partial%2Ffile.bin"
	headers := map[string]string{"Content-Range": "bytes 0-3/8", "x-tfs-filelength": "8"}
	if status, _, raw := h.pipeline(t, token, http.MethodPut, upload, headers, []byte("abcd")); status != http.StatusOK {
		t.Fatalf("first half: %d %s", status, raw)
	}
	status, body, _ := h.pipeline(t, token, http.MethodPatch, base+"?artifactName=partial", nil, []byte(`{"Size":8}`))
	if status != http.StatusBadRequest || !strings.Contains(fmt.Sprint(body["message"]), "4 of 8 bytes") {
		t.Fatalf("incomplete finalize: %d %v", status, body)
	}
	if rows, _ := h.m.RunArtifacts(context.Background(), h.run); len(rows) != 0 {
		t.Fatalf("pending upload listed: %+v", rows)
	}
	if status, _, _ := h.pipeline(t, token, http.MethodPatch, base+"?artifactName=nothing", nil, []byte(`{"Size":0}`)); status != http.StatusNotFound {
		t.Fatalf("finalize of unknown name answered %d", status)
	}
	if status, _, _ := h.pipeline(t, token, http.MethodGet, base+"/"+hash+"/path?itemPath=partial", nil, nil); status != http.StatusNotFound {
		t.Fatalf("items of pending upload answered %d", status)
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

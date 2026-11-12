package artifacts

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOwnedUploadSessions(t *testing.T) {
	blobs, err := NewBlobStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	id, err := blobs.InitiateOwnedUpload("user:alice")
	if err != nil {
		t.Fatalf("InitiateOwnedUpload: %v", err)
	}
	if err := blobs.VerifyUploadOwner(id, "user:alice"); err != nil {
		t.Fatalf("owner refused: %v", err)
	}
	if err := blobs.VerifyUploadOwner(id, "user:bob", "token:t1"); !errors.Is(err, ErrUploadDenied) {
		t.Fatalf("other principal: got %v, want ErrUploadDenied", err)
	}
	if err := blobs.VerifyUploadOwner(id); !errors.Is(err, ErrUploadDenied) {
		t.Fatalf("no principal: got %v, want ErrUploadDenied", err)
	}
	if err := blobs.VerifyUploadOwner("00000000-0000-4000-8000-000000000000", "user:alice"); !errors.Is(err, ErrUploadNotFound) {
		t.Fatalf("unknown session: got %v, want ErrUploadNotFound", err)
	}

	// Internal sessions carry no owner and never pass the client gate
	internal, err := blobs.InitiateUpload()
	if err != nil {
		t.Fatalf("InitiateUpload: %v", err)
	}
	if err := blobs.VerifyUploadOwner(internal, "user:alice"); !errors.Is(err, ErrUploadNotFound) {
		t.Fatalf("unowned session: got %v, want ErrUploadNotFound", err)
	}

	// Appends need an open session, nothing is created on the fly
	if _, err := blobs.AppendChunk("00000000-0000-4000-8000-000000000001", strings.NewReader("x")); !errors.Is(err, ErrUploadNotFound) {
		t.Fatalf("append to missing session: got %v", err)
	}
	if _, err := blobs.AppendChunk(id, strings.NewReader("hello")); err != nil {
		t.Fatalf("AppendChunk: %v", err)
	}

	// Completion and cancellation drop the sidecar
	if _, _, _, err := blobs.CompleteUpload(id); err != nil {
		t.Fatalf("CompleteUpload: %v", err)
	}
	if _, err := os.Stat(blobs.uploadPath(id) + uploadOwnerSuffix); !os.IsNotExist(err) {
		t.Fatalf("sidecar survived completion: %v", err)
	}
	if _, err := blobs.InitiateOwnedUpload(""); err == nil {
		t.Fatal("empty owner accepted")
	}
	cancelled, _ := blobs.InitiateOwnedUpload("user:alice")
	if err := blobs.CancelUpload(cancelled); err != nil {
		t.Fatalf("CancelUpload: %v", err)
	}
	if _, err := os.Stat(blobs.uploadPath(cancelled) + uploadOwnerSuffix); !os.IsNotExist(err) {
		t.Fatalf("sidecar survived cancel: %v", err)
	}
}

func TestCleanStaleUploadsDropsSidecars(t *testing.T) {
	blobs, err := NewBlobStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	stale, _ := blobs.InitiateOwnedUpload("user:alice")
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(blobs.uploadPath(stale), old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	fresh, _ := blobs.InitiateOwnedUpload("user:alice")
	orphan := filepath.Join(blobs.root, "_uploads", "00000000-0000-4000-8000-000000000002"+uploadOwnerSuffix)
	if err := os.WriteFile(orphan, []byte("user:x"), 0600); err != nil {
		t.Fatalf("orphan: %v", err)
	}
	removed, err := blobs.CleanStaleUploads(24 * time.Hour)
	if err != nil || removed != 1 {
		t.Fatalf("CleanStaleUploads: removed %d err %v", removed, err)
	}
	for _, p := range []string{blobs.uploadPath(stale), blobs.uploadPath(stale) + uploadOwnerSuffix, orphan} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s should be gone: %v", p, err)
		}
	}
	if err := blobs.VerifyUploadOwner(fresh, "user:alice"); err != nil {
		t.Errorf("fresh session lost: %v", err)
	}
}

func TestIngestMatchesCompleteUpload(t *testing.T) {
	blobs, err := NewBlobStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	payload := strings.Repeat("<html>distroface</html>", 4096)
	id, _ := blobs.InitiateUpload()
	if _, err := blobs.AppendChunk(id, strings.NewReader(payload)); err != nil {
		t.Fatalf("AppendChunk: %v", err)
	}
	wantDigest, wantSize, wantMime, err := blobs.CompleteUpload(id)
	if err != nil {
		t.Fatalf("CompleteUpload: %v", err)
	}
	digest, size, mime, err := blobs.Ingest(strings.NewReader(payload), 0)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if digest != wantDigest || size != wantSize || mime != wantMime {
		t.Fatalf("Ingest %s %d %s, CompleteUpload %s %d %s", digest, size, mime, wantDigest, wantSize, wantMime)
	}
	f, info, err := blobs.OpenBlob(digest)
	if err != nil {
		t.Fatalf("OpenBlob: %v", err)
	}
	f.Close()
	if info.Size() != int64(len(payload)) {
		t.Fatalf("stored %d bytes, want %d", info.Size(), len(payload))
	}
	assertNoStaging(t, blobs)
}

func TestIngestEnforcesCap(t *testing.T) {
	blobs, err := NewBlobStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	if _, _, _, err := blobs.Ingest(strings.NewReader("12345"), 4); !errors.Is(err, ErrBlobTooLarge) {
		t.Fatalf("over cap: got %v, want ErrBlobTooLarge", err)
	}
	if _, size, _, err := blobs.Ingest(strings.NewReader("1234"), 4); err != nil || size != 4 {
		t.Fatalf("at cap: size %d err %v", size, err)
	}
	assertNoStaging(t, blobs)
}

func TestBlobWriterAbortLeavesNothing(t *testing.T) {
	blobs, err := NewBlobStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	w, err := blobs.NewWriter()
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if _, err := w.Write([]byte("partial")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	w.Abort()
	assertNoStaging(t, blobs)
}

func assertNoStaging(t *testing.T, blobs *BlobStore) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(blobs.root, "_uploads"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("%d staging files left behind", len(entries))
	}
}

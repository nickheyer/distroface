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

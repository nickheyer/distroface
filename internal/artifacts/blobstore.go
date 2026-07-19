package artifacts

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Blobs live at blobs/sha256/<xx>/<hex> with _uploads staging
type BlobStore struct {
	root string
}

var uploadIDPattern = regexp.MustCompile(`^[a-zA-Z0-9-]{1,64}$`)

func NewBlobStore(root string) (*BlobStore, error) {
	for _, dir := range []string{filepath.Join(root, "_uploads"), filepath.Join(root, "blobs", "sha256")} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("creating artifact storage: %w", err)
		}
	}
	return &BlobStore{root: root}, nil
}

// Creates an empty upload session
func (b *BlobStore) InitiateUpload() (string, error) {
	id := uuid.New().String()
	f, err := os.OpenFile(b.uploadPath(id), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return "", err
	}
	return id, f.Close()
}

// Appends bytes, creates missing session file like v1
func (b *BlobStore) AppendChunk(uploadID string, r io.Reader) (int64, error) {
	if !uploadIDPattern.MatchString(uploadID) {
		return 0, fmt.Errorf("invalid upload id")
	}
	f, err := os.OpenFile(b.uploadPath(uploadID), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return io.Copy(f, r)
}

func (b *BlobStore) UploadSize(uploadID string) (int64, error) {
	if !uploadIDPattern.MatchString(uploadID) {
		return 0, fmt.Errorf("invalid upload id")
	}
	info, err := os.Stat(b.uploadPath(uploadID))
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// Hashes staged upload into blob storage with dedup
func (b *BlobStore) CompleteUpload(uploadID string) (digest string, size int64, mimeType string, err error) {
	if !uploadIDPattern.MatchString(uploadID) {
		return "", 0, "", fmt.Errorf("invalid upload id")
	}
	src := b.uploadPath(uploadID)

	f, err := os.Open(src)
	if err != nil {
		return "", 0, "", fmt.Errorf("upload session not found: %w", err)
	}

	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	mimeType = http.DetectContentType(head[:n])

	if _, err = f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return "", 0, "", err
	}
	hasher := sha256.New()
	size, err = io.Copy(hasher, f)
	f.Close()
	if err != nil {
		return "", 0, "", err
	}
	hexDigest := hex.EncodeToString(hasher.Sum(nil))
	digest = "sha256:" + hexDigest

	dest := b.blobPathHex(hexDigest)
	if _, statErr := os.Stat(dest); statErr == nil {
		// Identical blob already stored
		return digest, size, mimeType, os.Remove(src)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return "", 0, "", err
	}
	if err := os.Rename(src, dest); err != nil {
		return "", 0, "", err
	}
	return digest, size, mimeType, nil
}

func (b *BlobStore) CancelUpload(uploadID string) error {
	if !uploadIDPattern.MatchString(uploadID) {
		return fmt.Errorf("invalid upload id")
	}
	err := os.Remove(b.uploadPath(uploadID))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (b *BlobStore) OpenBlob(digest string) (*os.File, os.FileInfo, error) {
	path, err := b.blobPath(digest)
	if err != nil {
		return nil, nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, info, nil
}

// Missing blob delete is a no-op
func (b *BlobStore) DeleteBlob(digest string) error {
	path, err := b.blobPath(digest)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Removes upload sessions older than maxAge
func (b *BlobStore) CleanStaleUploads(maxAge time.Duration) (int, error) {
	entries, err := os.ReadDir(filepath.Join(b.root, "_uploads"))
	if err != nil {
		return 0, err
	}
	cutoff := time.Now().Add(-maxAge)
	removed := 0
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			if os.Remove(filepath.Join(b.root, "_uploads", e.Name())) == nil {
				removed++
			}
		}
	}
	return removed, nil
}

func (b *BlobStore) uploadPath(id string) string {
	return filepath.Join(b.root, "_uploads", id)
}

var hexPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (b *BlobStore) blobPath(digest string) (string, error) {
	hexDigest, ok := strings.CutPrefix(digest, "sha256:")
	if !ok || !hexPattern.MatchString(hexDigest) {
		return "", fmt.Errorf("invalid digest %q", digest)
	}
	return b.blobPathHex(hexDigest), nil
}

func (b *BlobStore) blobPathHex(hexDigest string) string {
	return filepath.Join(b.root, "blobs", "sha256", hexDigest[:2], hexDigest)
}

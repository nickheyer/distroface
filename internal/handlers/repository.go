package handlers

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/nickheyer/distroface/internal/config"
	"github.com/nickheyer/distroface/internal/constants"
	"github.com/nickheyer/distroface/internal/models"
	"github.com/nickheyer/distroface/internal/repository"
	"github.com/opencontainers/go-digest"
)

type RepositoryHandler struct {
	repo   repository.Repository
	config *config.Config
	logger *log.Logger
}

func NewRepositoryHandler(repo repository.Repository, cfg *config.Config) *RepositoryHandler {
	return &RepositoryHandler{
		repo:   repo,
		config: cfg,
		logger: log.New(os.Stdout, "REGISTRY: ", log.LstdFlags),
	}
}

func (h *RepositoryHandler) ListRepositories(w http.ResponseWriter, r *http.Request) {
	username := r.Context().Value(constants.UsernameKey).(string)
	h.logger.Printf("Listing repositories for user: %s", username)

	metadata, err := h.repo.ListImageMetadata(username)
	if err != nil {
		h.logger.Printf("Failed to list repositories: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	if metadata == nil {
		metadata = []*models.ImageMetadata{}
	}

	// BUILD RESPONSE
	type TagInfo struct {
		Name    string    `json:"name"`
		Size    int64     `json:"size"`
		Digest  string    `json:"digest"`
		Created time.Time `json:"created"`
	}

	type RepositoryResponse struct {
		ID        string    `json:"id"`
		Name      string    `json:"name"`
		Tags      []TagInfo `json:"tags"`
		UpdatedAt time.Time `json:"updated_at"`
		Owner     string    `json:"owner"`
		TotalSize int64     `json:"size"`
		Private   bool      `json:"private"`
	}

	// GROUPING BY REPO NAME
	repoMap := make(map[string]*RepositoryResponse)
	for _, img := range metadata {
		repo, exists := repoMap[img.Name]
		if !exists {
			repo = &RepositoryResponse{
				ID:        img.ID,
				Name:      img.Name,
				Tags:      make([]TagInfo, 0),
				UpdatedAt: img.UpdatedAt,
				Owner:     img.Owner,
				TotalSize: img.Size,
				Private:   img.Private,
			}
			repoMap[img.Name] = repo
		}

		// ADD TAG INFO
		for _, tagName := range img.Tags {
			tag := TagInfo{
				Name:    tagName,
				Size:    img.Size,
				Digest:  img.ID, // USE MANIFEST AS ID
				Created: img.CreatedAt,
			}
			repo.Tags = append(repo.Tags, tag)
		}

		// UPDATE IF NEWER
		if img.UpdatedAt.After(repo.UpdatedAt) {
			repo.UpdatedAt = img.UpdatedAt
		}
	}

	// MAP TO SLICE
	repositories := make([]*RepositoryResponse, 0, len(repoMap))
	for _, repo := range repoMap {
		repositories = append(repositories, repo)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(repositories); err != nil {
		h.logger.Printf("Failed to encode response: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}
}

// TAGS OPERATIONS
func (h *RepositoryHandler) DeleteTag(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]
	tag := vars["tag"]
	username := r.Context().Value(constants.UsernameKey).(string)

	h.logger.Printf("Deleting tag %s from repository %s by user %s", tag, name, username)

	// GET TAG'S MANIFEST DIGEST
	tagLinkPath := filepath.Join(
		h.config.Storage.RootDirectory,
		"repositories",
		name,
		"_manifests",
		"tags",
		tag,
		"current",
		"link",
	)

	digest, err := os.ReadFile(tagLinkPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "TAG NOT FOUND", http.StatusNotFound)
			return
		}
		h.logger.Printf("Failed to read tag link: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	manifestDigest := strings.TrimSpace(string(digest))

	// REMOVE TAG DIRECTORY
	tagDir := filepath.Dir(filepath.Dir(tagLinkPath))
	if err := os.RemoveAll(tagDir); err != nil {
		h.logger.Printf("Failed to remove tag directory: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	// CHECK FOR REMAINING TAGS
	if hasRemainingTags, err := h.checkRemainingTags(name, manifestDigest); err != nil {
		h.logger.Printf("Error checking remaining tags: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	} else if !hasRemainingTags {
		// PERFORM FULL CLEANUP FOR LAST TAG
		if err := h.performFullCleanup(name, manifestDigest); err != nil {
			h.logger.Printf("Error during full cleanup: %v", err)
			http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
			return
		}
	} else {
		// UPDATE METADATA TO REMOVE JUST THIS TAG
		if err := h.updateImageMetadata(manifestDigest, tag); err != nil {
			h.logger.Printf("Error updating image metadata: %v", err)
			http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusAccepted)
}

func (h *RepositoryHandler) ListTags(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]
	username := r.Context().Value(constants.UsernameKey).(string)

	h.logger.Printf("Listing tags for repository %s by user %s", name, username)

	// GET ALL METADATA FOR REPO
	metadata, err := h.repo.ListImageMetadata(username)
	if err != nil {
		h.logger.Printf("Failed to get repository metadata: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	// FILTER METADATA AND GET TAGS
	var tags []string
	tagMap := make(map[string]bool) // GET ONLY UNIQUE TAGS, DEDUP

	for _, img := range metadata {
		if img.Name == name {
			for _, tag := range img.Tags {
				tagMap[tag] = true
			}
		}
	}

	// TAG MAP TO SLICE
	for tag := range tagMap {
		tags = append(tags, tag)
	}

	response := struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}{
		Name: name,
		Tags: tags,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.logger.Printf("Failed to encode tags response: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}
}

// MANIFEST OPERATIONS
func (h *RepositoryHandler) HandleManifest(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]
	reference := vars["reference"]
	username := r.Context().Value(constants.UsernameKey).(string)

	h.logger.Printf("Handling manifest request: method=%s repo=%s ref=%s user=%s",
		r.Method, name, reference, username)

	switch r.Method {
	case "HEAD", "GET":
		h.getManifest(w, r, name, reference)
	case "PUT":
		h.putManifest(w, r, name, reference, username)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *RepositoryHandler) getManifest(w http.ResponseWriter, r *http.Request, name, reference string) {
	// RESOLVE MANIFEST PATH
	manifestPath := h.resolveManifestPath(name, reference)
	if manifestPath == "" {
		h.logger.Printf("Failed to resolve manifest path for %s:%s", name, reference)
		http.Error(w, "MANIFEST NOT FOUND", http.StatusNotFound)
		return
	}

	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		h.logger.Printf("Failed to read manifest at %s: %v", manifestPath, err)
		http.Error(w, "MANIFEST NOT FOUND", http.StatusNotFound)
		return
	}

	// PARSE MANIFEST
	var manifestObj struct {
		MediaType string `json:"mediaType"`
	}
	if err := json.Unmarshal(manifest, &manifestObj); err != nil {
		h.logger.Printf("Failed to parse manifest JSON: %v", err)
		http.Error(w, "INVALID MANIFEST", http.StatusBadRequest)
		return
	}

	// DOCKER HEADERS
	w.Header().Set("Content-Type", manifestObj.MediaType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(manifest)))
	manifestDigest := digest.FromBytes(manifest)
	w.Header().Set("Docker-Content-Digest", manifestDigest.String())

	if r.Method == "HEAD" {
		return
	}

	w.Write(manifest)
}

func (h *RepositoryHandler) putManifest(w http.ResponseWriter, r *http.Request, name, reference, username string) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.logger.Printf("Failed to read manifest body: %v", err)
		http.Error(w, "INVALID REQUEST", http.StatusBadRequest)
		return
	}

	// GET DIGEST
	manifestDigest := digest.FromBytes(body)

	// GET LAYER INFO
	var manifestObj struct {
		SchemaVersion int    `json:"schemaVersion"`
		MediaType     string `json:"mediaType"`
		Config        struct {
			MediaType string `json:"mediaType"`
			Size      int64  `json:"size"`
			Digest    string `json:"digest"`
		} `json:"config"`
		Layers []struct {
			MediaType string `json:"mediaType"`
			Size      int64  `json:"size"`
			Digest    string `json:"digest"`
		} `json:"layers"`
	}

	if err := json.Unmarshal(body, &manifestObj); err != nil {
		h.logger.Printf("Failed to parse manifest JSON: %v", err)
		http.Error(w, "INVALID MANIFEST", http.StatusBadRequest)
		return
	}

	// GET TOTAL SIZE
	var totalSize int64
	totalSize += manifestObj.Config.Size
	for _, layer := range manifestObj.Layers {
		totalSize += layer.Size
	}

	// STORE BY DIGEST
	manifestDir := filepath.Join(
		h.config.Storage.RootDirectory,
		"repositories",
		name,
		"_manifests",
		"revisions",
	)
	if err := os.MkdirAll(manifestDir, 0755); err != nil {
		h.logger.Printf("Failed to create manifest directory: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	manifestPath := filepath.Join(manifestDir, manifestDigest.String())
	if err := os.WriteFile(manifestPath, body, 0644); err != nil {
		h.logger.Printf("Failed to write manifest: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	// UPSERT METADATA
	metadata, err := h.repo.GetImageMetadata(manifestDigest.String())
	if err == nil {
		// UPDATE EXISTING
		hasTag := false
		for _, t := range metadata.Tags {
			if t == reference {
				hasTag = true
				break
			}
		}
		if !hasTag {
			metadata.Tags = append(metadata.Tags, reference)
		}
		metadata.Size = totalSize
		metadata.UpdatedAt = time.Now()
		if err := h.repo.UpdateImageMetadata(metadata); err != nil {
			h.logger.Printf("Failed to update image metadata: %v", err)
		}
	} else {
		// CREATE NEW METADATA
		metadata = &models.ImageMetadata{
			ID:        manifestDigest.String(),
			Name:      name,
			Tags:      []string{reference},
			Size:      totalSize,
			Owner:     username,
			Labels:    make(map[string]string),
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		if err := h.repo.CreateImageMetadata(metadata); err != nil {
			h.logger.Printf("Failed to create image metadata: %v", err)
		}
	}

	// UPDATE LINK IF TAG
	if !strings.HasPrefix(reference, "sha256:") {
		tagDir := filepath.Join(
			h.config.Storage.RootDirectory,
			"repositories",
			name,
			"_manifests",
			"tags",
			reference,
			"current",
		)
		if err := os.MkdirAll(tagDir, 0755); err != nil {
			h.logger.Printf("Failed to create tag directory: %v", err)
			http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
			return
		}

		linkPath := filepath.Join(tagDir, "link")
		if err := os.WriteFile(linkPath, []byte(manifestDigest.String()), 0644); err != nil {
			h.logger.Printf("Failed to write tag link: %v", err)
			http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Docker-Content-Digest", manifestDigest.String())
	w.Header().Set("Location", fmt.Sprintf("/v2/%s/manifests/%s", name, manifestDigest))
	w.WriteHeader(http.StatusCreated)
}

func (h *RepositoryHandler) DeleteManifest(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]
	reference := vars["reference"]
	username := r.Context().Value(constants.UsernameKey).(string)

	h.logger.Printf("Deleting manifest %s from repository %s by user %s", reference, name, username)

	// RESOLVE MANIFEST PATH
	manifestPath := h.resolveManifestPath(name, reference)
	if manifestPath == "" {
		http.Error(w, "MANIFEST NOT FOUND", http.StatusNotFound)
		return
	}

	// RM MANIFEST FILE
	if err := os.Remove(manifestPath); err != nil {
		h.logger.Printf("Failed to delete manifest file: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	// RM TAG LINK IF TAG
	if !strings.HasPrefix(reference, "sha256:") {
		tagLinkPath := filepath.Join(
			h.config.Storage.RootDirectory,
			"repositories",
			name,
			"_manifests",
			"tags",
			reference,
			"current",
			"link",
		)
		if err := os.RemoveAll(filepath.Dir(tagLinkPath)); err != nil {
			h.logger.Printf("Failed to remove tag directory: %v", err)
			// DONT FAIL ON FAILURE TO CLEANUP TAG
		}
	}

	// UPDATE METADATA
	metadata, err := h.repo.GetImageMetadata(reference)
	if err == nil {
		newTags := make([]string, 0)
		for _, tag := range metadata.Tags {
			if tag != reference {
				newTags = append(newTags, tag)
			}
		}

		if len(newTags) == 0 {
			// IF NO TAGS, DELETE METADATA
			if err := h.repo.DeleteImageMetadata(metadata.ID); err != nil {
				h.logger.Printf("Failed to delete image metadata: %v", err)
			}
		} else {
			// UPDATE REMAINING TAGS
			metadata.Tags = newTags
			if err := h.repo.UpdateImageMetadata(metadata); err != nil {
				h.logger.Printf("Failed to update image metadata: %v", err)
			}
		}
	}

	w.WriteHeader(http.StatusAccepted)
}

// BLOB OPERATIONS
func (h *RepositoryHandler) InitiateBlobUpload(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]
	uploadID := uuid.New().String()

	// CREATE UPLOAD DIR
	uploadDir := filepath.Join(h.config.Storage.RootDirectory, "_uploads")
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		h.logger.Printf("Failed to create upload directory: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	// CREATE EMPTY FILE
	uploadPath := filepath.Join(uploadDir, uploadID)
	if _, err := os.Create(uploadPath); err != nil {
		h.logger.Printf("Failed to create upload file: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Location", fmt.Sprintf("/v2/%s/blobs/uploads/%s", name, uploadID))
	w.Header().Set("Docker-Upload-UUID", uploadID)
	w.Header().Set("Range", "0-0")
	w.WriteHeader(http.StatusAccepted)
}

func (h *RepositoryHandler) HandleBlobUpload(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]
	uploadID := vars["uuid"]

	uploadPath := filepath.Join(h.config.Storage.RootDirectory, "_uploads", uploadID)

	// OPEN FILE IN APPEND MODE
	file, err := os.OpenFile(uploadPath, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		h.logger.Printf("Failed to open upload file: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	// GET CURRENT SIZE
	info, err := file.Stat()
	if err != nil {
		h.logger.Printf("Failed to stat file: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}
	startSize := info.Size()

	// COPY DATA IN CHUNKS
	written, err := io.Copy(file, r.Body)
	if err != nil {
		h.logger.Printf("Failed to write data: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	// SET HEADERS
	w.Header().Set("Location", fmt.Sprintf("/v2/%s/blobs/uploads/%s", name, uploadID))
	w.Header().Set("Docker-Upload-UUID", uploadID)
	w.Header().Set("Range", fmt.Sprintf("0-%d", startSize+written-1))
	w.WriteHeader(http.StatusAccepted)
}

func (h *RepositoryHandler) CompleteBlobUpload(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]
	uploadID := vars["uuid"]
	expectedDigest := r.URL.Query().Get("digest")

	if expectedDigest == "" {
		h.logger.Printf("Missing digest parameter")
		http.Error(w, "MISSING DIGEST", http.StatusBadRequest)
		return
	}

	uploadPath := filepath.Join(h.config.Storage.RootDirectory, "_uploads", uploadID)

	// CREATE OR OPEN FILE
	file, err := os.OpenFile(uploadPath, os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		h.logger.Printf("Failed to open upload file: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	// WRITE REQUEST BODY IF PRESENT
	hash := sha256.New()
	if r.ContentLength > 0 {
		// TEEREADER
		reader := io.TeeReader(r.Body, hash)
		if _, err := io.Copy(file, reader); err != nil {
			h.logger.Printf("Failed to write upload data: %v", err)
			http.Error(w, "FAILED TO WRITE DATA", http.StatusInternalServerError)
			return
		}
	} else {
		// READ EXISTING FILE HASH IF NO BODY
		if err := file.Close(); err != nil {
			h.logger.Printf("Failed to close file for reading: %v", err)
			http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
			return
		}

		file, err = os.Open(uploadPath)
		if err != nil {
			h.logger.Printf("Failed to open file for reading: %v", err)
			http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
			return
		}
		defer file.Close()

		if _, err := io.Copy(hash, file); err != nil {
			h.logger.Printf("Failed to read file for hash: %v", err)
			http.Error(w, "FAILED TO READ DATA", http.StatusInternalServerError)
			return
		}
	}

	actualDigest := fmt.Sprintf("sha256:%x", hash.Sum(nil))

	if actualDigest != expectedDigest {
		h.logger.Printf("Digest mismatch: expected=%s actual=%s", expectedDigest, actualDigest)
		http.Error(w, "DIGEST MISMATCH", http.StatusBadRequest)
		return
	}

	// MOVE TO FINAL LOCATION
	blobDir := filepath.Join(
		h.config.Storage.RootDirectory,
		"blobs",
		"sha256",
	)
	if err := os.MkdirAll(blobDir, 0755); err != nil {
		h.logger.Printf("Failed to create blob directory: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	blobPath := filepath.Join(blobDir, strings.TrimPrefix(expectedDigest, "sha256:"))

	// TRY RENAME, FALLBACK TO COPY
	if err := os.Rename(uploadPath, blobPath); err != nil {
		if err := copyFile(uploadPath, blobPath); err != nil {
			h.logger.Printf("Failed to copy blob: %v", err)
			http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
			return
		}
		os.Remove(uploadPath)
	}

	// CREATE REPO LINK
	layerLinkDir := filepath.Join(
		h.config.Storage.RootDirectory,
		"repositories",
		name,
		"_layers",
		"sha256",
		strings.TrimPrefix(expectedDigest, "sha256:"),
	)
	if err := os.MkdirAll(layerLinkDir, 0755); err != nil {
		h.logger.Printf("Failed to create layer link directory: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	linkPath := filepath.Join(layerLinkDir, "link")
	if err := os.WriteFile(linkPath, []byte(expectedDigest), 0644); err != nil {
		h.logger.Printf("Failed to write layer link: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Docker-Content-Digest", expectedDigest)
	w.Header().Set("Location", fmt.Sprintf("/v2/%s/blobs/%s", name, expectedDigest))
	w.WriteHeader(http.StatusCreated)
}

func (h *RepositoryHandler) GetBlob(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]
	digest := vars["digest"]

	// VERIFY BLOB EXISTS AND IS LINKED TO REPOSITORY
	layerLink := filepath.Join(
		h.config.Storage.RootDirectory,
		"repositories",
		name,
		"_layers",
		"sha256",
		strings.TrimPrefix(digest, "sha256:"),
		"link",
	)

	if _, err := os.Stat(layerLink); err != nil {
		h.logger.Printf("Blob not found in repository: %s", digest)
		http.Error(w, "BLOB NOT FOUND", http.StatusNotFound)
		return
	}

	blobPath := filepath.Join(
		h.config.Storage.RootDirectory,
		"blobs",
		"sha256",
		strings.TrimPrefix(digest, "sha256:"),
	)

	blob, err := os.Open(blobPath)
	if err != nil {
		h.logger.Printf("Failed to open blob: %v", err)
		http.Error(w, "BLOB NOT FOUND", http.StatusNotFound)
		return
	}
	defer blob.Close()

	info, err := blob.Stat()
	if err != nil {
		h.logger.Printf("Failed to get blob info: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	// SET RESPONSE HEADERS
	w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Docker-Content-Digest", digest)
	w.Header().Set("Accept-Ranges", "bytes")

	// HANDLE RANGE REQUESTS
	rangeHeader := r.Header.Get("Range")
	if rangeHeader != "" {
		// PARSE RANGE HEADER
		// EX: bytes=0-1000
		var start, end int64
		fmt.Sscanf(rangeHeader, "bytes=%d-%d", &start, &end)
		if end == 0 {
			end = info.Size() - 1
		}
		if start > end || start < 0 || end >= info.Size() {
			http.Error(w, "Invalid Range", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, info.Size()))
		w.Header().Set("Content-Length", fmt.Sprintf("%d", end-start+1))
		w.WriteHeader(http.StatusPartialContent)
		blob.Seek(start, io.SeekStart)
	}

	// USE BUFFERED COPY WITH LARGER BUFFER
	buf := make([]byte, 32*1024) // 32KB buffer
	_, err = io.CopyBuffer(w, blob, buf)
	if err != nil {
		// CHECK FOR COMMON NETWORK INTERRUPTION ERRORS
		if errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) ||
			strings.Contains(err.Error(), "broken pipe") {
			// LOG DEBUG
			h.logger.Printf("Client disconnected during blob transfer: %v", err)
			return
		}
		// LOG ERR
		h.logger.Printf("Unexpected error streaming blob: %v", err)
	}
}

func (h *RepositoryHandler) DeleteBlob(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]
	digest := vars["digest"]
	username := r.Context().Value(constants.UsernameKey).(string)

	h.logger.Printf("Deleting blob %s from repository %s by user %s", digest, name, username)

	// CHECK IF BLOB IS REFERENCED
	layerLink := filepath.Join(
		h.config.Storage.RootDirectory,
		"repositories",
		name,
		"_layers",
		"sha256",
		strings.TrimPrefix(digest, "sha256:"),
		"link",
	)

	if _, err := os.Stat(layerLink); err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "BLOB NOT FOUND", http.StatusNotFound)
			return
		}
		h.logger.Printf("Failed to check layer link: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	// RM LAYER LINK IF IT EXISTS
	if err := os.RemoveAll(filepath.Dir(layerLink)); err != nil {
		h.logger.Printf("Failed to remove layer link: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	// CHECK IF STILL REFERENCED BEFORE DELETING
	isReferenced := false
	repoPath := filepath.Join(h.config.Storage.RootDirectory, "repositories")
	err := filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if strings.HasSuffix(path, "link") && filepath.Base(filepath.Dir(path)) == strings.TrimPrefix(digest, "sha256:") {
			isReferenced = true
			return filepath.SkipAll
		}
		return nil
	})

	if err != nil {
		h.logger.Printf("Failed to check blob references: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	// IF NO REF, DELETE
	if !isReferenced {
		blobPath := filepath.Join(
			h.config.Storage.RootDirectory,
			"blobs",
			"sha256",
			strings.TrimPrefix(digest, "sha256:"),
		)
		if err := os.Remove(blobPath); err != nil && !os.IsNotExist(err) {
			h.logger.Printf("Failed to delete blob file: %v", err)
			http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusAccepted)
}

// HELPER FUNCTIONS
func (h *RepositoryHandler) resolveManifestPath(name, reference string) string {
	var manifestPath string

	if strings.HasPrefix(reference, "sha256:") {
		// DIRECT DIGEST REF
		manifestPath = filepath.Join(
			h.config.Storage.RootDirectory,
			"repositories",
			name,
			"_manifests",
			"revisions",
			reference,
		)
	} else {
		// TAG REF - NEED TO RESOLVE TO DIGEST
		tagLinkPath := filepath.Join(
			h.config.Storage.RootDirectory,
			"repositories",
			name,
			"_manifests",
			"tags",
			reference,
			"current",
			"link",
		)

		digest, err := os.ReadFile(tagLinkPath)
		if err != nil {
			h.logger.Printf("Failed to read tag link at %s: %v", tagLinkPath, err)
			return ""
		}

		manifestPath = filepath.Join(
			h.config.Storage.RootDirectory,
			"repositories",
			name,
			"_manifests",
			"revisions",
			string(digest),
		)
	}

	// DOES MANIFEST EXIST
	if _, err := os.Stat(manifestPath); err != nil {
		h.logger.Printf("Manifest not found at %s: %v", manifestPath, err)
		return ""
	}

	return manifestPath
}

func copyFile(src, dst string) error {
	source, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open source file: %v", err)
	}
	defer source.Close()

	destination, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %v", err)
	}
	defer destination.Close()

	_, err = io.Copy(destination, source)
	return err
}

func (h *RepositoryHandler) checkRemainingTags(name, manifestDigest string) (bool, error) {
	manifestTagsDir := filepath.Join(
		h.config.Storage.RootDirectory,
		"repositories",
		name,
		"_manifests",
		"tags",
	)

	hasRemainingTags := false
	err := filepath.Walk(manifestTagsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if strings.HasSuffix(path, "link") {
			linkDigest, err := os.ReadFile(path)
			if err == nil && strings.TrimSpace(string(linkDigest)) == manifestDigest {
				hasRemainingTags = true
				return filepath.SkipAll
			}
		}
		return nil
	})

	return hasRemainingTags, err
}

func (h *RepositoryHandler) updateImageMetadata(manifestDigest, removedTag string) error {
	metadata, err := h.repo.GetImageMetadata(manifestDigest)
	if err != nil {
		return fmt.Errorf("failed to get image metadata: %v", err)
	}

	newTags := make([]string, 0, len(metadata.Tags)-1)
	for _, t := range metadata.Tags {
		if t != removedTag {
			newTags = append(newTags, t)
		}
	}
	metadata.Tags = newTags

	return h.repo.UpdateImageMetadata(metadata)
}

func (h *RepositoryHandler) performFullCleanup(name, manifestDigest string) error {
	// 1. READ AND PARSE MANIFEST
	manifestPath := filepath.Join(
		h.config.Storage.RootDirectory,
		"repositories",
		name,
		"_manifests",
		"revisions",
		manifestDigest,
	)

	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("failed to read manifest: %v", err)
	}

	var manifest struct {
		Layers []struct {
			Digest string `json:"digest"`
		} `json:"layers"`
		Config struct {
			Digest string `json:"digest"`
		} `json:"config"`
	}

	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return fmt.Errorf("failed to parse manifest: %v", err)
	}

	// 2. COLLECT ALL DIGESTS
	var digests []string
	for _, layer := range manifest.Layers {
		digests = append(digests, layer.Digest)
	}
	digests = append(digests, manifest.Config.Digest)

	// 3. CLEANUP EACH DIGEST
	for _, digest := range digests {
		// RM REPO LAYER LINK
		layerLinkDir := filepath.Join(
			h.config.Storage.RootDirectory,
			"repositories",
			name,
			"_layers",
			"sha256",
			strings.TrimPrefix(digest, "sha256:"),
		)
		if err := os.RemoveAll(layerLinkDir); err != nil {
			h.logger.Printf("Warning: failed to remove layer link directory: %v", err)
		}

		// RM BLOB IF NOT REFERENCED ELSEWHERE
		if !h.isDigestReferenced(digest, name) {
			blobPath := filepath.Join(
				h.config.Storage.RootDirectory,
				"blobs",
				"sha256",
				strings.TrimPrefix(digest, "sha256:"),
			)
			if err := os.Remove(blobPath); err != nil && !os.IsNotExist(err) {
				h.logger.Printf("Warning: failed to remove blob: %v", err)
			}
		}
	}

	// 4. REMOVE MANIFEST FILE
	if err := os.Remove(manifestPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove manifest file: %v", err)
	}

	// 5. CLEANUP EMPTY DIRECTORIES
	dirsToClean := []string{
		filepath.Join(h.config.Storage.RootDirectory, "repositories", name, "_layers", "sha256"),
		filepath.Join(h.config.Storage.RootDirectory, "repositories", name, "_layers"),
		filepath.Join(h.config.Storage.RootDirectory, "repositories", name, "_manifests", "revisions"),
		filepath.Join(h.config.Storage.RootDirectory, "repositories", name, "_manifests", "tags"),
		filepath.Join(h.config.Storage.RootDirectory, "repositories", name, "_manifests"),
		filepath.Join(h.config.Storage.RootDirectory, "repositories", name),
	}

	for _, dir := range dirsToClean {
		if isEmpty, _ := h.isDirEmpty(dir); isEmpty {
			os.Remove(dir)
		}
	}

	// 6. REMOVE METADATA
	return h.repo.DeleteImageMetadata(manifestDigest)
}

func (h *RepositoryHandler) isDigestReferenced(digest, excludeRepo string) bool {
	reposPath := filepath.Join(h.config.Storage.RootDirectory, "repositories")
	isReferenced := false

	filepath.Walk(reposPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || strings.Contains(path, excludeRepo) {
			return filepath.SkipDir
		}

		if strings.HasSuffix(path, "link") {
			linkData, err := os.ReadFile(path)
			if err == nil && strings.TrimSpace(string(linkData)) == digest {
				isReferenced = true
				return filepath.SkipAll
			}
		}
		return nil
	})

	return isReferenced
}

func (h *RepositoryHandler) isDirEmpty(dir string) (bool, error) {
	f, err := os.Open(dir)
	if err != nil {
		return false, err
	}
	defer f.Close()

	_, err = f.Readdirnames(1)
	return err == io.EOF, nil
}

func (h *RepositoryHandler) UpdateImageVisibility(w http.ResponseWriter, r *http.Request) {
	username := r.Context().Value(constants.UsernameKey).(string)

	var req struct {
		ID      string `json:"id"`
		Private bool   `json:"private"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	// CHECK OWNERSHIP USING THE DIGEST/SHA ID
	metadata, err := h.repo.GetImageMetadata(req.ID)
	if err != nil {
		h.logger.Printf("Failed to get image metadata: %v", err)
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	if metadata.Owner != username {
		h.logger.Printf("User %s not authorized for image %s", username, req.ID)
		http.Error(w, "Not authorized", http.StatusForbidden)
		return
	}

	if err := h.repo.UpdateImageVisibility(req.ID, req.Private); err != nil {
		h.logger.Printf("Failed to update visibility: %v", err)
		http.Error(w, "Failed to update visibility", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *RepositoryHandler) ListGlobalRepositories(w http.ResponseWriter, r *http.Request) {
	username := r.Context().Value(constants.UsernameKey).(string)

	// GET ALL PUBLIC IMAGES AND USER'S PRIVATE IMAGES
	metadata, err := h.repo.ListPublicImageMetadata()
	if err != nil {
		http.Error(w, "Failed to list repositories", http.StatusInternalServerError)
		return
	}

	// GET USER'S PRIVATE IMAGES
	userMeta, err := h.repo.ListImageMetadata(username)
	if err != nil {
		http.Error(w, "Failed to list user repositories", http.StatusInternalServerError)
		return
	}

	// COMBINE AND DEDUPLICATE
	seen := make(map[string]bool)
	var combined []*models.ImageMetadata

	// ADD PUBLIC IMAGES
	for _, img := range metadata {
		if !seen[img.ID] {
			seen[img.ID] = true
			combined = append(combined, img)
		}
	}

	// ADD USER'S PRIVATE IMAGES
	for _, img := range userMeta {
		if !seen[img.ID] && img.Private {
			seen[img.ID] = true
			combined = append(combined, img)
		}
	}

	// CALCULATE TOTALS
	var totalSize int64
	for _, img := range combined {
		totalSize += img.Size
	}

	response := models.GlobalView{
		TotalImages: int64(len(combined)),
		TotalSize:   totalSize,
		Images:      combined,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

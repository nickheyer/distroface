package handlers

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/constants"
	"github.com/nickheyer/distroface/internal/models"
	"github.com/opencontainers/go-digest"
)

// TASK STATUS TRACKING
type MigrationTask struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"` // PENDING, RUNNING, COMPLETE, FAILED
	Progress  float64   `json:"progress"`
	Error     string    `json:"error,omitempty"`
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time,omitempty"`
	Stats     struct {
		TotalLayers   int64 `json:"total_layers"`
		LayersSkipped int64 `json:"layers_skipped"`
		BytesSkipped  int64 `json:"bytes_skipped"`
	} `json:"stats"`
}

type MigrationRequest struct {
	SourceRegistry string   `json:"source_registry"`
	Images         []string `json:"images"`
	Username       string   `json:"username,omitempty"`
	Password       string   `json:"password,omitempty"`
}

// EXTEND REPOSITORY HANDLER
func (h *RepositoryHandler) MigrateImages(w http.ResponseWriter, r *http.Request) {
	h.log.Printf("Received migration request")

	// GET USERNAME FROM REQUEST CONTEXT
	username := r.Context().Value(constants.UsernameKey).(string)
	h.log.Printf("Migration requested by user: %s", username)

	var req MigrationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.log.Printf("Failed to decode migration request: %v", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// VALIDATE REQUEST
	if req.SourceRegistry == "" || len(req.Images) == 0 {
		h.log.Printf("Invalid request: source_registry=%s, image_count=%d",
			req.SourceRegistry, len(req.Images))
		http.Error(w, "Source registry and images are required", http.StatusBadRequest)
		return
	}

	h.log.Printf("Creating migration task for %d images from %s",
		len(req.Images), req.SourceRegistry)

	// CREATE TASK
	taskID := uuid.New().String()
	task := &MigrationTask{
		ID:        taskID,
		Status:    "pending",
		StartTime: time.Now(),
	}

	// CREATE NEW BACKGROUND CONTEXT
	ctx := context.Background()
	ctx = context.WithValue(ctx, constants.UsernameKey, username)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)

	// START MIGRATION IN BACKGROUND
	go func() {
		defer cancel() // Cleanup the context when done
		defer func() {
			if r := recover(); r != nil {
				h.log.Printf("Panic recovered in migration process: %v", r)
				task.Status = "failed"
				task.Error = fmt.Sprintf("Internal error: %v", r)
				h.updateMigrationTask(task)
			}
		}()

		h.processMigration(ctx, task, &req)
	}()

	h.log.Printf("Migration task %s started", taskID)

	// RETURN TASK ID
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"task_id": taskID,
	})
}

func (h *RepositoryHandler) GetMigrationStatus(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("task_id")
	if taskID == "" {
		http.Error(w, "Task ID is required", http.StatusBadRequest)
		return
	}

	// FETCH TASK STATUS FROM CACHE/DB
	task := h.getMigrationTask(taskID)
	if task == nil {
		http.Error(w, "Task not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(task)
}

func (h *RepositoryHandler) processMigration(ctx context.Context, task *MigrationTask, req *MigrationRequest) {
	h.log.Printf("Starting migration process for task %s with %d images", task.ID, len(req.Images))
	h.log.Printf("Source registry: %s", req.SourceRegistry)

	task.Status = "running"
	h.updateMigrationTask(task)

	totalImages := len(req.Images)
	completedImages := 0

	// LOG CONTEXT STATE
	deadline, hasDeadline := ctx.Deadline()
	h.log.Printf("Context state - Done: %v, Err: %v, Deadline: %v, HasDeadline: %v",
		ctx.Done() != nil, ctx.Err(), deadline, hasDeadline)

	if ctx.Err() != nil {
		h.log.Printf("Context already has error before processing: %v", ctx.Err())
		task.Status = "failed"
		task.Error = fmt.Sprintf("Context error before processing: %v", ctx.Err())
		h.updateMigrationTask(task)
		return
	}

	for i, img := range req.Images {
		h.log.Printf("Processing image %d/%d: %s", i+1, totalImages, img)

		select {
		case <-ctx.Done():
			h.log.Printf("Context cancelled during migration of %s. Error: %v", img, ctx.Err())
			task.Status = "failed"
			task.Error = fmt.Sprintf("Migration cancelled during %s: %v", img, ctx.Err())
			h.updateMigrationTask(task)
			return
		default:
			h.log.Printf("Starting migration of image: %s", img)

			// CREATE NEW CONTEXT WITH TIMEOUT FOR THIS IMAGE
			imageCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
			err := h.migrateImage(imageCtx, req.SourceRegistry, img, req.Username, req.Password)
			cancel() // ALWAYS CANCEL CLEANUP

			if err != nil {
				h.log.Printf("Failed to migrate image %s: %v", img, err)
				task.Status = "failed"
				task.Error = fmt.Sprintf("Failed to migrate %s: %v", img, err)
				h.updateMigrationTask(task)
				return
			}

			completedImages++
			progress := float64(completedImages) / float64(totalImages) * 100
			h.log.Printf("Successfully migrated image %s. Progress: %.2f%%", img, progress)

			task.Progress = progress
			h.updateMigrationTask(task)
		}
	}

	h.log.Printf("Migration completed successfully. Total images processed: %d", completedImages)
	task.Status = "completed"
	task.EndTime = time.Now()
	h.updateMigrationTask(task)
}

func (h *RepositoryHandler) migrateImage(ctx context.Context, sourceRegistry, image, username, password string) error {
	// PARSE IMAGE NAME AND TAG
	imageParts := strings.Split(image, ":")
	imageName := imageParts[0]
	tag := "latest"
	if len(imageParts) > 1 {
		tag = imageParts[1]
	}

	// CLEAN PATH
	imagePath := path.Clean(imageName)

	// GET USERNAME FROM CONTEXT
	var ctxUsername string
	if username, ok := ctx.Value(constants.UsernameKey).(string); ok {
		ctxUsername = username
		h.log.Printf("Migrating image as user: %s", ctxUsername)
	} else {
		h.log.Printf("No username in context, using default")
		ctxUsername = "admin"
	}

	// BUILD SOURCE URL
	sourceURL := fmt.Sprintf("https://%s/v2/%s/manifests/%s",
		sourceRegistry,
		imagePath,
		tag)

	h.log.Printf("Fetching manifest from: %s", sourceURL)

	// CREATE CUSTOM TRANSPORT
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			ServerName:         h.config.Server.Domain,
			InsecureSkipVerify: true,
		},
	}

	// CREATE CLIENT WITH TIMEOUT
	client := &http.Client{
		Transport: tr,
		Timeout:   time.Minute * 10, // WHOLE OP MAX 10 MIN
	}

	// CREATE REQUEST WITH MANIFEST SUPPORT
	req, err := http.NewRequestWithContext(ctx, "GET", sourceURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %v", err)
	}

	// ADD MANIFEST ACCEPT HEADERS
	req.Header.Set("Docker-Distribution-Api-Version", "registry/2.0")
	req.Header.Add("Accept", "application/vnd.docker.distribution.manifest.v2+json")
	req.Header.Add("Accept", "application/vnd.docker.distribution.manifest.list.v2+json")
	req.Header.Add("Accept", "application/vnd.oci.image.manifest.v1+json")
	req.Header.Add("Accept", "application/vnd.oci.image.index.v1+json")
	req.Header.Add("Accept", "application/vnd.docker.distribution.manifest.v1+prettyjws")

	// ADD AUTH IF PROVIDED
	if username != "" && password != "" {
		req.SetBasicAuth(username, password)
	}

	// MAKE REQUEST
	resp, err := client.Do(req)
	if err != nil {
		h.log.Printf("Failed to fetch manifest from %s: %v", sourceURL, err)
		return fmt.Errorf("failed to fetch manifest: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		h.log.Printf("Manifest fetch failed with status %d: %s", resp.StatusCode, string(body))
		return fmt.Errorf("manifest fetch failed with status %d", resp.StatusCode)
	}

	// READ MANIFEST
	manifest, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read manifest: %v", err)
	}

	h.log.Printf("Successfully fetched manifest for %s:%s", imagePath, tag)

	// PARSE MANIFEST TO GET LAYERS
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

	if err := json.Unmarshal(manifest, &manifestObj); err != nil {
		return fmt.Errorf("failed to parse manifest: %v", err)
	}

	// CREATE DIRS FOR IMAGE
	manifestDir := filepath.Join(
		h.config.Storage.RootDirectory,
		"repositories",
		imagePath,
		"_manifests",
		"revisions",
	)
	if err := os.MkdirAll(manifestDir, 0755); err != nil {
		return fmt.Errorf("failed to create manifest directory: %v", err)
	}

	// STORE MANIFEST BY DIGEST
	manifestDigest := digest.FromBytes(manifest)

	// CHECK IF TAG ALREADY EXISTS WITH SAME DIGEST
	existingManifestPath := filepath.Join(
		h.config.Storage.RootDirectory,
		"repositories",
		imagePath,
		"_manifests",
		"tags",
		tag,
		"current",
		"link",
	)

	// READ EXISTING MANIFEST DIGEST IF IT EXISTS
	if _, err := os.Stat(existingManifestPath); err == nil {
		existingDigest, err := os.ReadFile(existingManifestPath)
		if err == nil {
			if string(existingDigest) == manifestDigest.String() {
				h.log.Printf("Tag %s already exists with same digest, skipping", tag)
				return nil
			}
			h.log.Printf("Tag %s exists but digest differs, updating", tag)
		}
	}

	manifestPath := filepath.Join(manifestDir, manifestDigest.String())
	if err := os.WriteFile(manifestPath, manifest, 0644); err != nil {
		return fmt.Errorf("failed to write manifest: %v", err)
	}

	h.log.Printf("Stored manifest for %s:%s with digest %s", imagePath, tag, manifestDigest)

	// CREATE SEMAPHORE FOR CONCURRENT DOWNLOADS
	sem := make(chan struct{}, 5) // LIMIT TO 5 CONCURRENT DOWNLOADS
	var wg sync.WaitGroup
	errChan := make(chan error, len(manifestObj.Layers)+1) // +1 FOR CONFIG

	// PULL CONFIG IN PARALLEL
	wg.Add(1)
	go func() {
		defer wg.Done()
		sem <- struct{}{}        // ACQUIRE
		defer func() { <-sem }() // RELEASE
		if err := h.pullAndStoreBlob(ctx, sourceRegistry, imagePath, manifestObj.Config.Digest, username, password); err != nil {
			errChan <- fmt.Errorf("failed to pull config: %v", err)
		}
	}()

	// PULL LAYERS IN PARALLEL
	for _, layer := range manifestObj.Layers {
		wg.Add(1)
		go func(layer struct {
			MediaType string `json:"mediaType"`
			Size      int64  `json:"size"`
			Digest    string `json:"digest"`
		}) {
			defer wg.Done()
			sem <- struct{}{}        // ACQUIRE
			defer func() { <-sem }() // RELEASE
			if err := h.pullAndStoreBlob(ctx, sourceRegistry, imagePath, layer.Digest, username, password); err != nil {
				errChan <- fmt.Errorf("failed to pull layer: %v", err)
			}
		}(layer)
	}

	// WAIT FOR ALL DOWNLOADS
	go func() {
		wg.Wait()
		close(errChan)
	}()

	// CHECK FOR ERRORS
	for err := range errChan {
		if err != nil {
			return err
		}
	}

	// UPDATE TAG
	tagDir := filepath.Join(
		h.config.Storage.RootDirectory,
		"repositories",
		imagePath,
		"_manifests",
		"tags",
		tag,
		"current",
	)
	if err := os.MkdirAll(tagDir, 0755); err != nil {
		return fmt.Errorf("failed to create tag directory: %v", err)
	}

	linkPath := filepath.Join(tagDir, "link")
	if err := os.WriteFile(linkPath, []byte(manifestDigest.String()), 0644); err != nil {
		return fmt.Errorf("failed to write tag link: %v", err)
	}

	// UPDATE IMAGE METADATA
	metadata := &models.ImageMetadata{
		ID:        manifestDigest.String(),
		Name:      imagePath,
		Tags:      []string{tag},
		Size:      calculateTotalSize(manifestObj),
		Owner:     ctxUsername,
		Labels:    make(map[string]string),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := h.repo.CreateImageMetadata(metadata); err != nil {
		h.log.Printf("Warning: failed to create image metadata: %v", err)
	}

	h.log.Printf("Successfully migrated image %s:%s", imagePath, tag)
	return nil
}

func (h *RepositoryHandler) pullAndStoreBlob(ctx context.Context, sourceRegistry, imagePath, digest, username, password string) error {
	h.log.Printf("Checking blob %s for image %s", digest, imagePath)

	// SPLIT DIGEST INTO ALGORITHM AND HASH
	digestParts := strings.Split(digest, ":")
	if len(digestParts) != 2 {
		return fmt.Errorf("invalid digest format: %s", digest)
	}

	// CHECK IF BLOB ALREADY EXISTS
	blobPath := filepath.Join(
		h.config.Storage.RootDirectory,
		"blobs",
		"sha256",
		digestParts[1],
	)

	if _, err := os.Stat(blobPath); err == nil {
		h.log.Printf("Blob %s already exists, creating link", digest)
		// JUST CREATE THE LINK
		return h.createBlobLink(imagePath, digest, digestParts[1])
	}

	// BLOB DOESN'T EXIST, PULL IT
	h.log.Printf("Pulling new blob %s", digest)

	// BUILD BLOB URL
	blobURL := fmt.Sprintf("https://%s/v2/%s/blobs/%s",
		sourceRegistry, imagePath, digest)

	// CREATE REQUEST WITH LARGER TIMEOUT
	req, err := http.NewRequestWithContext(ctx, "GET", blobURL, nil)
	if err != nil {
		return err
	}

	// ADD AUTH IF PROVIDED
	if username != "" && password != "" {
		req.SetBasicAuth(username, password)
	}

	// USE CUSTOM TRANSPORT WITH LARGER BUFFER
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: false,
			},
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 100,
			IdleConnTimeout:     90 * time.Second,
			DisableCompression:  true,
		},
		Timeout: 10 * time.Minute,
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to fetch blob: %s", resp.Status)
	}

	// CREATE BLOB DIRECTORIES
	blobDir := filepath.Join(
		h.config.Storage.RootDirectory,
		"blobs",
		"sha256",
	)
	if err := os.MkdirAll(blobDir, 0755); err != nil {
		return fmt.Errorf("failed to create blob directory: %v", err)
	}

	// CREATE TEMPORARY FILE
	tmpFile := blobPath + ".tmp"
	blob, err := os.OpenFile(tmpFile, os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return fmt.Errorf("failed to create temp blob file: %v", err)
	}
	defer os.Remove(tmpFile) // CLEAN TEMP IF THIS BREAKS

	// USE LARGER BUFFER FOR COPY
	buf := make([]byte, 1024*1024) // 1MB BUFFER
	hash := sha256.New()
	writer := io.MultiWriter(blob, hash)

	_, err = io.CopyBuffer(writer, resp.Body, buf)
	if err != nil {
		blob.Close()
		return fmt.Errorf("failed to write blob: %v", err)
	}
	blob.Close()

	// VERIFY HASH
	actualHash := hex.EncodeToString(hash.Sum(nil))
	if actualHash != digestParts[1] {
		return fmt.Errorf("hash mismatch: expected %s, got %s", digestParts[1], actualHash)
	}

	// MOVE TEMP FILE TO FINAL LOCATION
	if err := os.Rename(tmpFile, blobPath); err != nil {
		return fmt.Errorf("failed to move blob to final location: %v", err)
	}

	// CREATE LINK
	return h.createBlobLink(imagePath, digest, digestParts[1])
}

func (h *RepositoryHandler) createBlobLink(imagePath, digest, hash string) error {
	layerLinkDir := filepath.Join(
		h.config.Storage.RootDirectory,
		"repositories",
		imagePath,
		"_layers",
		"sha256",
		hash,
	)
	if err := os.MkdirAll(layerLinkDir, 0755); err != nil {
		return fmt.Errorf("failed to create layer link directory: %v", err)
	}

	linkPath := filepath.Join(layerLinkDir, "link")
	return os.WriteFile(linkPath, []byte(digest), 0644)
}

func (h *RepositoryHandler) ProxyCatalog(w http.ResponseWriter, r *http.Request) {
	sourceRegistry := r.URL.Query().Get("registry")
	username := r.URL.Query().Get("username")
	password := r.URL.Query().Get("password")

	if sourceRegistry == "" {
		http.Error(w, "source registry is required", http.StatusBadRequest)
		return
	}

	// CREATE TRANSPORT WITH TLS CONFIG
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			ServerName:         h.config.Server.Domain, // MY SERVER NAME
			InsecureSkipVerify: true,                   // IF SELF-SIGNED
		},
	}

	client := &http.Client{Transport: tr}

	// BUILD CATALOG URL

	catalogURL := fmt.Sprintf("https://%s/v2/_catalog", sourceRegistry)

	req, err := http.NewRequest("GET", catalogURL, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// ADD AUTH IF PROVIDED
	if username != "" && password != "" {
		req.SetBasicAuth(username, password)
	}

	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// COPY STATUS CODE AND HEADERS
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func (h *RepositoryHandler) ProxyTags(w http.ResponseWriter, r *http.Request) {
	sourceRegistry := r.URL.Query().Get("registry")
	repository := r.URL.Query().Get("repository")
	username := r.URL.Query().Get("username")
	password := r.URL.Query().Get("password")

	if sourceRegistry == "" || repository == "" {
		http.Error(w, "source registry and repository are required", http.StatusBadRequest)
		return
	}

	// CREATE TRANSPORT WITH TLS CONFIG
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			ServerName:         h.config.Server.Domain, // MY SERVER NAME
			InsecureSkipVerify: true,
		},
	}

	client := &http.Client{Transport: tr}

	// BUILD TAGS URL
	tagsURL := fmt.Sprintf("https://%s/v2/%s/tags/list", sourceRegistry, repository)
	req, err := http.NewRequest("GET", tagsURL, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// ADD AUTH IF PROVIDED
	if username != "" && password != "" {
		req.SetBasicAuth(username, password)
	}

	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// COPY STATUS CODE AND HEADERS
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// TASK MANAGEMENT
var migrationTasks = make(map[string]*MigrationTask)

func (h *RepositoryHandler) updateMigrationTask(task *MigrationTask) {
	migrationTasks[task.ID] = task
}

func (h *RepositoryHandler) getMigrationTask(taskID string) *MigrationTask {
	return migrationTasks[taskID]
}

func calculateTotalSize(manifest struct {
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
}) int64 {
	var totalSize int64
	totalSize += manifest.Config.Size
	for _, layer := range manifest.Layers {
		totalSize += layer.Size
	}
	return totalSize
}

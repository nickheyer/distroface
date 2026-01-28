package handlers

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/nickheyer/distroface/internal/constants"
	"github.com/nickheyer/distroface/internal/logging"
	"github.com/nickheyer/distroface/internal/metrics"
	"github.com/nickheyer/distroface/internal/models"
	"github.com/nickheyer/distroface/internal/repository"
	"github.com/nickheyer/distroface/internal/utils"
	"github.com/opencontainers/go-digest"
)

const bufSize = 1024 * 1024

type RepositoryHandler struct {
	repo         repository.Repository
	config       *models.Config
	log          *logging.LogService
	metrics      *metrics.MetricsService
	uploadHashes *struct {
		sync.RWMutex
		hashes map[string]*struct {
			hash     hash.Hash
			lastUsed time.Time
		}
	}
	cleanupTicker *time.Ticker
}

func NewRepositoryHandler(repo repository.Repository, cfg *models.Config, log *logging.LogService, metrics *metrics.MetricsService) *RepositoryHandler {
	h := &RepositoryHandler{
		repo:    repo,
		config:  cfg,
		log:     log,
		metrics: metrics,
		uploadHashes: &struct {
			sync.RWMutex
			hashes map[string]*struct {
				hash     hash.Hash
				lastUsed time.Time
			}
		}{
			hashes: make(map[string]*struct {
				hash     hash.Hash
				lastUsed time.Time
			}),
		},
		cleanupTicker: time.NewTicker(10 * time.Minute),
	}
	go h.cleanupOldHashes()

	return h
}

func (h *RepositoryHandler) cleanupOldHashes() {
	for range h.cleanupTicker.C {
		h.uploadHashes.Lock()
		now := time.Now()
		for id, hashInfo := range h.uploadHashes.hashes {
			// REMOVE HASHES NOT USED IN LAST 30 MINS
			if now.Sub(hashInfo.lastUsed) > 30*time.Minute {
				h.log.Printf("CLEANING UP UNUSED HASH FOR UPLOAD %s", id)
				delete(h.uploadHashes.hashes, id)
			}
		}
		h.uploadHashes.Unlock()
	}
}

func (h *RepositoryHandler) rmHash(uploadID string) {
	h.uploadHashes.Lock()
	delete(h.uploadHashes.hashes, uploadID)
	h.uploadHashes.Unlock()
}

func (h *RepositoryHandler) Shutdown() {
	if h.cleanupTicker != nil {
		h.cleanupTicker.Stop()
	}
}

func (h *RepositoryHandler) ListRepositories(w http.ResponseWriter, r *http.Request) {
	username := r.Context().Value(constants.UsernameKey).(string)
	h.log.Printf("Listing repositories for user: %s", username)

	userImages, err := h.repo.ListUserImages(username)
	if err != nil {
		h.log.Printf("Failed to list repositories: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	if userImages == nil {
		userImages = []*models.UserImage{}
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

	// PUT/GET SIZES IN MEM CACHE
	imgSizeCache := make(map[string]int64)
	getImageSize := func(imageID string) int64 {
		if size, ok := imgSizeCache[imageID]; ok {
			return size
		}

		physicalImg, err := h.repo.GetImageMetadata(imageID)
		var size int64 = 0
		if err == nil {
			size = physicalImg.Size
		}
		imgSizeCache[imageID] = size
		return size
	}

	// GROUPING BY REPO NAME
	repoMap := make(map[string]*RepositoryResponse)
	for _, img := range userImages {
		repo, exists := repoMap[img.Name]
		size := getImageSize(img.ImageID)
		if !exists {
			repo = &RepositoryResponse{
				ID:        img.ImageID,
				Name:      img.Name,
				Tags:      make([]TagInfo, 0),
				UpdatedAt: img.UpdatedAt,
				Owner:     img.Owner,
				TotalSize: size,
				Private:   img.Private,
			}
			repoMap[img.Name] = repo
		}

		// ADD TAG INFO
		for _, tagName := range img.Tags {
			tag := TagInfo{
				Name:    tagName,
				Size:    size,
				Digest:  img.ImageID, // USE MANIFEST AS ID
				Created: img.CreatedAt,
			}
			repo.Tags = append(repo.Tags, tag)
		}

		// UPDATE IF NEWER
		if img.UpdatedAt.After(repo.UpdatedAt) {
			repo.UpdatedAt = img.UpdatedAt
		}
	}

	// MAP TO SLICE AND ENSURE SIZE VALUES ARE PROPERLY SET
	repositories := make([]*RepositoryResponse, 0, len(repoMap))
	for _, repo := range repoMap {
		for i := range repo.Tags {
			// AGGR SIZE IF NOT EXISTING
			if repo.Tags[i].Size == 0 {
				physicalImg, err := h.repo.GetImageMetadata(repo.Tags[i].Digest)
				if err == nil {
					repo.Tags[i].Size = physicalImg.Size
				}
			}
		}
		repositories = append(repositories, repo)
	}

	// SORT BY RECENT UPDATE
	sort.Slice(repositories, func(i, j int) bool {
		timeI, _ := time.Parse(time.RFC3339, repositories[i].UpdatedAt.Format(time.RFC3339))
		timeJ, _ := time.Parse(time.RFC3339, repositories[j].UpdatedAt.Format(time.RFC3339))
		return timeI.After(timeJ)
	})

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(repositories); err != nil {
		h.log.Printf("Failed to encode response: %v", err)
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

	h.log.Printf("Deleting tag %s from repository %s by user %s", tag, name, username)

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
		h.log.Printf("Failed to read tag link: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	manifestDigest := strings.TrimSpace(string(digest))

	// REMOVE TAG DIRECTORY
	tagDir := filepath.Dir(filepath.Dir(tagLinkPath))
	if err := os.RemoveAll(tagDir); err != nil {
		h.log.Printf("Failed to remove tag directory: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	// CHECK FOR REMAINING TAGS
	if hasRemainingTags, err := h.checkRemainingTags(name, manifestDigest); err != nil {
		h.log.Printf("Error checking remaining tags: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	} else if !hasRemainingTags {
		// PERFORM FULL CLEANUP FOR LAST TAG
		if err := h.performFullCleanup(name, manifestDigest); err != nil {
			h.log.Printf("Error during full cleanup: %v", err)
			http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
			return
		}
	} else {
		// RM TAG
		if err := h.repo.DeleteUserImageTag(name, tag, username); err != nil {
			h.log.Printf("Error updating image metadata: %v", err)
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

	h.log.Printf("Listing tags for repository %s by user %s", name, username)

	// GET ALL USER IMAGES FOR REPO
	userImages, err := h.repo.ListUserImages(username)
	if err != nil {
		h.log.Printf("Failed to get repository metadata: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	// FILTER METADATA AND GET TAGS
	var tags []string
	tagMap := make(map[string]bool) // GET ONLY UNIQUE TAGS, DEDUP

	for _, img := range userImages {
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
		h.log.Printf("Failed to encode tags response: %v", err)
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
	h.log.Printf("Handling manifest request: method=%s repo=%s ref=%s user=%s path=%s",
		r.Method, name, reference, username, r.URL.Path)

	// NORMALIZE REPOSITORY PATH
	name = strings.TrimPrefix(name, "/")
	name = strings.TrimSuffix(name, "/")

	// ENSURE SUBPATH DIRECTORIES EXIST
	repoPath := filepath.Join(h.config.Storage.RootDirectory, "repositories", name)
	if err := os.MkdirAll(repoPath, 0755); err != nil {
		h.log.Printf("Failed to create repository directory structure: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

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
	// GET CURRENT USER
	username := r.Context().Value(constants.UsernameKey).(string)

	// CHECK IF USER HAS THIS TAG IN DATABASE
	_, err := h.repo.GetUserImageByNameAndTag(username, name, reference)
	if err != nil {
		// USER DOESN'T HAVE THIS TAG - FORCE UPLOAD BY RETURNING 404
		h.log.Errorf("\nError encountered while getting image by user and tag:\n", err)
		h.log.Printf("User %s doesn't have tag %s:%s in database.\n",
			username, name, reference)
		http.Error(w, "MANIFEST NOT FOUND", http.StatusNotFound)
		return
	}

	// USER HAS THIS TAG - SERVE MANIFEST NORMALLY
	manifestPath := h.resolveManifestPath(name, reference)
	if manifestPath == "" {
		http.Error(w, "MANIFEST NOT FOUND", http.StatusNotFound)
		return
	}

	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		http.Error(w, "MANIFEST NOT FOUND", http.StatusNotFound)
		return
	}

	// PARSE MANIFEST
	var manifestObj struct {
		MediaType string `json:"mediaType"`
	}
	if err := json.Unmarshal(manifest, &manifestObj); err != nil {
		h.log.Printf("Failed to parse manifest JSON: %v", err)
		http.Error(w, "INVALID MANIFEST", http.StatusBadRequest)
		return
	}

	// SET HEADERS
	manifestDigest := digest.FromBytes(manifest)
	w.Header().Set("Content-Type", manifestObj.MediaType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(manifest)))
	w.Header().Set("Docker-Content-Digest", manifestDigest.String())

	// SEND BODY FOR GET REQUESTS
	if r.Method != "HEAD" {
		w.Write(manifest)
	}
}

func (h *RepositoryHandler) putManifest(w http.ResponseWriter, r *http.Request, name, reference, username string) {
	h.log.Printf("Starting putManifest: repo=%s ref=%s user=%s contentType=%s",
		name, reference, username, r.Header.Get("Content-Type"))
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.log.Printf("Failed to read manifest body: %v", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// GET DIGEST AND LOG IT
	manifestDigest := digest.FromBytes(body)
	h.log.Printf("Calculated manifest digest: %s", manifestDigest)

	// ENSURE ALL PARENT DIRECTORIES EXIST
	manifestDir := filepath.Join(
		h.config.Storage.RootDirectory,
		"repositories",
		name,
		"_manifests",
		"revisions",
	)
	if err := os.MkdirAll(manifestDir, 0755); err != nil {
		h.log.Printf("Failed to create manifest directory: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	// STORE BY DIGEST
	manifestPath := filepath.Join(manifestDir, manifestDigest.String())
	h.log.Printf("Creating manifest at path: %s", manifestPath)
	if err := os.WriteFile(manifestPath, body, 0644); err != nil {
		h.log.Printf("Failed to write manifest: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	// PARSE MANIFEST FOR SIZE CALCULATION
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
		h.log.Printf("Failed to parse manifest JSON: %v\n", err)
		http.Error(w, "INVALID MANIFEST", http.StatusBadRequest)
		return
	}

	imgMetadata, err := h.repo.GetImageMetadata(manifestDigest.String())
	if err == nil {
		// UPDATE EXISTING
		imgMetadata.Size = calculateTotalSize(manifestObj)
		imgMetadata.UpdatedAt = time.Now()
		if err := h.repo.UpdateImageMetadata(imgMetadata); err != nil {
			h.log.Printf("Failed to update image metadata: %v", err)
		} else {
			h.log.Printf("Updated physical image metadata for digest %s", manifestDigest.String())
		}
	} else {
		imgMetadata = &models.ImageMetadata{
			ID:        manifestDigest.String(),
			Size:      calculateTotalSize(manifestObj),
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		if err := h.repo.CreateImageMetadata(imgMetadata); err != nil {
			h.log.Printf("Failed to create image metadata: %v\n", err)
		} else {
			h.log.Printf("Created image metadata: %+v\n", imgMetadata)
		}
	}

	// CREATE USER IMAGE REFERENCE FOR THIS TAG
	userImg := &models.UserImage{
		ImageID:   manifestDigest.String(),
		Name:      name,
		Tags:      []string{reference},
		Owner:     username,
		Labels:    make(map[string]string),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := h.repo.CreateUserImage(userImg); err != nil {
		h.log.Printf("Failed to create user image reference: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	} else {
		h.log.Printf("Created user image reference: %+v\n", userImg)
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
			h.log.Printf("Failed to create tag directory: %v\n", err)
			http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
			return
		}

		linkPath := filepath.Join(tagDir, "link")
		if err := os.WriteFile(linkPath, []byte(manifestDigest.String()), 0644); err != nil {
			h.log.Printf("Failed to write tag link: %v", err)
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

	h.log.Printf("Deleting manifest %s from repository %s by user %s", reference, name, username)

	// RESOLVE MANIFEST PATH
	manifestPath := h.resolveManifestPath(name, reference)
	if manifestPath == "" {
		http.Error(w, "MANIFEST NOT FOUND", http.StatusNotFound)
		return
	}

	// RM MANIFEST FILE
	if err := os.Remove(manifestPath); err != nil {
		h.log.Printf("Failed to delete manifest file: %v", err)
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
			h.log.Printf("Failed to remove tag directory: %v", err)
			// DONT FAIL ON FAILURE TO CLEANUP TAG
		}
	}

	// UPDATE USER IMAGE METADATA
	deleteErr := h.repo.DeleteUserImageTag(name, reference, username)
	if deleteErr != nil {
		h.log.Printf("Failed to delete tag from user image: %v", deleteErr)
		// Don't fail on tag removal error
	}

	w.WriteHeader(http.StatusAccepted)
}

// BLOB OPERATIONS
func (h *RepositoryHandler) InitiateBlobUpload(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	// CREATE UPLOAD DIR
	uploadDir := filepath.Join(h.config.Storage.RootDirectory, "_uploads")

	// IF DIGEST, THEN WE START AND END HERE,
	// NO IDEA WHY DOCKER DOES THIS,
	// BUT ITS A COMPACT FORM OF THE MULTIPART PACKED INTO AN IF BLOCK
	digestParam := r.URL.Query().Get("digest")
	if digestParam != "" {
		uploadID := uuid.New().String()
		if err := os.MkdirAll(uploadDir, 0755); err != nil {
			http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
			return
		}
		// CREATE EMPTY FILE
		uploadPath := filepath.Join(uploadDir, uploadID)
		f, err := os.Create(uploadPath)
		if err != nil {
			http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
			return
		}
		defer f.Close()
		hh := sha256.New()
		_, err = io.Copy(io.MultiWriter(f, hh), r.Body)
		if err != nil {
			os.Remove(uploadPath)
			http.Error(w, "UPLOAD FAILED", http.StatusInternalServerError)
			return
		}
		actual := fmt.Sprintf("sha256:%x", hh.Sum(nil))
		if actual != digestParam {
			os.Remove(uploadPath)
			http.Error(w, "Digest mismatch", http.StatusBadRequest)
			return
		}
		f.Close()
		blobDir := filepath.Join(h.config.Storage.RootDirectory, "blobs", "sha256")
		if err := os.MkdirAll(blobDir, 0755); err != nil {
			os.Remove(uploadPath)
			http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
			return
		}
		blobPath := filepath.Join(blobDir, strings.TrimPrefix(digestParam, "sha256:"))
		if err := os.Rename(uploadPath, blobPath); err != nil {
			if err2 := copyFile(uploadPath, blobPath); err2 != nil {
				os.Remove(uploadPath)
				http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
				return
			}
			os.Remove(uploadPath)
		}
		linkDir := filepath.Join(
			h.config.Storage.RootDirectory,
			"repositories",
			name,
			"_layers",
			"sha256",
			strings.TrimPrefix(digestParam, "sha256:"),
		)
		if err := os.MkdirAll(linkDir, 0755); err != nil {
			http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
			return
		}
		linkPath := filepath.Join(linkDir, "link")
		if err := os.WriteFile(linkPath, []byte(digestParam), 0644); err != nil {
			http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Docker-Content-Digest", digestParam)
		w.Header().Set("Location", fmt.Sprintf("/v2/%s/blobs/%s", name, digestParam))
		w.WriteHeader(http.StatusCreated)
		return
	}
	uploadID := uuid.New().String()
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	} // END OF MONOLITHIC UPLOAD CHAIN, FORTUNATELY WE DONT DO THIS OFTEN
	uploadPath := filepath.Join(uploadDir, uploadID)
	if _, err := os.Create(uploadPath); err != nil {
		h.log.Printf("Failed to create upload file: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	location := fmt.Sprintf("/v2/%s/blobs/uploads/%s", url.PathEscape(name), uploadID)
	w.Header().Set("Location", location)
	w.Header().Set("Docker-Upload-UUID", uploadID)
	w.Header().Set("Range", "0-0")
	w.WriteHeader(http.StatusAccepted)
}

func (h *RepositoryHandler) HandleBlobUpload(w http.ResponseWriter, r *http.Request) {
	// METRICS
	h.metrics.TrackUploadStart()
	startTime := time.Now()
	vars := mux.Vars(r)
	name := vars["name"]
	name = strings.TrimPrefix(name, "/")
	name = strings.TrimSuffix(name, "/")
	uploadID := vars["uuid"]
	uploadPath := filepath.Join(h.config.Storage.RootDirectory, "_uploads", uploadID)

	// STORE/UPDATE RUNNING HASH IN MEMORY BY UPLOAD ID
	h.uploadHashes.Lock()
	hashInfo, exists := h.uploadHashes.hashes[uploadID]
	if !exists {
		h.log.Printf("CREATING NEW HASH FOR %s", uploadID)
		hashInfo = &struct {
			hash     hash.Hash
			lastUsed time.Time
		}{
			hash:     sha256.New(),
			lastUsed: time.Now(),
		}
		h.uploadHashes.hashes[uploadID] = hashInfo
	}
	hashInfo.lastUsed = time.Now() // UPDATE LAST USED TIME
	hash := hashInfo.hash
	h.uploadHashes.Unlock()

	// GET OFFSET
	info, err := os.Stat(uploadPath)
	if err != nil && !os.IsNotExist(err) {
		h.rmHash(uploadID)
		h.metrics.TrackUploadFailed()
		h.log.Printf("Failed to stat upload: %v", err)
		http.Error(w, "INTERNAL SERVER ERROR", http.StatusInternalServerError)
		return
	}
	currentSize := info.Size()

	// OPEN FILE (SEEK TO OFFSET)
	file, err := os.OpenFile(uploadPath, os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		h.rmHash(uploadID)
		h.metrics.TrackUploadFailed()
		h.log.Printf("FAILED TO OPEN UPLOAD FILE: %v", err)
		http.Error(w, "INTERNAL SERVER ERROR", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	if _, err := file.Seek(currentSize, io.SeekStart); err != nil {
		h.rmHash(uploadID)
		h.metrics.TrackUploadFailed()
		h.log.Printf("Failed to seek: %v", err)
		http.Error(w, "INTERNAL SERVER ERROR", http.StatusInternalServerError)
		return
	}

	// TODO: NEED TO MESS WITH BUFFER SIZES AND SEE WHATS BEST FOR THIS
	bufWriter := bufio.NewWriterSize(file, bufSize)

	// CLEANUP HANDLING
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()

	// COPY DATA FROM BODY
	written, err := io.Copy(io.MultiWriter(bufWriter, hash), r.Body)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded || !utils.IsNetworkError(err) {
			h.rmHash(uploadID)
			h.metrics.TrackUploadFailed()
			h.log.Printf("Upload failed (network/timeout error): %v", err)
			http.Error(w, "REQUEST TIMEOUT", http.StatusRequestTimeout)
			return
		}
		// ENSURE FLUSH FOR NET ERRORS
		bufWriter.Flush()
		return
	}

	h.log.Printf("AFTER COPY - BYTES WRITTEN: %d", written)

	// FLUSH REMAINING DATA
	if err := bufWriter.Flush(); err != nil {
		h.rmHash(uploadID)
		h.metrics.TrackUploadFailed()
		h.log.Printf("FAILED TO FLUSH BUFFER: %v", err)
		http.Error(w, "FAILED TO SAVE UPLOAD", http.StatusInternalServerError)
		return
	}

	// COMPLETE UPLOAD, END METRICS
	h.metrics.TrackUploadComplete(written, time.Since(startTime))
	w.Header().Set("Docker-Upload-UUID", uploadID)
	w.Header().Set("Range", fmt.Sprintf("0-%d", currentSize+written-1))
	w.Header().Set("Location", fmt.Sprintf("/v2/%s/blobs/uploads/%s", name, uploadID))
	w.WriteHeader(http.StatusAccepted)
}

func (h *RepositoryHandler) GetBlobUploadOffset(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	uploadID := vars["uuid"]
	uploadPath := filepath.Join(h.config.Storage.RootDirectory, "_uploads", uploadID)

	info, err := os.Stat(uploadPath)
	if os.IsNotExist(err) {
		http.Error(w, "UPLOAD NOT FOUND", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	offset := info.Size()
	w.Header().Set("Docker-Upload-UUID", uploadID)
	if offset > 0 {
		w.Header().Set("Range", fmt.Sprintf("0-%d", offset-1))
	} else {
		w.Header().Set("Range", "0-0")
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *RepositoryHandler) CompleteBlobUpload(w http.ResponseWriter, r *http.Request) {
	// METRICS
	h.metrics.TrackUploadStart()
	startTime := time.Now()

	vars := mux.Vars(r)

	// NORMALIZE NAME
	name := strings.TrimPrefix(vars["name"], "/")
	name = strings.TrimSuffix(name, "/")
	uploadID := vars["uuid"]
	expected := r.URL.Query().Get("digest")
	if expected == "" {
		http.Error(w, "MISSING DIGEST", http.StatusBadRequest)
		return
	}
	uploadPath := filepath.Join(h.config.Storage.RootDirectory, "_uploads", uploadID)

	// TRY GET HASH WE BUILT INCREMENTALLY
	h.uploadHashes.Lock()
	hashInfo, ok := h.uploadHashes.hashes[uploadID]
	if ok {
		delete(h.uploadHashes.hashes, uploadID) // REMOVE IMMEDIATELY
	}
	h.uploadHashes.Unlock()

	if !ok {
		// NO EXISTING HASH, CREATE NEW ONE FOR FINAL DATA
		hashInfo = &struct {
			hash     hash.Hash
			lastUsed time.Time
		}{
			hash:     sha256.New(),
			lastUsed: time.Now(),
		}
	}

	file, err := os.OpenFile(uploadPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		http.Error(w, "UPLOAD NOT FOUND", http.StatusNotFound)
		return
	}
	defer file.Close()

	// WRITE FILE AND HASH
	written, err := io.Copy(io.MultiWriter(file, hashInfo.hash), r.Body)
	if err != nil {
		http.Error(w, "UPLOAD FAILED", http.StatusInternalServerError)
		return
	}

	// NOW RM HASH
	actual := fmt.Sprintf("sha256:%x", hashInfo.hash.Sum(nil))
	h.rmHash(uploadID)

	if actual != expected {
		os.Remove(uploadPath)
		h.metrics.TrackUploadFailed()
		http.Error(w, "Digest mismatch", http.StatusBadRequest)
		return
	}

	blobDir := filepath.Join(h.config.Storage.RootDirectory, "blobs", "sha256")
	if err := os.MkdirAll(blobDir, 0755); err != nil {
		os.Remove(uploadPath)
		h.metrics.TrackUploadFailed()
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}
	blobPath := filepath.Join(blobDir, strings.TrimPrefix(expected, "sha256:"))

	// TRY RENAME, FALLBACK TO COPY
	if err := os.Rename(uploadPath, blobPath); err != nil {
		if err2 := copyFile(uploadPath, blobPath); err2 != nil {
			os.Remove(uploadPath)
			h.metrics.TrackUploadFailed()
			http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
			return
		}
		os.Remove(uploadPath)
	}
	// CREATE REPO LINK
	linkDir := filepath.Join(
		h.config.Storage.RootDirectory,
		"repositories",
		name,
		"_layers",
		"sha256",
		strings.TrimPrefix(expected, "sha256:"),
	)
	if err := os.MkdirAll(linkDir, 0755); err != nil {
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		h.metrics.TrackUploadFailed()
		return
	}
	linkPath := filepath.Join(linkDir, "link")
	if err := os.WriteFile(linkPath, []byte(expected), 0644); err != nil {
		h.metrics.TrackUploadFailed()
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	h.metrics.TrackUploadComplete(written, time.Since(startTime))
	w.Header().Set("Docker-Content-Digest", expected)
	w.Header().Set("Location", fmt.Sprintf("/v2/%s/blobs/%s", name, expected))
	w.WriteHeader(http.StatusCreated)
}

func (h *RepositoryHandler) GetBlob(w http.ResponseWriter, r *http.Request) {
	// METRICS: MARK DL START AND STATT TIME
	h.metrics.TrackDownloadStart()
	startTime := time.Now()
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
		h.log.Printf("Blob not found in repository: %s", digest)
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
		h.log.Printf("Failed to open blob: %v", err)
		http.Error(w, "BLOB NOT FOUND", http.StatusNotFound)
		return
	}
	defer blob.Close()

	info, err := blob.Stat()
	if err != nil {
		h.metrics.TrackDownloadFailed()
		h.log.Printf("Failed to get blob info: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	// SET RESPONSE HEADERS
	w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Docker-Content-Digest", digest)
	w.Header().Set("Accept-Ranges", "bytes")

	// CHECK FOR RANGE REQUEST
	rangeHeader := r.Header.Get("Range")
	var bytesWritten int64
	var contentLength int64

	if rangeHeader != "" {
		var start, end int64
		_, err := fmt.Sscanf(rangeHeader, "bytes=%d-%d", &start, &end)
		if err != nil {
			h.metrics.TrackDownloadFailed()
			h.log.Printf("Invalid range header: %v", err)
			http.Error(w, "Invalid Range", http.StatusRequestedRangeNotSatisfiable)
			return
		}

		// IF END IS NOT PROVIDED, SET TO EOF
		if end == 0 {
			end = info.Size() - 1
		}

		// CHECK RANGES
		if start > end || start < 0 || end >= info.Size() {
			http.Error(w, "Invalid Range", http.StatusRequestedRangeNotSatisfiable)
			return
		}

		// CALC LEN IN REQUEST
		contentLength = end - start + 1

		// SET HEADERS FOR PARTIAL
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, info.Size()))
		w.Header().Set("Content-Length", fmt.Sprintf("%d", contentLength))
		w.WriteHeader(http.StatusPartialContent)

		// SEEK TO OFFSET
		_, err = blob.Seek(start, io.SeekStart)
		if err != nil {
			h.metrics.TrackDownloadFailed()
			h.log.Printf("Failed to seek blob: %v", err)
			http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
			return
		}

		// RANGE HANDLER
		bytesWritten, err = io.CopyN(w, blob, contentLength)
		if err != nil {
			if !utils.IsNetworkError(err) {
				h.metrics.TrackDownloadFailed()
				h.log.Printf("Error while using CopyN: %v", err)
				http.Error(w, "Failed to process Download", http.StatusInternalServerError)
				return
			}
		}
	} else {
		// FULL DOWNLOAD, USE FILE SIZE
		buf := make([]byte, bufSize)
		bytesWritten, err = io.CopyBuffer(w, blob, buf)
		if err != nil {
			if !utils.IsNetworkError(err) {
				h.metrics.TrackDownloadFailed()
				h.log.Printf("Error while using CopyBuffer: %v", err)
				http.Error(w, "Failed to process Download", http.StatusInternalServerError)
				return
			}
		}
	}

	// REPORT WITH NUM BYTES SENT
	h.metrics.TrackDownloadComplete(bytesWritten, time.Since(startTime))
}

func (h *RepositoryHandler) DeleteBlob(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]
	digest := vars["digest"]
	username := r.Context().Value(constants.UsernameKey).(string)

	h.log.Printf("Deleting blob %s from repository %s by user %s", digest, name, username)

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
		h.log.Printf("Failed to check layer link: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	// RM LAYER LINK IF IT EXISTS
	if err := os.RemoveAll(filepath.Dir(layerLink)); err != nil {
		h.log.Printf("Failed to remove layer link: %v", err)
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
		h.log.Printf("Failed to check blob references: %v", err)
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
			h.log.Printf("Failed to delete blob file: %v", err)
			http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusAccepted)
}

// HELPER FUNCTIONS
func (h *RepositoryHandler) resolveManifestPath(name, reference string) string {
	// TRIM EXTRA SLASHES
	name = strings.TrimPrefix(name, "/")
	name = strings.TrimSuffix(name, "/")

	// ENSURE SUBPATH DIRS EXIST
	repoPath := filepath.Join(h.config.Storage.RootDirectory, "repositories", name)
	if err := os.MkdirAll(repoPath, 0755); err != nil {
		h.log.Printf("Failed to create repository directory structure: %v", err)
		return ""
	}

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
		tagPath := filepath.Join(
			h.config.Storage.RootDirectory,
			"repositories",
			name,
			"_manifests",
			"tags",
			reference,
			"current",
			"link",
		)
		digest, err := os.ReadFile(tagPath)
		if err != nil {
			h.log.Printf("Failed to read tag link at %s: %v", tagPath, err)
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

	// ENSURE ALL PARENT DIRS EXIST
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0755); err != nil {
		h.log.Printf("Failed to create manifest directory structure: %v", err)
		return ""
	}

	// DOES MANIFEST EXIST
	if _, err := os.Stat(manifestPath); err != nil {
		h.log.Printf("Manifest not found at %s: %v", manifestPath, err)
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
			h.log.Printf("Warning: failed to remove layer link directory: %v", err)
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
				h.log.Printf("Warning: failed to remove blob: %v", err)
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
	userImages, err := h.repo.ListUserImages(username)
	if err != nil {
		h.log.Printf("Failed to get user images: %v", err)
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	// FIND USER IMAGE
	var userImageID int
	found := false
	for _, img := range userImages {
		if img.ImageID == req.ID {
			userImageID = img.ID
			found = true
			break
		}
	}

	if !found {
		h.log.Printf("User %s not authorized for image %s", username, req.ID)
		http.Error(w, "Not authorized", http.StatusForbidden)
		return
	}

	if err := h.repo.UpdateUserImageVisibility(userImageID, req.Private); err != nil {
		h.log.Printf("Failed to update visibility: %v", err)
		http.Error(w, "Failed to update visibility", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *RepositoryHandler) ListGlobalRepositories(w http.ResponseWriter, r *http.Request) {
	username := r.Context().Value(constants.UsernameKey).(string)

	// GET ALL PUBLIC IMAGES AND USER'S PRIVATE IMAGES
	userImages, err := h.repo.ListPublicUserImages()
	if err != nil {
		http.Error(w, "Failed to list repositories", http.StatusInternalServerError)
		return
	}

	// GET USER'S PRIVATE IMAGES
	privateImages, err := h.repo.ListUserImages(username)
	if err != nil {
		http.Error(w, "Failed to list user repositories", http.StatusInternalServerError)
		return
	}

	// COMBINE WITHOUT DEDUPLICATING BY IMAGE ID
	var combined []*models.UserImage

	combined = append(combined, userImages...)

	// ADD USER'S PRIVATE IMAGES
	for _, img := range privateImages {
		if img.Private {
			combined = append(combined, img)
		}
	}

	// SORT BY UPDATED_AT TIME (NEWEST FIRST)
	sort.Slice(combined, func(i, j int) bool {
		return combined[i].UpdatedAt.After(combined[j].UpdatedAt)
	})

	// CALCULATE TOTALS WITH SIZE INFORMATION
	var totalSize int64
	imageSizeCache := make(map[string]int64)
	for _, img := range combined {
		if _, exists := imageSizeCache[img.ImageID]; !exists {
			physicalImg, err := h.repo.GetImageMetadata(img.ImageID)
			if err == nil {
				imageSizeCache[img.ImageID] = physicalImg.Size
				totalSize += physicalImg.Size
			} else {
				imageSizeCache[img.ImageID] = 0
			}
		} else {
			totalSize += imageSizeCache[img.ImageID]
		}
	}

	type EnhancedUserImage struct {
		*models.UserImage
		Size int64 `json:"size"`
	}

	enhancedImages := make([]*EnhancedUserImage, 0, len(combined))
	for _, img := range combined {
		size := imageSizeCache[img.ImageID]
		enhancedImages = append(enhancedImages, &EnhancedUserImage{
			UserImage: img,
			Size:      size,
		})
	}

	response := struct {
		TotalImages int64                `json:"total_images"`
		TotalSize   int64                `json:"total_size"`
		Images      []*EnhancedUserImage `json:"images"`
	}{
		TotalImages: int64(len(combined)),
		TotalSize:   totalSize,
		Images:      enhancedImages,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

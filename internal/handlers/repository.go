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
	"strconv"
	"strings"
	"sync"
	"time"

	"slices"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/nickheyer/distroface/internal/constants"
	"github.com/nickheyer/distroface/internal/logging"
	"github.com/nickheyer/distroface/internal/metrics"
	"github.com/nickheyer/distroface/internal/models"
	"github.com/nickheyer/distroface/internal/repository"
	"github.com/nickheyer/distroface/internal/utils"
	"github.com/opencontainers/go-digest"
	"gorm.io/gorm"
)

const bufSize = 1024 * 1024

type RepositoryHandler struct {
	repo         repository.Repository
	db           *gorm.DB
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
	var db *gorm.DB
	if gormRepo, ok := repo.(*repository.GormRepository); ok {
		db = gormRepo.GetDB()
	}

	h := &RepositoryHandler{
		repo:    repo,
		db:      db,
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
		h.log.Printf("Failed to list user images: %v", err)
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
		ID          string    `json:"id"`
		Name        string    `json:"name"`
		FullName    string    `json:"full_name"` // username/repo
		Tags        []TagInfo `json:"tags"`
		UpdatedAt   time.Time `json:"updated_at"`
		Owner       string    `json:"owner"`
		TotalSize   int64     `json:"size"`
		Private     bool      `json:"private"`
		IsOwnedByMe bool      `json:"is_owned_by_me"`
	}

	// GET ALL IMAGE ID INTO SINGLE QUERY
	imageIDs := make([]string, 0, len(userImages))
	for _, userImg := range userImages {
		imageIDs = append(imageIDs, userImg.ImageID)
	}

	// DEDUP ID
	imageIDsMap := make(map[string]bool)
	uniqueImageIDs := make([]string, 0)
	for _, id := range imageIDs {
		if !imageIDsMap[id] {
			imageIDsMap[id] = true
			uniqueImageIDs = append(uniqueImageIDs, id)
		}
	}

	// GET IMAGE METADATA FOR IDS
	var metadataList []*models.ImageMetadata
	if err := h.db.Where("id IN ?", uniqueImageIDs).Find(&metadataList).Error; err != nil {
		h.log.Printf("Failed to get image metadata: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	// CREATE MAP OF ID TO METADATA FOR LOOKUPS
	metadataMap := make(map[string]*models.ImageMetadata)
	for _, md := range metadataList {
		metadataMap[md.ID] = md
	}

	// GROUPING BY REPO NAME
	repoMap := make(map[string]*RepositoryResponse)

	for _, userImg := range userImages {
		repo, exists := repoMap[userImg.Name]
		if !exists {
			fullName := userImg.Name
			if userImg.Username != username {
				fullName = userImg.Username + "/" + userImg.Name
			}

			repo = &RepositoryResponse{
				ID:          "", // SET IN LOOP
				Name:        userImg.Name,
				FullName:    fullName,
				Tags:        make([]TagInfo, 0),
				UpdatedAt:   userImg.UpdatedAt,
				Owner:       userImg.Username,
				TotalSize:   0, // ACCUMULATED
				Private:     userImg.Private,
				IsOwnedByMe: userImg.Username == username,
			}
			repoMap[userImg.Name] = repo
		}

		// GET METADATA
		metadata, exists := metadataMap[userImg.ImageID]
		if !exists {
			h.log.Printf("Warning: metadata not found for image ID %s", userImg.ImageID)
			continue
		}

		// ADD TAG INFO
		tag := TagInfo{
			Name:    userImg.Tag,
			Size:    metadata.Size,
			Digest:  userImg.ImageID, // USE MANIFEST DIGEST
			Created: userImg.CreatedAt,
		}
		repo.Tags = append(repo.Tags, tag)
		repo.TotalSize += metadata.Size

		// SET REPO ID TO A CONSISTENT VALUE (USING FIRST IMAGE'S ID)
		if repo.ID == "" {
			repo.ID = userImg.ImageID
		}

		// UPDATE IF NEWER
		if userImg.UpdatedAt.After(repo.UpdatedAt) {
			repo.UpdatedAt = userImg.UpdatedAt
		}
	}

	// MAP TO SLICE
	repositories := make([]*RepositoryResponse, 0, len(repoMap))
	for _, repo := range repoMap {
		repositories = append(repositories, repo)
	}

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

	// VALIDATE OWNERSHIP
	userImage, err := h.repo.GetUserImage(username, name, tag)
	if err != nil {
		h.log.Printf("Failed to get user image: %v", err)
		http.Error(w, "TAG NOT FOUND", http.StatusNotFound)
		return
	}

	// FOR LATER FILE CLEANUP
	imageID := userImage.ImageID
	fsPath := h.getRepositoryFSPath(name)

	// GET TAG'S MANIFEST DIGEST
	tagLinkPath := filepath.Join(
		h.config.Storage.RootDirectory,
		"repositories",
		fsPath,
		"_manifests",
		"tags",
		tag,
		"current",
		"link",
	)

	digest, err := os.ReadFile(tagLinkPath)
	if err != nil {
		if os.IsNotExist(err) {
			// FILE SYSTEM ENTRY DOESNT EXIST BUT DB HAS IT - DELETE FROM DB
			if err := h.repo.DeleteUserImage(username, name, tag); err != nil {
				h.log.Printf("Failed to delete user image: %v", err)
				http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
			} else {
				w.WriteHeader(http.StatusAccepted)
			}
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

	// DELETE USER IMAGE RECORD
	if err := h.repo.DeleteUserImage(username, name, tag); err != nil {
		h.log.Printf("Failed to delete user image: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	// CHECK FOR REMAINING TAGS
	if hasRemainingTags, err := h.checkRemainingTags(name, manifestDigest); err != nil {
		h.log.Printf("Error checking remaining tags: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	} else if !hasRemainingTags {
		// CHECK EXTERNAL USER REFERENCES TO AVOID CASCADE
		var count int64
		if err := h.db.Model(&models.UserImage{}).Where("image_id = ?", imageID).Count(&count).Error; err != nil {
			h.log.Printf("Error checking for remaining image references: %v", err)
		}

		if count == 0 {
			// PERFORM FULL CLEANUP FOR LAST TAG
			if err := h.performFullCleanup(name, manifestDigest); err != nil {
				h.log.Printf("Error during full cleanup: %v", err)
				http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
				return
			}
		}
	}

	w.WriteHeader(http.StatusAccepted)
}

func (h *RepositoryHandler) ListTags(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]
	username := r.Context().Value(constants.UsernameKey).(string)

	h.log.Printf("Listing tags for repository %s by user %s", name, username)

	hasAccess, err := h.hasRepositoryAccess(username, name)
	if err != nil {
		h.log.Printf("Error checking access for repo %s: %v", name, err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	if !hasAccess {
		h.log.Printf("User %s doesn't have access to repository %s", username, name)
		http.Error(w, "UNAUTHORIZED", http.StatusUnauthorized)
		return
	}

	// QUERY IMAGES
	var userImages []*models.UserImage
	if err := h.db.Where("username = ? AND name = ?", username, name).Find(&userImages).Error; err != nil {
		h.log.Printf("Failed to get user images: %v", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	// EXTRACT TAGS
	var tags []string
	for _, img := range userImages {
		tags = append(tags, img.Tag)
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
	username := r.Context().Value(constants.UsernameKey).(string)

	// CHECK IF THIS REPOSITORY IS ACCESSIBLE TO THIS USER
	hasAccess, err := h.hasRepositoryAccess(username, name)
	if err != nil {
		h.log.Printf("Error checking access for repo %s: %v", name, err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	if !hasAccess {
		h.log.Printf("User %s doesn't have access to repository %s", username, name)
		http.Error(w, "UNAUTHORIZED", http.StatusUnauthorized)
		return
	}

	// RESOLVE MANIFEST PATH
	manifestPath := h.resolveManifestPath(name, reference)
	if manifestPath == "" {
		// CHECK FOR AMBIGUOUS REPOSITORY NAME CASE
		owner, _ := h.parseRepoName(name)

		// IF THIS IS NOT A NAMESPACED REPOSITORY, IT MIGHT BE AN AMBIGUOUS NAME
		if owner == "" {
			// TRY TO FIND THE APPROPRIATE REPOSITORY OWNER USING OUR FALLBACK METHOD
			resolvedOwner, err := h.resolveRepositoryFallback(username, name)
			if err == nil && resolvedOwner != "" {
				// WE FOUND AN OWNER - TRY AGAIN WITH THE FULLY QUALIFIED PATH USING THE USER.OWNER FORMAT
				namespacedPath := "user." + resolvedOwner + "/" + name
				h.log.Printf("Ambiguous repository name detected, trying with explicit namespace: %s", namespacedPath)

				// TRY TO RESOLVE THE MANIFEST WITH THE NAMESPACED PATH
				manifestPath = h.resolveManifestPath(namespacedPath, reference)
				if manifestPath == "" {
					h.log.Printf("Failed to resolve manifest path for %s:%s even with namespace resolution", namespacedPath, reference)
					http.Error(w, "MANIFEST NOT FOUND", http.StatusNotFound)
					return
				}
			} else {
				h.log.Printf("Failed to resolve manifest path for %s:%s", name, reference)
				http.Error(w, "MANIFEST NOT FOUND", http.StatusNotFound)
				return
			}
		} else {
			h.log.Printf("Failed to resolve manifest path for %s:%s", name, reference)
			http.Error(w, "MANIFEST NOT FOUND", http.StatusNotFound)
			return
		}
	}

	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		h.log.Printf("Failed to read manifest at %s: %v", manifestPath, err)
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
	h.log.Printf("Starting putManifest: repo=%s ref=%s user=%s contentType=%s",
		name, reference, username, r.Header.Get("Content-Type"))

	// PREVENT REPOSITORY NAMES STARTING WITH "USER." WHICH IS A RESERVED PREFIX FOR REPOSITORY NAMESPACING
	if strings.HasPrefix(strings.ToLower(name), "user.") {
		h.log.Printf("User %s attempted to push to a repository with reserved prefix: %s", username, name)
		http.Error(w, "Repository name cannot start with 'user.' - this is a reserved prefix", http.StatusBadRequest)
		return
	}

	// PARSE THE REPOSITORY PATH TO CHECK IF IT'S NAMESPACED
	targetOwner, repoName := h.parseRepoName(name)

	// CHECK IF USER HAS PERMISSION TO PUSH TO THIS REPOSITORY
	hasAccess, err := h.hasRepositoryPushAccess(username, name)
	if err != nil {
		h.log.Printf("Error checking push access for repo %s: %v", name, err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	if !hasAccess {
		h.log.Printf("User %s doesn't have push access to repository %s", username, name)
		http.Error(w, "UNAUTHORIZED", http.StatusUnauthorized)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.log.Printf("Failed to read manifest body: %v", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// GET DIGEST AND LOG IT
	manifestDigest := digest.FromBytes(body)
	h.log.Printf("Calculated manifest digest: %s", manifestDigest)

	// GET THE APPROPRIATE FILESYSTEM PATH
	fsPath := h.getRepositoryFSPath(name)

	// ENSURE ALL PARENT DIRECTORIES EXIST
	manifestDir := filepath.Join(
		h.config.Storage.RootDirectory,
		"repositories",
		fsPath, // Use the resolved filesystem path
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
	var manifestObj utils.ManifestMetadata
	if err := json.Unmarshal(body, &manifestObj); err != nil {
		h.log.Printf("Failed to parse manifest JSON: %v", err)
		http.Error(w, "INVALID MANIFEST", http.StatusBadRequest)
		return
	}

	// GET OR CREATE
	_, err = h.repo.GetImageMetadata(manifestDigest.String())
	if err != nil {
		// CREATE NEW METADATA FOR THIS IMAGE
		metadata := &models.ImageMetadata{
			ID:        manifestDigest.String(),
			Size:      utils.CalculateTotalSize(manifestObj),
			Labels:    make(map[string]string),
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		if err := h.repo.CreateImageMetadata(metadata); err != nil {
			h.log.Printf("Failed to create image metadata: %v", err)
		}
	}

	// DETERMINE THE REPOSITORY NAME TO USE BASED ON NAMESPACING
	repoNameToStore := name
	if targetOwner != "" {
		// IF USING NAMESPACED FORMAT, STORE JUST THE REPO PART WITHOUT THE USERNAME PREFIX
		repoNameToStore = repoName
	}

	// UPSERT
	userImage := &models.UserImage{
		Username:  username,
		Name:      repoNameToStore,
		Tag:       reference,
		ImageID:   manifestDigest.String(),
		Private:   false, // DEFAULT AS PUBLIC
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := h.repo.CreateUserImage(userImage); err != nil {
		h.log.Printf("Failed to create/update user image: %v", err)
	}

	// UPDATE LINK IF TAG
	if !strings.HasPrefix(reference, "sha256:") {
		tagDir := filepath.Join(
			h.config.Storage.RootDirectory,
			"repositories",
			fsPath, // Use the resolved filesystem path
			"_manifests",
			"tags",
			reference,
			"current",
		)
		if err := os.MkdirAll(tagDir, 0755); err != nil {
			h.log.Printf("Failed to create tag directory: %v", err)
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
		// GET THE APPROPRIATE FILESYSTEM PATH
		fsPath := h.getRepositoryFSPath(name)

		tagLinkPath := filepath.Join(
			h.config.Storage.RootDirectory,
			"repositories",
			fsPath,
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

	// ORPHANED METADATA IS CLEANED UP AUTOMATICALLY!

	w.WriteHeader(http.StatusAccepted)
}

// BLOB OPERATIONS
func (h *RepositoryHandler) InitiateBlobUpload(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]
	username := r.Context().Value(constants.UsernameKey).(string)

	// CHECK IF USER HAS PERMISSION TO PUSH TO THIS REPOSITORY
	hasAccess, err := h.hasRepositoryPushAccess(username, name)
	if err != nil {
		h.log.Printf("Error checking push access for repo %s: %v", name, err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	if !hasAccess {
		h.log.Printf("User %s doesn't have push access to repository %s", username, name)
		http.Error(w, "UNAUTHORIZED", http.StatusUnauthorized)
		return
	}

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
	username := r.Context().Value(constants.UsernameKey).(string)

	// CHECK IF USER HAS PERMISSION TO PUSH TO THIS REPOSITORY
	hasAccess, err := h.hasRepositoryPushAccess(username, name)
	if err != nil {
		h.log.Printf("Error checking push access for repo %s: %v", name, err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	if !hasAccess {
		h.log.Printf("User %s doesn't have push access to repository %s", username, name)
		http.Error(w, "UNAUTHORIZED", http.StatusUnauthorized)
		return
	}
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

func (h *RepositoryHandler) DeleteBlobUpload(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	uploadID := vars["uuid"]

	// THIS IS CALLED BY DOCKER CLIENT AFTER A COMPLETE UPLOAD OR ON CLEANUP
	// WE JUST RETURN A 200 OK RESPONSE AND CLEAN UP THE FILE IF IT EXISTS
	uploadPath := filepath.Join(h.config.Storage.RootDirectory, "_uploads", uploadID)

	// CHECK IF THE FILE EXISTS AND REMOVE IT - DON'T ERROR IF IT DOESN'T
	if _, err := os.Stat(uploadPath); err == nil {
		if err := os.Remove(uploadPath); err != nil {
			h.log.Printf("Warning: failed to remove upload file %s: %v", uploadID, err)
		} else {
			h.log.Printf("Removed upload file %s", uploadID)
		}
	}

	// REMOVE ANY IN-MEMORY HASH STATE
	h.rmHash(uploadID)

	w.WriteHeader(http.StatusOK)
}

func (h *RepositoryHandler) CompleteBlobUpload(w http.ResponseWriter, r *http.Request) {
	// METRICS
	h.metrics.TrackUploadStart()
	startTime := time.Now()

	vars := mux.Vars(r)

	// NORMALIZE NAME
	name := strings.TrimPrefix(vars["name"], "/")
	name = strings.TrimSuffix(name, "/")
	username := r.Context().Value(constants.UsernameKey).(string)

	// CHECK IF USER HAS PERMISSION TO PUSH TO THIS REPOSITORY
	hasAccess, err := h.hasRepositoryPushAccess(username, name)
	if err != nil {
		h.log.Printf("Error checking push access for repo %s: %v", name, err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	if !hasAccess {
		h.log.Printf("User %s doesn't have push access to repository %s", username, name)
		http.Error(w, "UNAUTHORIZED", http.StatusUnauthorized)
		return
	}
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
	// GET THE APPROPRIATE FILESYSTEM PATH
	fsPath := h.getRepositoryFSPath(name)

	// CREATE REPO LINK
	linkDir := filepath.Join(
		h.config.Storage.RootDirectory,
		"repositories",
		fsPath, // Use the resolved filesystem path
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
	username := r.Context().Value(constants.UsernameKey).(string)

	// WHEN DOCKER IS PUSHING, IT MAKES A HEAD REQUEST TO CHECK IF THE BLOB ALREADY EXISTS
	// SO FOR HEAD REQUESTS, WE NEED TO CHECK PUSH ACCESS INSTEAD OF PULL ACCESS
	var accessAllowed bool

	if r.Method == "HEAD" {
		// FOR HEAD REQUESTS DURING PUSH, WE SHOULD USE PUSH ACCESS RULES
		hasAccess, err := h.hasRepositoryPushAccess(username, name)
		if err != nil {
			h.log.Printf("Error checking push access for repo %s: %v", name, err)
			http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
			return
		}
		accessAllowed = hasAccess
	} else {
		// FOR GET REQUESTS (PULLING), WE USE NORMAL ACCESS RULES
		hasAccess, err := h.hasRepositoryAccess(username, name)
		if err != nil {
			h.log.Printf("Error checking access for repo %s: %v", name, err)
			http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
			return
		}
		accessAllowed = hasAccess
	}

	if !accessAllowed {
		h.log.Printf("User %s doesn't have access to repository %s", username, name)
		http.Error(w, "UNAUTHORIZED", http.StatusUnauthorized)
		return
	}

	// GET THE APPROPRIATE FILESYSTEM PATH
	fsPath := h.getRepositoryFSPath(name)

	// VERIFY BLOB EXISTS AND IS LINKED TO REPOSITORY
	layerLink := filepath.Join(
		h.config.Storage.RootDirectory,
		"repositories",
		fsPath, // Use the resolved filesystem path
		"_layers",
		"sha256",
		strings.TrimPrefix(digest, "sha256:"),
		"link",
	)
	if _, err := os.Stat(layerLink); err != nil {
		// IF BLOB NOT FOUND DIRECTLY, CHECK FOR AMBIGUOUS REPOSITORY NAME
		owner, _ := h.parseRepoName(name)

		// IF THIS IS NOT A NAMESPACED REPOSITORY, IT MIGHT BE AN AMBIGUOUS NAME
		if owner == "" && r.Method != "HEAD" {
			// TRY TO FIND THE APPROPRIATE REPOSITORY OWNER USING OUR FALLBACK METHOD
			resolvedOwner, resolveErr := h.resolveRepositoryFallback(username, name)
			if resolveErr == nil && resolvedOwner != "" {
				// WE FOUND AN OWNER - TRY AGAIN WITH THE FULLY QUALIFIED PATH USING USER.OWNER FORMAT
				namespacedPath := "user." + resolvedOwner + "/" + name
				h.log.Printf("Ambiguous repository name detected for blob, trying with explicit namespace: %s", namespacedPath)

				// GET THE APPROPRIATE FILESYSTEM PATH FOR THE RESOLVED REPOSITORY
				resolvedFsPath := h.getRepositoryFSPath(namespacedPath)

				// TRY TO FIND THE BLOB IN THE RESOLVED REPOSITORY
				resolvedLayerLink := filepath.Join(
					h.config.Storage.RootDirectory,
					"repositories",
					resolvedFsPath,
					"_layers",
					"sha256",
					strings.TrimPrefix(digest, "sha256:"),
					"link",
				)

				if _, err := os.Stat(resolvedLayerLink); err == nil {
					// FOUND THE BLOB IN THE RESOLVED REPOSITORY
					// NO NEED TO UPDATE FSPATH OR LAYERLINK SINCE WE'RE GOING TO ACCESS THE BLOB DIRECTLY
					h.log.Printf("Found blob in user's repository using namespace resolution")
				} else {
					h.log.Printf("Blob not found in repository: %s even with namespace resolution", digest)
					http.Error(w, "BLOB NOT FOUND", http.StatusNotFound)
					return
				}
			} else {
				h.log.Printf("Blob not found in repository: %s", digest)
				http.Error(w, "BLOB NOT FOUND", http.StatusNotFound)
				return
			}
		} else {
			h.log.Printf("Blob not found in repository: %s", digest)
			http.Error(w, "BLOB NOT FOUND", http.StatusNotFound)
			return
		}
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

	// GET THE APPROPRIATE FILESYSTEM PATH
	fsPath := h.getRepositoryFSPath(name)

	// CHECK IF BLOB IS REFERENCED
	layerLink := filepath.Join(
		h.config.Storage.RootDirectory,
		"repositories",
		fsPath, // Use the resolved filesystem path
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

	// GET THE APPROPRIATE FILESYSTEM PATH BASED ON WHETHER THIS IS A NAMESPACED REPOSITORY
	fsPath := h.getRepositoryFSPath(name)
	h.log.Printf("Resolving manifest path: original path=%s, filesystem path=%s", name, fsPath)

	// ENSURE SUBPATH DIRS EXIST
	repoPath := filepath.Join(h.config.Storage.RootDirectory, "repositories", fsPath)
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
			fsPath, // Use the resolved filesystem path
			"_manifests",
			"revisions",
			reference,
		)
	} else {
		// TAG REF - NEED TO RESOLVE TO DIGEST
		tagPath := filepath.Join(
			h.config.Storage.RootDirectory,
			"repositories",
			fsPath, // Use the resolved filesystem path
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
			fsPath, // Use the resolved filesystem path
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
	// GET THE APPROPRIATE FILESYSTEM PATH
	fsPath := h.getRepositoryFSPath(name)

	manifestTagsDir := filepath.Join(
		h.config.Storage.RootDirectory,
		"repositories",
		fsPath, // Use the resolved filesystem path
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

	var manifest utils.ManifestMetadata
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

	// 6. CHECK IF IMAGE METADATA SHOULD BE DELETED
	var count int64
	if err := h.db.Model(&models.UserImage{}).Where("image_id = ?", manifestDigest).Count(&count).Error; err != nil {
		h.log.Printf("Error checking for image references: %v", err)
	}

	// ONLY DELETE THE METADATA IF THERE ARE NO MORE REFERENCES
	if count == 0 {
		return h.repo.DeleteImageMetadata(manifestDigest)
	}

	return nil
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

// PARSES A REPOSITORY PATH THAT MIGHT INCLUDE A USERNAME PREFIX
// RETURNS THE REPOSITORY OWNER (IF SPECIFIED) AND THE ACTUAL REPOSITORY NAME
func (h *RepositoryHandler) parseRepoName(repoPath string) (owner string, repo string) {
	// HANDLE THE CASE WHERE REPOPATH MIGHT BE EMPTY
	if repoPath == "" {
		return "", ""
	}

	// CHECK FOR SPECIAL USER.USERNAME PATTERN WHICH FORCES USERNAME INTERPRETATION (FOR CONFLICT RESOLUTION)
	// FORMAT: USER.USERNAME/REPOSITORY
	if strings.HasPrefix(repoPath, "user.") {
		parts := strings.SplitN(repoPath, "/", 2)
		if len(parts) >= 2 {
			// EXTRACT USERNAME AFTER THE "USER." PREFIX
			potentialUsername := strings.TrimPrefix(parts[0], "user.")
			remainder := parts[1]

			var count int64
			if err := h.db.Model(&models.User{}).Where("username = ?", potentialUsername).Count(&count).Error; err == nil && count > 0 {
				// IT'S A VALID USERNAME WITH THE USER. PREFIX, FORCE NAMESPACED INTERPRETATION
				h.log.Printf("User dot-prefix namespace format used: user.%s/...", potentialUsername)
				return potentialUsername, remainder
			}
		}
	}

	// CHECK IF THE PATH STARTS WITH A VALID USERNAME
	parts := strings.SplitN(repoPath, "/", 2)
	if len(parts) >= 2 {
		potentialUsername := parts[0]
		var count int64
		if err := h.db.Model(&models.User{}).Where("username = ?", potentialUsername).Count(&count).Error; err == nil && count > 0 {
			// IT'S A VALID USERNAME, SO THIS IS A NAMESPACED REPO
			return potentialUsername, parts[1]
		}
	}

	// IF WE GET HERE, EITHER:
	// 1. THE PATH DOESN'T CONTAIN SLASHES
	// 2. THE FIRST SEGMENT ISN'T A VALID USERNAME
	// 3. THERE WAS A DB ERROR CHECKING THE USERNAME
	// IN ALL CASES, WE TREAT IT AS A NON-NAMESPACED REPOSITORY
	return "", repoPath
}

// RETURNS THE APPROPRIATE FILESYSTEM PATH FOR A REPOSITORY BASED ON
// WHETHER IT'S NAMESPACED AND WHO THE CURRENT USER IS.
// FOR NAMESPACED REPOS (USERA/REPO), THIS WILL RETURN THE PATH WITH THE NAMESPACE REMOVED
// FOR NON-NAMESPACED REPOS, IT RETURNS THE PATH AS-IS
func (h *RepositoryHandler) getRepositoryFSPath(repoPath string) string {
	owner, repo := h.parseRepoName(repoPath)

	// IF WE HAVE A USERNAME NAMESPACE, WE NEED TO CHECK THE DATABASE TO SEE
	// IF THE CURRENT USER HAS A REPOSITORY WITH THE SAME BASE NAME (NO NAMESPACE)
	if owner != "" {
		// THIS MEANS THE IMAGE PATH IS USERNAME/REPO - WE SHOULD LOOK FOR
		// IMAGES STORED IN THE FILESYSTEM AT REPO/, NOT USERNAME/REPO/
		h.log.Printf("Namespaced repository detected: %s/%s, using path: %s", owner, repo, repo)
		return repo
	}

	// NO USERNAME NAMESPACE, USE THE PATH AS-IS
	return repoPath
}

// CHECKS IF A USER CAN PUSH TO A REPOSITORY
// FOR PUSH OPERATIONS, THE RULES ARE:
// 1. THE SPECIAL USER.USERNAME FORMAT IS NOT ALLOWED FOR PUSH OPERATIONS (ONLY FOR PULLS)
// 2. IF A NAMESPACED REPOSITORY IS SPECIFIED (USERA/REPO), THE USER MUST BE USERA
// 3. IF A NON-NAMESPACED REPOSITORY, THE USER CAN PUSH TO IT (WILL CREATE IT IF NEEDED)
// 4. SPECIAL CASE: IF A REPOSITORY WAS CREATED WITH FULL PATH BEFORE A CONFLICTING USERNAME EXISTED
func (h *RepositoryHandler) hasRepositoryPushAccess(username string, repoPath string) (bool, error) {
	// THE USER.USERNAME FORMAT IS NOT ALLOWED FOR PUSH OPERATIONS TO PREVENT CONFUSION
	if strings.HasPrefix(repoPath, "user.") {
		h.log.Printf("User %s attempted to push using the user.username format, which is not allowed: %s", username, repoPath)
		return false, fmt.Errorf("the user.username format is not allowed for push operations, only for pulls")
	}

	// CHECK IF THE USER OWNS A REPOSITORY WITH THE EXACT FULL PATH
	// THIS HANDLES THE EDGE CASE WHERE A REPO LIKE "MYIMAGES/NGINX" WAS CREATED
	// BEFORE A USER NAMED "MYIMAGES" EXISTED
	var count int64
	if err := h.db.Model(&models.UserImage{}).
		Where("username = ? AND name = ?", username, repoPath).
		Count(&count).Error; err != nil {
		return false, fmt.Errorf("error checking repository ownership (full path): %w", err)
	}

	if count > 0 {
		h.log.Printf("User %s has direct push access to the full path: %s", username, repoPath)
		return true, nil // User owns a repository with the full path
	}

	// PROCEED WITH NAMESPACE PARSING (WILL HANDLE USER.USERNAME FORMAT IF PRESENT)
	targetOwner, _ := h.parseRepoName(repoPath)

	// IF NAMESPACED, ONLY THE OWNER CAN PUSH
	if targetOwner != "" {
		return username == targetOwner, nil
	}

	// FOR NON-NAMESPACED REPOSITORIES, ANY AUTHENTICATED USER CAN PUSH
	return true, nil
}

// RESOLVEREPOSITORYFALLBACK FINDS THE APPROPRIATE REPOSITORY WHEN THERE'S A CONFLICT WITH MULTIPLE USERS
// HAVING REPOSITORIES WITH THE SAME NAME. IT PRIORITIZES IN THIS ORDER:
// 1. USER'S OWN REPOSITORY WITH THE NAME
// 2. MOST RECENTLY UPDATED PUBLIC REPOSITORY WITH THE NAME (FROM OTHER USERS)
func (h *RepositoryHandler) resolveRepositoryFallback(username string, repoName string) (ownerUsername string, err error) {
	// FIRST CHECK IF THE USER HAS THEIR OWN REPOSITORY WITH THIS NAME
	var count int64
	if err := h.db.Model(&models.UserImage{}).
		Where("username = ? AND name = ?", username, repoName).
		Count(&count).Error; err != nil {
		return "", fmt.Errorf("error checking user repository: %w", err)
	}

	if count > 0 {
		// USER HAS THEIR OWN REPOSITORY WITH THIS NAME, PRIORITIZE THAT
		return username, nil
	}

	// FIND THE MOST RECENTLY UPDATED PUBLIC REPOSITORY WITH THIS NAME
	var userImage models.UserImage
	if err := h.db.Model(&models.UserImage{}).
		Where("name = ? AND private = ? AND username != ?", repoName, false, username).
		Order("updated_at DESC").
		First(&userImage).Error; err != nil {
		// IF NO REPOSITORY FOUND, RETURN AN ERROR
		return "", fmt.Errorf("no accessible repository found: %w", err)
	}

	// RETURN THE OWNER OF THE MOST RECENTLY UPDATED REPOSITORY
	return userImage.Username, nil
}

// CHECKS IF A USER HAS ACCESS TO A REPOSITORY USING THE FOLLOWING RULES:
// 1. SPECIAL CASE: IF USING THE USER.USERNAME FORMAT (E.G., USER.USERA/REPO):
//   - FORCE INTERPRETATION AS A NAMESPACED REPOSITORY FOR THE SPECIFIED USER
//
// 2. IF A SPECIFIC NAMESPACE IS PROVIDED (E.G., USERA/REPO):
//   - CHECK IF THE USER HAS DIRECT OWNERSHIP OF THE ENTIRE PATH (LEGACY CASE WHERE REPO WAS CREATED BEFORE USERNAME)
//   - IF LOGGED-IN USER MATCHES THE NAMESPACE, CHECK ALL THEIR REPOS (PRIVATE & PUBLIC)
//   - IF LOGGED-IN USER IS DIFFERENT, CHECK ONLY PUBLIC REPOS OF THE SPECIFIED USER
//
// 3. IF NO NAMESPACE IS SPECIFIED (E.G., JUST REPO):
//   - FIRST CHECK IF THE LOGGED-IN USER HAS THE REPO (REGARDLESS OF VISIBILITY)
//   - IF NOT, CHECK FOR ANY PUBLIC REPO WITH THAT NAME FROM ANY USER
//   - WHEN MULTIPLE PUBLIC REPOSITORIES WITH THE SAME NAME EXIST, PRIORITIZE MOST RECENTLY UPDATED
func (h *RepositoryHandler) hasRepositoryAccess(username string, repoPath string) (bool, error) {
	// CHECK IF THIS IS USING THE SPECIAL USER.USERNAME FORMAT, WHICH FORCES NAMESPACE INTERPRETATION
	// IF IT IS, SKIP THE DIRECT PATH CHECK SINCE THE USER SPECIFICALLY WANTS ANOTHER USER'S REPO
	isForceNamespace := strings.HasPrefix(repoPath, "user.")

	// IF NOT FORCING A NAMESPACE, CHECK FOR DIRECT OWNERSHIP FIRST
	if !isForceNamespace {
		// CHECK IF THE USER OWNS A REPOSITORY WITH THE EXACT FULL PATH
		// THIS HANDLES THE EDGE CASE WHERE A REPO LIKE "MYIMAGES/NGINX" WAS CREATED
		// BEFORE A USER NAMED "MYIMAGES" EXISTED
		var count int64
		if err := h.db.Model(&models.UserImage{}).
			Where("username = ? AND name = ?", username, repoPath).
			Count(&count).Error; err != nil {
			return false, fmt.Errorf("error checking repository ownership (full path): %w", err)
		}

		if count > 0 {
			h.log.Printf("User %s has direct ownership of the full path: %s", username, repoPath)
			return true, nil // User owns a repository with the full path
		}
	}

	// PROCEED WITH NAMESPACE PARSING (WILL HANDLE USER.USERNAME FORMAT IF PRESENT)
	targetOwner, repoName := h.parseRepoName(repoPath)

	// IF A SPECIFIC OWNER IS TARGETED (NAMESPACED REPOSITORY)
	if targetOwner != "" {
		h.log.Printf("Checking access to namespaced repo: %s/%s for user %s", targetOwner, repoName, username)

		// IF THE REQUESTING USER IS THE TARGET OWNER, THEY HAVE ACCESS TO THEIR OWN REPOS
		if username == targetOwner {
			var count int64
			if err := h.db.Model(&models.UserImage{}).
				Where("username = ? AND name = ?", username, repoName).
				Count(&count).Error; err != nil {
				return false, fmt.Errorf("error checking repository ownership: %w", err)
			}
			return count > 0, nil
		}

		// IF DIFFERENT USER, CHECK ONLY PUBLIC REPOSITORIES OF THE TARGET USER
		var count int64
		if err := h.db.Model(&models.UserImage{}).
			Where("username = ? AND name = ? AND private = ?", targetOwner, repoName, false).
			Count(&count).Error; err != nil {
			return false, fmt.Errorf("error checking repository public status: %w", err)
		}

		return count > 0, nil // Access only if the target user's repo is public
	}

	// NO NAMESPACE SPECIFIED, TRY USER'S OWN REPOS FIRST
	var count int64
	if err := h.db.Model(&models.UserImage{}).
		Where("username = ? AND name = ?", username, repoName).
		Count(&count).Error; err != nil {
		return false, fmt.Errorf("error checking repository ownership: %w", err)
	}

	if count > 0 {
		return true, nil // User owns a matching repository
	}

	// FALL BACK TO PUBLIC REPOSITORIES WITH THE SAME NAME FROM ANY USER (MOST RECENTLY UPDATED)
	var userImage models.UserImage
	if err := h.db.Model(&models.UserImage{}).
		Where("name = ? AND private = ?", repoName, false).
		Order("updated_at DESC"). // Prioritize most recently updated repos
		First(&userImage).Error; err != nil {
		// IF NO PUBLIC REPOSITORY IS FOUND, ACCESS DENIED
		return false, nil
	}

	// FOUND A PUBLIC REPOSITORY WITH THIS NAME
	return true, nil
}

func (h *RepositoryHandler) UpdateImageVisibility(w http.ResponseWriter, r *http.Request) {
	username := r.Context().Value(constants.UsernameKey).(string)

	var req struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Private bool   `json:"private"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	h.log.Printf("Updating visibility for repository: id=%s, name=%s, private=%v", req.ID, req.Name, req.Private)

	// FIND ALL USER IMAGES BELONGING TO THIS REPOSITORY FOR THIS USER
	var userImages []models.UserImage
	repoName := req.Name

	// ONLY FALL BACK TO ID IF NAME IS NOT PROVIDED
	if repoName == "" {
		h.log.Printf("Repository name not provided, falling back to ID field")

		// IF NO NAME PROVIDED, TRY TO LOOK UP BY ID FIRST (MIGHT BE DIGEST OR NAME)
		if id, err := strconv.Atoi(req.ID); err == nil {
			// IT'S A NUMERIC ID, FIND THE SINGLE USERIMAGE
			var userImage models.UserImage
			if err := h.db.Where("id = ? AND username = ?", id, username).First(&userImage).Error; err != nil {
				h.log.Printf("Failed to find user image with ID %d: %v", id, err)
				http.Error(w, "Image not found", http.StatusNotFound)
				return
			}
			// GET REPOSITORY NAME FROM THIS IMAGE
			repoName = userImage.Name
			h.log.Printf("Resolved numeric ID %d to repository name: %s", id, repoName)
		} else if strings.HasPrefix(req.ID, "sha256:") {
			// IT'S A DIGEST, FIND ALL IMAGES WITH THIS DIGEST
			var tempImg models.UserImage
			if err := h.db.Where("image_id = ? AND username = ?", req.ID, username).First(&tempImg).Error; err != nil {
				h.log.Printf("Failed to find any user images with digest %s: %v", req.ID, err)
				http.Error(w, "Image not found", http.StatusNotFound)
				return
			}
			repoName = tempImg.Name
			h.log.Printf("Resolved digest %s to repository name: %s", req.ID, repoName)
		} else {
			// ASSUME ID IS THE REPOSITORY NAME
			repoName = req.ID
			h.log.Printf("Using ID field as repository name: %s", repoName)
		}
	} else {
		h.log.Printf("Using provided repository name: %s", repoName)
	}

	// CHECK IF THE REPOSITORY EXISTS - FIRST TRY EXACT NAME MATCH FOR THIS USER
	var count int64
	if err := h.db.Model(&models.UserImage{}).Where("username = ? AND name = ?", username, repoName).Count(&count).Error; err != nil {
		h.log.Printf("Error checking repository existence: %v", err)
		http.Error(w, "Failed to update visibility", http.StatusInternalServerError)
		return
	}

	if count == 0 {
		h.log.Printf("No repository found with name %s for user %s", repoName, username)
		http.Error(w, "Repository not found", http.StatusNotFound)
		return
	}

	// NOW FIND ALL IMAGES IN THIS REPOSITORY FOR THIS USER
	// FOR MULTI-SEGMENT REPOSITORY NAMES, WE NEED AN EXACT MATCH
	if err := h.db.Where("username = ? AND name = ?", username, repoName).Find(&userImages).Error; err != nil {
		h.log.Printf("Failed to find user images for repository %s: %v", repoName, err)
		http.Error(w, "Failed to update visibility", http.StatusInternalServerError)
		return
	}

	if len(userImages) == 0 {
		h.log.Printf("No images found for repository %s", repoName)
		http.Error(w, "No images found", http.StatusNotFound)
		return
	}

	h.log.Printf("Found %d images to update visibility for repository %s", len(userImages), repoName)

	// UPDATE VISIBILITY FOR ALL USER IMAGES IN THIS REPOSITORY
	for _, img := range userImages {
		if err := h.repo.UpdateUserImageVisibility(img.ID, req.Private); err != nil {
			h.log.Printf("Failed to update visibility for image %d: %v", img.ID, err)
			// CONTINUE WITH OTHER IMAGES
		}
	}

	w.WriteHeader(http.StatusOK)
}

func (h *RepositoryHandler) ListGlobalRepositories(w http.ResponseWriter, r *http.Request) {
	username := r.Context().Value(constants.UsernameKey).(string)

	// SEE LIST REPOSITORIES FOR COMMENTS, THIS IS VERY SIMILAR
	publicImages, err := h.repo.ListPublicUserImages()
	if err != nil {
		h.log.Printf("Failed to list public images: %v", err)
		http.Error(w, "Failed to list repositories", http.StatusInternalServerError)
		return
	}

	userImages, err := h.repo.ListUserImages(username)
	if err != nil {
		h.log.Printf("Failed to list user images: %v", err)
		http.Error(w, "Failed to list user repositories", http.StatusInternalServerError)
		return
	}

	allImages := append(publicImages, userImages...)
	imageIDs := make([]string, 0, len(allImages))
	for _, img := range allImages {
		imageIDs = append(imageIDs, img.ImageID)
	}

	idMap := make(map[string]bool)
	uniqueImageIDs := make([]string, 0)
	for _, id := range imageIDs {
		if !idMap[id] {
			idMap[id] = true
			uniqueImageIDs = append(uniqueImageIDs, id)
		}
	}

	var metadataList []*models.ImageMetadata
	if err := h.db.Where("id IN ?", uniqueImageIDs).Find(&metadataList).Error; err != nil {
		h.log.Printf("Failed to get image metadata: %v", err)
		http.Error(w, "Failed to list repositories", http.StatusInternalServerError)
		return
	}

	metadataByID := make(map[string]*models.ImageMetadata)
	for _, md := range metadataList {
		metadataByID[md.ID] = md
	}

	// CREATE A MAP TO ORGANIZE USERIMAGES BY USER+NAME (REPOSITORY KEY)
	// THIS ENSURES EACH USER'S REPOSITORIES ARE KEPT SEPARATE, EVEN IF THEY SHARE CONTENT
	imageViews := make(map[string]*models.ImageMetadataView)

	for _, userImg := range allImages {
		metadata, exists := metadataByID[userImg.ImageID]
		if !exists {
			continue // Skip if metadata not found
		}

		// CREATE A UNIQUE KEY THAT INCLUDES BOTH THE USER AND REPOSITORY NAME
		repoKey := fmt.Sprintf("%s/%s", userImg.Username, userImg.Name)

		view, exists := imageViews[repoKey]
		if !exists {
			// CREATE FULL NAME IN USERNAME/REPO FORMAT FOR UI DISPLAY
			fullName := fmt.Sprintf("%s/%s", userImg.Username, userImg.Name)

			view = &models.ImageMetadataView{
				ID:          userImg.ImageID,
				Name:        userImg.Name,
				Tags:        []string{userImg.Tag},
				Size:        metadata.Size,
				Owner:       userImg.Username,
				Labels:      metadata.Labels,
				Private:     userImg.Private,
				CreatedAt:   metadata.CreatedAt,
				UpdatedAt:   metadata.UpdatedAt,
				FullName:    fullName,
				IsOwnedByMe: userImg.Username == username,
			}
			imageViews[repoKey] = view
		} else {
			tagExists := slices.Contains(view.Tags, userImg.Tag)
			if !tagExists {
				view.Tags = append(view.Tags, userImg.Tag)
			}
		}
	}

	// MAP TO SLICE
	var viewsList []*models.ImageMetadataView
	for _, view := range imageViews {
		viewsList = append(viewsList, view)
	}

	// CALCULATE TOTALS
	var totalSize int64
	for _, img := range metadataList {
		totalSize += img.Size
	}

	response := models.GlobalView{
		TotalImages: int64(len(viewsList)),
		TotalSize:   totalSize,
		Images:      viewsList,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

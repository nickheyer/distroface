package handlers

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/nickheyer/distroface/internal/constants"
	"github.com/nickheyer/distroface/internal/logging"
	"github.com/nickheyer/distroface/internal/models"
	"github.com/nickheyer/distroface/internal/repository"
	"github.com/nickheyer/distroface/internal/utils"
)

type ArtifactHandler struct {
	repo   repository.Repository
	config *models.Config
	log    *logging.LogService
}

func NewArtifactHandler(repo repository.Repository, cfg *models.Config, log *logging.LogService) *ArtifactHandler {
	return &ArtifactHandler{
		repo:   repo,
		config: cfg,
		log:    log,
	}
}

func (h *ArtifactHandler) CreateRepository(w http.ResponseWriter, r *http.Request) {
	username := r.Context().Value(constants.UsernameKey).(string)

	var repo models.ArtifactRepository
	if err := json.NewDecoder(r.Body).Decode(&repo); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	repo.Owner = username
	if err := h.repo.CreateArtifactRepository(&repo); err != nil {
		h.log.Printf("Failed to create repository: %v", err)
		http.Error(w, "Failed to create repository", http.StatusInternalServerError)
		return
	}

	// CREATE REPO SKELETON
	repoPath := filepath.Join(h.config.Storage.RootDirectory, "artifacts", "repos", repo.Name)
	if err := h.ensureDirectoryExists(repoPath); err != nil {
		h.log.Printf("Failed to create repository directories: %v", err)
		http.Error(w, "Failed to initialize repository", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
}

func (h *ArtifactHandler) ListRepositories(w http.ResponseWriter, r *http.Request) {
	username := r.Context().Value(constants.UsernameKey).(string)

	repos, err := h.repo.ListArtifactRepositories(username)
	if err != nil {
		h.log.Printf("Failed to list repositories: %v", err)
		http.Error(w, "Failed to list repositories", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(repos)
}

func (h *ArtifactHandler) InitiateUpload(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	repoName := vars["repo"]
	username := r.Context().Value(constants.UsernameKey).(string)

	// VERIFY REPO ACCESS
	repo, err := h.repo.GetArtifactRepository(repoName)
	if err != nil {
		http.Error(w, "Repository not found", http.StatusNotFound)
		return
	}
	if repo.Owner != username && repo.Private {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	// GENERATE UPLOAD ID
	uploadID := uuid.New().String()

	// GLOBAL DIR EXISTS
	uploadDir := filepath.Join(h.config.Storage.RootDirectory, "artifacts", "_uploads")
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		h.log.Printf("Failed to create upload directory: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// RETURN UPLOAD LOCATION TO THE CLIENT
	w.Header().Set("Location", fmt.Sprintf("/api/v1/artifacts/%s/upload/%s", repoName, uploadID))
	w.Header().Set("Upload-ID", uploadID)
	w.WriteHeader(http.StatusAccepted)
}

func (h *ArtifactHandler) HandleUpload(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	uploadID := vars["uuid"]

	// HANDLE UPLOAD CHUNK
	uploadPath := filepath.Join(h.config.Storage.RootDirectory, "artifacts", "_uploads", uploadID)
	file, err := os.OpenFile(uploadPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		h.log.Printf("Failed to open upload file: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	if _, err := io.Copy(file, r.Body); err != nil {
		h.log.Printf("Failed to write upload data: %v", err)
		http.Error(w, "Failed to process upload", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

func (h *ArtifactHandler) CompleteUpload(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	repoName := vars["repo"]
	uploadID := vars["uuid"]
	version := r.URL.Query().Get("version")
	artifactPath := r.URL.Query().Get("path")
	username := r.Context().Value(constants.UsernameKey).(string)

	if artifactPath == "" {
		artifactPath = utils.SanitizeFilePath(repoName)
	}

	if repoName == "" || version == "" || uploadID == "" {
		http.Error(w, "Required parameters missing", http.StatusBadRequest)
		return
	}

	// CLEAN AND ENCODE PATHS
	version = url.QueryEscape(version)
	artifactPath = url.QueryEscape(artifactPath)

	// BUILD PATHS
	uploadPath := filepath.Join(h.config.Storage.RootDirectory, "artifacts", "_uploads", uploadID)
	if err := h.validatePath(artifactPath, false); err != nil {
		_ = os.Remove(uploadPath)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	finalPath := filepath.Join(h.config.Storage.RootDirectory, "artifacts", "repos", repoName, "versions", version, "files", artifactPath)

	// VERIFY REPO ACCESS
	repo, err := h.repo.GetArtifactRepository(repoName)
	if err != nil {
		_ = os.Remove(uploadPath)
		http.Error(w, "Repository not found", http.StatusNotFound)
		return
	}
	if repo.Owner != username && repo.Private {
		_ = os.Remove(uploadPath)
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	// PARSE PROPS FROM QUERY PARAMS + BODY
	var properties map[string]string
	if propertiesJSON := r.URL.Query().Get("properties"); propertiesJSON != "" {
		if err := json.Unmarshal([]byte(propertiesJSON), &properties); err != nil {
			_ = os.Remove(uploadPath)
			http.Error(w, "Invalid properties format", http.StatusBadRequest)
			return
		}
	}

	if err := json.NewDecoder(r.Body).Decode(&properties); err != nil {
		h.log.Printf("Unable to parse properties from upload body: %v", err)
	}

	// GET ARTIFACT SETTINGS
	settings, err := h.getSettings()
	if err != nil {
		_ = os.Remove(uploadPath)
		h.log.Printf("Failed to get settings: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// VALIDATE PROPS
	for _, required := range settings.Properties.Required {
		if _, exists := properties[required]; !exists {
			_ = os.Remove(uploadPath)
			http.Error(w, fmt.Sprintf("Missing required property: %s", required), http.StatusBadRequest)
			return
		}
	}

	// OPEN UPLOAD
	file, err := os.Open(uploadPath)
	if err != nil {
		_ = os.Remove(uploadPath)
		h.log.Printf("Failed to open upload: %v", err)
		http.Error(w, "Failed to process upload", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	// PEEK FILE TO DETECT MIME
	buffer := make([]byte, 512)
	n, err := file.Read(buffer)
	if err != nil && err != io.EOF {
		_ = os.Remove(uploadPath)
		h.log.Printf("Failed to read file: %v", err)
		http.Error(w, "Failed to process upload", http.StatusInternalServerError)
		return
	}
	mimeType := http.DetectContentType(buffer[:n])

	// VALIDATE FILE SIZE NOW
	fi, err := file.Stat()
	if err != nil {
		_ = os.Remove(uploadPath)
		h.log.Printf("Failed to stat upload: %v", err)
		http.Error(w, "Failed to process upload", http.StatusInternalServerError)
		return
	}

	fileHeader := &multipart.FileHeader{
		Size: fi.Size(),
		Header: textproto.MIMEHeader{
			"Content-Type": []string{mimeType},
		},
	}

	if err := h.validateFileUpload(fileHeader, settings); err != nil {
		_ = os.Remove(uploadPath)
		h.log.Printf("File validation failed: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// ENSURE DIR STRUCTURE
	if err := h.ensureDirectoryExists(filepath.Dir(finalPath)); err != nil {
		_ = os.Remove(uploadPath)
		h.log.Printf("Failed to create artifact directory: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// MOVE FILE TO FINAL LOCATION
	if err := os.Rename(uploadPath, finalPath); err != nil {
		_ = os.Remove(uploadPath)
		h.log.Printf("Failed to move artifact: %v", err)
		http.Error(w, "Failed to save artifact", http.StatusInternalServerError)
		return
	}

	// CREATE ARTIFACT IN DB
	artifact := &models.Artifact{
		RepoID:    repo.ID,
		Name:      filepath.Base(artifactPath),
		Path:      artifactPath,
		Version:   version,
		Size:      fi.Size(),
		MimeType:  mimeType,
		Metadata:  "{}",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := h.repo.CreateArtifact(artifact); err != nil {
		_ = os.Remove(uploadPath)
		h.log.Printf("Failed to create artifact metadata: %v", err)
		http.Error(w, "Failed to save artifact metadata", http.StatusInternalServerError)
		return
	}

	// STORE PROPS
	if len(properties) > 0 {
		if err := h.repo.SetArtifactProperties(artifact.ID, properties); err != nil {
			h.log.Printf("Failed to store properties: %v", err)
			// DONT ROLL BACK
		}
	}

	// OPTION: APPLY RETENTION POLIXY
	if settings.Retention.Enabled {
		go h.applyRetentionPolicy(repo.ID, settings.Retention)
	}

	w.WriteHeader(http.StatusCreated)
}

func (h *ArtifactHandler) DownloadArtifact(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	repoName := vars["repo"]
	version := vars["version"]
	artifactPath := vars["path"]
	username := r.Context().Value(constants.UsernameKey).(string)

	if err := h.validatePath(artifactPath, false); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// VERIFY REPO ACCESS
	repo, err := h.repo.GetArtifactRepository(repoName)
	if err != nil {
		http.Error(w, "Repository not found", http.StatusNotFound)
		return
	}

	if repo.Owner != username && repo.Private {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	filePath := filepath.Join(h.config.Storage.RootDirectory, "artifacts", "repos", repoName, "versions", version, "files", artifactPath)

	// VERIFY ARTIFACT ACTUALLY EXISTS
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		http.Error(w, "Artifact not found", http.StatusNotFound)
		return
	}

	// SERVE IT
	http.ServeFile(w, r, filePath)
}

func (h *ArtifactHandler) QueryDownloadArtifacts(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	repoName := vars["repo"]
	username := r.Context().Value(constants.UsernameKey).(string)

	// GET REPO ACCESS
	repo, err := h.repo.GetArtifactRepository(repoName)
	if err != nil {
		http.Error(w, "Repository not found", http.StatusNotFound)
		return
	}
	if repo.Owner != username && repo.Private {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	// PARSE QUERY PARAMS
	queryParams := r.URL.Query()

	// BUILD SEARCH CRITERIA
	criteria := models.ArtifactSearchCriteria{
		RepoID:     utils.IntPtr(repo.ID),
		Properties: make(map[string]string),
	}

	// HANDLE SPECIAL PARAMS
	if name := queryParams.Get("name"); name != "" {
		criteria.Name = utils.StringPtr(name)
	}
	if version := queryParams.Get("version"); version != "" {
		criteria.Version = utils.StringPtr(version)
	}
	if path := queryParams.Get("path"); path != "" {
		criteria.Path = utils.StringPtr(path)
	}
	if maxResults := queryParams.Get("num"); maxResults != "" {
		if n, err := strconv.Atoi(maxResults); err == nil && n > 0 {
			criteria.Limit = n
		}
	} else {
		criteria.Limit = 1 // DEFAULT TO 1
	}

	// SET SORTING
	criteria.Sort = "created_at" // DEFAULT SORT
	if sort := queryParams.Get("sort"); sort != "" {
		criteria.Sort = sort
	}
	criteria.Order = "DESC" // DEFAULT ORDER
	if order := queryParams.Get("order"); order != "" {
		criteria.Order = order
	}

	// OUTPUT FORMAT (FILE, ZIP, OR TAR.GZ)
	forceArchive := queryParams.Get("archive") != "" // IF ARCHIVE PARAM EXISTS, FORCE ARCHIVE
	archiveFormat := strings.ToLower(queryParams.Get("format"))
	if archiveFormat != "zip" && archiveFormat != "tar.gz" {
		archiveFormat = "zip" // DEFAULT TO ZIP
	}

	// ADD PROPERTY FILTERS FROM REMAINING QUERY PARAMS
	for key, values := range queryParams {
		switch key {
		case "num", "archive", "format", "name", "version", "path", "sort", "order":
			continue // SKIP SPECIAL PARAMS
		default:
			if len(values) > 0 {
				criteria.Properties[key] = values[0]
			}
		}
	}

	// SEARCH ARTIFACTS
	artifacts, err := h.repo.SearchArtifacts(criteria)
	if err != nil {
		h.log.Printf("Failed to search artifacts: %v", err)
		http.Error(w, "Failed to search artifacts", http.StatusInternalServerError)
		return
	}

	if len(artifacts) == 0 {
		http.Error(w, "No matching artifacts found", http.StatusNotFound)
		return
	}

	// IF SINGLE FILE AND NO ARCHIVE FORCED
	if len(artifacts) == 1 && !forceArchive {
		artifact := artifacts[0]
		filePath := filepath.Join(
			h.config.Storage.RootDirectory,
			"artifacts",
			"repos",
			repoName,
			"versions",
			artifact.Version,
			"files",
			artifact.Path,
		)
		http.ServeFile(w, r, filePath)
		return
	}

	// CREATE TMP DIR FOR ARCHIVE
	tempDir, err := os.MkdirTemp("", "artifact-download-*")
	if err != nil {
		h.log.Printf("Failed to create temp directory: %v", err)
		http.Error(w, "Failed to prepare download", http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(tempDir)

	// COPY FILES TO TMP DIR
	for _, artifact := range artifacts {
		srcPath := filepath.Join(
			h.config.Storage.RootDirectory,
			"artifacts",
			"repos",
			repoName,
			"versions",
			artifact.Version,
			"files",
			artifact.Path,
		)
		destPath := filepath.Join(tempDir, fmt.Sprintf("%s-%s-%s",
			artifact.Name,
			artifact.Version,
			filepath.Base(artifact.Path)))

		if err := h.copyFile(srcPath, destPath); err != nil {
			h.log.Printf("Failed to copy file: %v", err)
			http.Error(w, "Failed to prepare download", http.StatusInternalServerError)
			return
		}
	}

	// CREATE ARCHIVE
	var archivePath string
	var mimeType string
	if archiveFormat == "zip" {
		archivePath = filepath.Join(tempDir, "artifacts.zip")
		mimeType = "application/zip"
		if err := h.createZipArchive(tempDir, archivePath); err != nil {
			h.log.Printf("Failed to create zip archive: %v", err)
			http.Error(w, "Failed to create archive", http.StatusInternalServerError)
			return
		}
	} else {
		archivePath = filepath.Join(tempDir, "artifacts.tar.gz")
		mimeType = "application/gzip"
		if err := h.createTarGzArchive(tempDir, archivePath); err != nil {
			h.log.Printf("Failed to create tar.gz archive: %v", err)
			http.Error(w, "Failed to create archive", http.StatusInternalServerError)
			return
		}
	}

	// SET HEADERS AND SERVE
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filepath.Base(archivePath)))
	http.ServeFile(w, r, archivePath)
}

func (h *ArtifactHandler) ListVersions(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	repoName := vars["repo"]
	username := r.Context().Value(constants.UsernameKey).(string)

	// VERIFY REPO ACCESS
	repo, err := h.repo.GetArtifactRepository(repoName)
	if err != nil {
		http.Error(w, "Repository not found", http.StatusNotFound)
		return
	}

	if repo.Owner != username && repo.Private {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	artifacts, err := h.repo.ListArtifacts(repo.ID)
	if err != nil {
		h.log.Printf("Failed to list artifacts: %v", err)
		http.Error(w, "Failed to list versions", http.StatusInternalServerError)
		return
	}

	// GROUP ARTIFACTS BY VERSION
	versions := make(map[string][]models.Artifact)
	for _, artifact := range artifacts {
		versions[artifact.Version] = append(versions[artifact.Version], artifact)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(versions)
}

func (h *ArtifactHandler) UpdateMetadata(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	repoName := vars["repo"]
	artifactID := vars["id"]
	username := r.Context().Value(constants.UsernameKey).(string)

	// VERIFY REPO ACCESS
	repo, err := h.repo.GetArtifactRepository(repoName)
	if err != nil {
		http.Error(w, "Repository not found", http.StatusNotFound)
		return
	}

	if repo.Owner != username && repo.Private {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	var metadata map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&metadata); err != nil {
		http.Error(w, "Invalid metadata", http.StatusBadRequest)
		return
	}

	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		http.Error(w, "Invalid metadata format", http.StatusBadRequest)
		return
	}

	if err := h.repo.UpdateArtifactMetadata(artifactID, string(metadataJSON)); err != nil {
		h.log.Printf("Failed to update metadata: %v", err)
		http.Error(w, "Failed to update metadata", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *ArtifactHandler) UpdateProperties(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	repoName := vars["repo"]
	artifactID := vars["id"]
	username := r.Context().Value(constants.UsernameKey).(string)

	// VERIFY
	repo, err := h.repo.GetArtifactRepository(repoName)
	if err != nil {
		http.Error(w, "Repository not found", http.StatusNotFound)
		return
	}

	if repo.Owner != username {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	var properties map[string]string
	if err := json.NewDecoder(r.Body).Decode(&properties); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// GET VALIDATION
	settings, err := h.getSettings()
	if err != nil {
		h.log.Printf("Failed to get settings: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// ENSURE REQUIRED
	existingProps, err := h.repo.GetArtifactProperties(artifactID)
	if err != nil {
		h.log.Printf("Failed to get existing properties: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	for _, required := range settings.Properties.Required {
		if _, exists := properties[required]; !exists {
			if _, hadProp := existingProps[required]; hadProp {
				http.Error(w, fmt.Sprintf("Cannot remove required property: %s", required), http.StatusBadRequest)
				return
			}
		}
	}

	if err := h.repo.SetArtifactProperties(artifactID, properties); err != nil {
		h.log.Printf("Failed to update properties: %v", err)
		http.Error(w, "Failed to update properties", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *ArtifactHandler) DeleteArtifact(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	repoName := vars["repo"]
	version := vars["version"]
	artifactPath := vars["path"]
	username := r.Context().Value(constants.UsernameKey).(string)

	if err := h.validatePath(artifactPath, false); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// VERIFY REPO ACCESS
	repo, err := h.repo.GetArtifactRepository(repoName)
	if err != nil {
		http.Error(w, "Repository not found", http.StatusNotFound)
		return
	}

	if repo.Owner != username {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	// DELETE ROW IN DB
	if err := h.repo.DeleteArtifactByPath(repo.ID, version, artifactPath); err != nil {
		h.log.Printf("Failed to delete artifact from db: %v", err)
		http.Error(w, "Failed to delete artifact from db", http.StatusInternalServerError)
		return
	}

	// DELETE FILE
	filePath := filepath.Join(h.config.Storage.RootDirectory, "artifacts", "repos", repoName, "versions", version, "files", artifactPath)
	if err := h.validatePath(filePath, true); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := os.RemoveAll(filePath); err != nil && !os.IsNotExist(err) {
		h.log.Printf("Failed to delete artifact file: %v", err)
		http.Error(w, "Failed to delete artifact", http.StatusInternalServerError)
		return
	}

	// RETURN NO CONTENT STATUS
	w.WriteHeader(http.StatusNoContent)
}

func (h *ArtifactHandler) DeleteRepository(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	repoName := vars["repo"]
	username := r.Context().Value(constants.UsernameKey).(string)

	// VERIFY REPO ACCESS
	repo, err := h.repo.GetArtifactRepository(repoName)
	if err != nil {
		http.Error(w, "Repository not found", http.StatusNotFound)
		return
	}

	if repo.Owner != username {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	// DELETE REPO DIRECTORY
	repoPath := filepath.Join(h.config.Storage.RootDirectory, "artifacts", "repos", repoName)
	if err := h.validatePath(repoPath, true); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := os.RemoveAll(repoPath); err != nil {
		h.log.Printf("Failed to delete repository directory: %v", err)
		http.Error(w, "Failed to delete repository", http.StatusInternalServerError)
		return
	}

	// DELETE METADATA
	if err := h.repo.DeleteArtifactRepository(repoName); err != nil {
		h.log.Printf("Failed to delete repository metadata: %v", err)
		http.Error(w, "Failed to delete repository metadata", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *ArtifactHandler) SearchArtifacts(w http.ResponseWriter, r *http.Request) {
	// PARSE TO CRITERIA STRUCT
	var req models.ArtifactSearchCriteria
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// GET SETTINGS
	settings, err := h.getSettings()
	if err != nil {
		h.log.Printf("Failed to get settings: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// BUILD SEARCH CRITERIA WITH DEFAULTS FROM SETTINGS
	criteria := models.ArtifactSearchCriteria{
		RepoID:     req.RepoID,
		Name:       req.Name,
		Version:    req.Version,
		Path:       req.Path,
		Properties: req.Properties,
		Sort:       settings.Search.DefaultSort,
		Order:      settings.Search.DefaultOrder,
		Limit:      settings.Search.MaxResults,
	}

	// OVERRIDE DEFAULTS IF PROVIDED IN REQUEST
	if req.Sort != "" {
		criteria.Sort = req.Sort
	}
	if req.Order != "" {
		criteria.Order = req.Order
	}
	if req.Limit > 0 && req.Limit <= settings.Search.MaxResults {
		criteria.Limit = req.Limit
	}

	// VALIDATE SORT FIELD
	validSortFields := map[string]bool{
		"name":       true,
		"version":    true,
		"path":       true,
		"size":       true,
		"created_at": true,
		"updated_at": true,
	}
	if !validSortFields[criteria.Sort] {
		http.Error(w, fmt.Sprintf("Invalid sort field: %s", criteria.Sort), http.StatusBadRequest)
		return
	}

	// VALIDATE ORDER
	criteria.Order = strings.ToUpper(criteria.Order)
	if criteria.Order != "ASC" && criteria.Order != "DESC" {
		criteria.Order = settings.Search.DefaultOrder
	}

	// PERFORM SEARCH
	results, err := h.repo.SearchArtifacts(criteria)
	if err != nil {
		h.log.Printf("Search failed: %v", err)
		http.Error(w, "Search failed", http.StatusInternalServerError)
		return
	}

	// BUILD RESPONSE
	type SearchResponse struct {
		Results []models.Artifact `json:"results"`
		Total   int               `json:"total"`
		Limit   int               `json:"limit"`
		Sort    string            `json:"sort"`
		Order   string            `json:"order"`
	}

	response := SearchResponse{
		Results: results,
		Total:   len(results),
		Limit:   criteria.Limit,
		Sort:    criteria.Sort,
		Order:   criteria.Order,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.log.Printf("Failed to encode response: %v", err)
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}
}

func (h *ArtifactHandler) RenameArtifact(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	repoName := vars["repo"]
	artifactID := vars["id"]
	username := r.Context().Value(constants.UsernameKey).(string)

	var updateReq struct {
		Name    string `json:"name"`
		Path    string `json:"path"` // OPTIONAL
		Version string `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&updateReq); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// VALIDATE PATHS
	if err := h.validatePath(updateReq.Path, false); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// CHECK ACCESS
	repo, err := h.repo.GetArtifactRepository(repoName)
	if err != nil {
		http.Error(w, "Repository not found", http.StatusNotFound)
		return
	}
	if repo.Owner != username {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	// GET ARTIFACT
	artifact, err := h.repo.GetArtifact(artifactID)
	if err != nil {
		http.Error(w, "Failed to get artifact", http.StatusInternalServerError)
		return
	}

	// ESCAPE VERSION
	updateReq.Version = url.QueryEscape(updateReq.Version)

	// IF NO PATH, MAINTAIN DIR STRUCTURE AND UPDATE FILENAME
	if updateReq.Path == "" {
		dir := filepath.Dir(artifact.Path)
		if dir == "." {
			updateReq.Path = updateReq.Name
		} else {
			updateReq.Path = filepath.Join(dir, updateReq.Name)
		}
	}

	// BUILD FULL PATH
	oldPath := filepath.Join(h.config.Storage.RootDirectory, "artifacts", "repos", repoName, "versions", artifact.Version, "files", artifact.Path)
	newPath := filepath.Join(h.config.Storage.RootDirectory, "artifacts", "repos", repoName, "versions", updateReq.Version, "files", updateReq.Path)

	// NEW DIR
	if err := os.MkdirAll(filepath.Dir(newPath), 0755); err != nil {
		http.Error(w, "Failed to create directories", http.StatusInternalServerError)
		return
	}

	// MOVE FILE
	if err := os.Rename(oldPath, newPath); err != nil {
		http.Error(w, "Failed to move file", http.StatusInternalServerError)
		return
	}

	// UPDATE DB
	if err := h.repo.UpdateArtifactPath(artifactID, updateReq.Name, updateReq.Path, updateReq.Version); err != nil {
		// TRY TO MOVE BACK ON FAILURE
		os.Rename(newPath, oldPath)
		http.Error(w, "Failed to update database", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// PRIVATE METHODS

func (h *ArtifactHandler) createZipArchive(sourceDir, destPath string) error {
	zipfile, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer zipfile.Close()

	archive := zip.NewWriter(zipfile)
	defer archive.Close()

	return filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// SKIP THE ZIP FILE ITSELF
		if path == destPath {
			return nil
		}

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}

		// SET RELATIVE PATH
		header.Name, err = filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}

		if info.IsDir() {
			header.Name += "/"
		} else {
			header.Method = zip.Deflate
		}

		writer, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		_, err = io.Copy(writer, file)
		return err
	})
}

func (h *ArtifactHandler) createTarGzArchive(sourceDir, destPath string) error {
	file, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer file.Close()

	gzWriter := gzip.NewWriter(file)
	defer gzWriter.Close()

	tarWriter := tar.NewWriter(gzWriter)
	defer tarWriter.Close()

	return filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// SKIP THE ARCHIVE FILE ITSELF
		if path == destPath {
			return nil
		}

		header, err := tar.FileInfoHeader(info, info.Name())
		if err != nil {
			return err
		}

		// SET RELATIVE PATH
		header.Name, err = filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}

		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		_, err = io.Copy(tarWriter, file)
		return err
	})
}

func (h *ArtifactHandler) copyFile(src, dst string) error {
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()

	destination, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destination.Close()

	_, err = io.Copy(destination, source)
	return err
}

func (h *ArtifactHandler) validatePath(path string, allowAbs bool) error {
	cleanPath := filepath.Clean(path)

	if strings.Contains(cleanPath, "..") {
		return fmt.Errorf("path cannot contain parent directory references")
	}

	if !allowAbs && filepath.IsAbs(cleanPath) {
		return fmt.Errorf("absolute paths not allowed")
	}

	if len(cleanPath) > 255 {
		return fmt.Errorf("path too long (max 255 characters)")
	}

	return nil
}

func (h *ArtifactHandler) validateFileUpload(file *multipart.FileHeader, settings *models.ArtifactSettings) error {
	maxSize := settings.Storage.MaxFileSize * 1024 * 1024
	if file.Size > maxSize {
		return fmt.Errorf("file size (%v) exceeds maximum size of %v", utils.FormatSize(file.Size), utils.FormatSize(maxSize))
	}

	return nil
}

func (h *ArtifactHandler) ensureDirectoryExists(path string) error {
	return os.MkdirAll(path, 0755)
}

func (h *ArtifactHandler) applyRetentionPolicy(repoID int, retention models.RetentionPolicy) {
	artifacts, err := h.repo.ListArtifacts(repoID)
	if err != nil {
		h.log.Printf("Failed to list artifacts for retention: %v", err)
		return
	}

	// GROUP ARTIFACTS
	groups := make(map[string][]models.Artifact)
	for _, artifact := range artifacts {
		props, err := h.repo.GetArtifactProperties(artifact.ID)
		if err != nil {
			h.log.Printf("Failed to get properties for artifact %s: %v", artifact.ID, err)
			continue
		}

		// CREATE PROP KEY (LIKE JENKINS...)
		key := createGroupKey(props)
		groups[key] = append(groups[key], artifact)
	}

	// APPLY RETENTION
	for _, group := range groups {
		// SORT BY: TODO - USE OUR SORT BY SETTING
		sort.Slice(group, func(i, j int) bool {
			return group[i].CreatedAt.After(group[j].CreatedAt)
		})

		// KEEP LATEST UNLESS CONFIGURED
		start := 0
		if retention.ExcludeLatest && len(group) > 0 {
			start = 1
		}

		// DELETE EXCESS
		for i := start + retention.MaxVersions; i < len(group); i++ {
			artifact := group[i]

			// CHECK AGE
			if retention.MaxAge > 0 {
				age := time.Since(artifact.CreatedAt)
				if age.Hours() < float64(retention.MaxAge*24) {
					continue
				}
			}

			// DELETE ARTIFACTS FROM DB
			if err := h.repo.DeleteArtifact(artifact.RepoID, artifact.Version, artifact.ID); err != nil {
				h.log.Printf("Failed to delete artifact %s during retention: %v", artifact.ID, err)
			}

			// DELETE ARTIFACTS FROM FILESYSTEM
			repo, _ := h.repo.GetArtifactRepositoryByID(fmt.Sprintf("%v", artifact.RepoID))
			versionPath := filepath.Join(
				h.config.Storage.RootDirectory,
				"artifacts",
				"repos",
				repo.Name,
				"versions",
				artifact.Version,
			)

			if err := os.RemoveAll(versionPath); err != nil && !os.IsNotExist(err) {
				h.log.Printf("Failed to prune version path: %v", err)
				return
			}
		}
	}
}

func (h *ArtifactHandler) getSettings() (*models.ArtifactSettings, error) {
	settings, err := utils.GetSettings[*models.ArtifactSettings](h.repo, "artifacts")
	if err != nil {
		return nil, err
	}

	return settings, nil
}

func createGroupKey(properties map[string]string) string {
	var parts []string
	for _, key := range []string{"branch", "buildType"} {
		if val, ok := properties[key]; ok {
			parts = append(parts, val)
		}
	}
	return strings.Join(parts, "-")
}

package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/nickheyer/distroface/internal/config"
	"github.com/nickheyer/distroface/internal/constants"
	"github.com/nickheyer/distroface/internal/models"
	"github.com/nickheyer/distroface/internal/repository"
)

type ArtifactHandler struct {
	repo   repository.Repository
	config *config.Config
	logger *log.Logger
}

func NewArtifactHandler(repo repository.Repository, cfg *config.Config) *ArtifactHandler {
	return &ArtifactHandler{
		repo:   repo,
		config: cfg,
		logger: log.New(os.Stdout, "ARTIFACTS: ", log.LstdFlags),
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
		h.logger.Printf("Failed to create repository: %v", err)
		http.Error(w, "Failed to create repository", http.StatusInternalServerError)
		return
	}

	// CREATE REPO SKELETON
	repoPath := filepath.Join(h.config.Storage.RootDirectory, "artifacts", "repos", repo.Name)
	if err := h.ensureDirectoryExists(repoPath); err != nil {
		h.logger.Printf("Failed to create repository directories: %v", err)
		http.Error(w, "Failed to initialize repository", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
}

func (h *ArtifactHandler) ListRepositories(w http.ResponseWriter, r *http.Request) {
	username := r.Context().Value(constants.UsernameKey).(string)

	repos, err := h.repo.ListArtifactRepositories(username)
	if err != nil {
		h.logger.Printf("Failed to list repositories: %v", err)
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

	// CREATE UPLOAD ID AND TMP DIR
	uploadID := uuid.New().String()
	uploadDir := filepath.Join(h.config.Storage.RootDirectory, "artifacts", "_uploads")
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		h.logger.Printf("Failed to create upload directory: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Location", fmt.Sprintf("/api/v1/artifacts/%s/upload/%s", repoName, uploadID))
	w.Header().Set("Upload-ID", uploadID)
	w.WriteHeader(http.StatusAccepted)
}

func (h *ArtifactHandler) HandleUpload(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	repoName := vars["repo"]
	uploadID := vars["uuid"]
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

	// HANDLE UPLOAD CHUNK
	uploadPath := filepath.Join(h.config.Storage.RootDirectory, "artifacts", "_uploads", uploadID)
	file, err := os.OpenFile(uploadPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		h.logger.Printf("Failed to open upload file: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	if _, err := io.Copy(file, r.Body); err != nil {
		h.logger.Printf("Failed to write upload data: %v", err)
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

	if version == "" || artifactPath == "" {
		http.Error(w, "Version and path are required", http.StatusBadRequest)
		return
	}

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

	// PROCESS COMPLETED UPLOAD
	uploadPath := filepath.Join(h.config.Storage.RootDirectory, "artifacts", "_uploads", uploadID)
	finalPath := filepath.Join(h.config.Storage.RootDirectory, "artifacts", "repos", repoName, "versions", version, "files", artifactPath)

	// ENSURE TARGET DIR EXISTS
	if err := h.ensureDirectoryExists(filepath.Dir(finalPath)); err != nil {
		h.logger.Printf("Failed to create artifact directory: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// MOVE TO FINAL LOCATION
	if err := os.Rename(uploadPath, finalPath); err != nil {
		h.logger.Printf("Failed to move artifact: %v", err)
		http.Error(w, "Failed to save artifact", http.StatusInternalServerError)
		return
	}

	// GENERATE ARTIFACT METADATA
	fileInfo, err := os.Stat(finalPath)
	if err != nil {
		h.logger.Printf("Failed to get artifact info: %v", err)
		http.Error(w, "Failed to process artifact", http.StatusInternalServerError)
		return
	}

	artifact := &models.Artifact{
		RepoID:    repo.ID,
		Name:      filepath.Base(artifactPath),
		Version:   version,
		Size:      fileInfo.Size(),
		MimeType:  http.DetectContentType([]byte{}),
		Metadata:  "{}", // DEFAULT EMPTY
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := h.repo.CreateArtifact(artifact); err != nil {
		h.logger.Printf("Failed to create artifact metadata: %v", err)
		http.Error(w, "Failed to save artifact metadata", http.StatusInternalServerError)
		return
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
		h.logger.Printf("Failed to list artifacts: %v", err)
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
		h.logger.Printf("Failed to update metadata: %v", err)
		http.Error(w, "Failed to update metadata", http.StatusInternalServerError)
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

	// DELETE FILE
	filePath := filepath.Join(h.config.Storage.RootDirectory, "artifacts", "repos", repoName, "versions", version, "files", artifactPath)
	if err := h.validatePath(filePath, true); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		h.logger.Printf("Failed to delete artifact file: %v", err)
		http.Error(w, "Failed to delete artifact", http.StatusInternalServerError)
		return
	}

	// DELETE METADATA
	if err := h.repo.DeleteArtifact(repo.ID, version, artifactPath); err != nil {
		h.logger.Printf("Failed to delete artifact metadata: %v", err)
		http.Error(w, "Failed to delete artifact metadata", http.StatusInternalServerError)
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
		h.logger.Printf("Failed to delete repository directory: %v", err)
		http.Error(w, "Failed to delete repository", http.StatusInternalServerError)
		return
	}

	// DELETE METADATA
	if err := h.repo.DeleteArtifactRepository(repoName); err != nil {
		h.logger.Printf("Failed to delete repository metadata: %v", err)
		http.Error(w, "Failed to delete repository metadata", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *ArtifactHandler) SearchArtifacts(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	username := r.Context().Value(constants.UsernameKey).(string)

	artifacts, err := h.repo.SearchArtifacts(query, username)
	if err != nil {
		h.logger.Printf("Failed to search artifacts: %v", err)
		http.Error(w, "Failed to search artifacts", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(artifacts)
}

func (h *ArtifactHandler) validatePath(path string, allowAbs bool) error {
	// NO PATH TRAVERSAL
	if strings.Contains(path, "..") {
		return fmt.Errorf("invalid path: contains parent directory reference")
	}

	// RELATIVE ONLY
	if !allowAbs && filepath.IsAbs(path) {
		return fmt.Errorf("invalid path: absolute paths not allowed")
	}

	return nil
}

func (h *ArtifactHandler) ensureDirectoryExists(path string) error {
	return os.MkdirAll(path, 0755)
}

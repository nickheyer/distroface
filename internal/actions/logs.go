package actions

import (
	"bytes"
	"context"
	"io"

	"github.com/nickheyer/distroface/internal/db"
)

// Concatenates chunks into one blob and drops the rows
func (m *Manager) compactJobLogs(ctx context.Context, job *db.ActionsJob) {
	chunks, err := m.store.ListActionsJobLogs(ctx, job.ID, 0)
	if err != nil || len(chunks) == 0 {
		return
	}
	blobs := m.artifacts.Blobs()
	uploadID, err := blobs.InitiateUpload()
	if err != nil {
		m.log.Warn("actions: compact logs %s: %v", job.ID, err)
		return
	}
	for _, c := range chunks {
		if _, err := blobs.AppendChunk(uploadID, bytes.NewReader(c.Data)); err != nil {
			_ = blobs.CancelUpload(uploadID)
			m.log.Warn("actions: compact logs %s: %v", job.ID, err)
			return
		}
	}
	digest, _, _, err := blobs.CompleteUpload(uploadID)
	if err != nil {
		m.log.Warn("actions: compact logs %s: %v", job.ID, err)
		return
	}
	if err := m.store.UpdateActionsJobColumns(ctx, job.ID, map[string]any{"log_digest": digest, "log_chunks": len(chunks)}); err != nil {
		return
	}
	job.LogDigest = digest
	if err := m.store.DeleteActionsJobLogs(ctx, job.ID); err != nil {
		m.log.Warn("actions: drop log chunks %s: %v", job.ID, err)
	}
}

// Removes chunks and the compacted blob when nothing else shares it
func (m *Manager) dropJobLogs(ctx context.Context, job *db.ActionsJob) {
	_ = m.store.DeleteActionsJobLogs(ctx, job.ID)
	if job.LogDigest == "" {
		return
	}
	digest := job.LogDigest
	job.LogDigest = ""
	_ = m.store.UpdateActionsJobColumns(ctx, job.ID, map[string]any{"log_digest": "", "log_chunks": 0})
	if n, _ := m.store.CountActionsJobsByLogDigest(ctx, digest); n > 0 {
		return
	}
	if n, _ := m.store.CountArtifactsByDigest(ctx, digest); n > 0 {
		return
	}
	if err := m.artifacts.Blobs().DeleteBlob(digest); err != nil {
		m.log.Warn("actions: delete log blob %s: %v", digest, err)
	}
}

// ReadCompactedLog returns the whole log of a finished job
func (m *Manager) ReadCompactedLog(job *db.ActionsJob) ([]byte, error) {
	if job.LogDigest == "" {
		return nil, nil
	}
	f, _, err := m.artifacts.Blobs().OpenBlob(job.LogDigest)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// LogChunks returns stored chunks from an index onward
func (m *Manager) LogChunks(ctx context.Context, jobID string, from int) ([]*db.ActionsJobLog, error) {
	return m.store.ListActionsJobLogs(ctx, jobID, from)
}

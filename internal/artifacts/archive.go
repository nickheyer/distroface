package artifacts

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"path"

	storage "github.com/nickheyer/distroface/internal/db"
)

// Archive formats for query downloads
const (
	FormatZip   = "zip"
	FormatTarGz = "tar.gz"
)

// Unknown formats coerce to zip like v1
func NormalizeFormat(format string) string {
	if format == FormatTarGz {
		return FormatTarGz
	}
	return FormatZip
}

// Streams blobs into w as zip or tar.gz
func (m *Manager) WriteArchive(w io.Writer, artifacts []*storage.Artifact, format string, flat bool) error {
	if NormalizeFormat(format) == FormatTarGz {
		return m.writeTarGz(w, artifacts, flat)
	}
	return m.writeZip(w, artifacts, flat)
}

// Flat uses basename, nested uses version slash path like v1
func entryName(a *storage.Artifact, flat bool) string {
	if flat {
		return path.Base(a.Path)
	}
	return path.Join(a.Version, a.Path)
}

func (m *Manager) writeZip(w io.Writer, artifacts []*storage.Artifact, flat bool) error {
	zw := zip.NewWriter(w)
	for _, a := range artifacts {
		f, info, err := m.blobs.OpenBlob(a.Digest)
		if err != nil {
			return fmt.Errorf("blob for %s: %w", a.Path, err)
		}
		hdr := &zip.FileHeader{
			Name:     entryName(a, flat),
			Method:   zip.Deflate,
			Modified: info.ModTime(),
		}
		entry, err := zw.CreateHeader(hdr)
		if err == nil {
			_, err = io.Copy(entry, f)
		}
		f.Close()
		if err != nil {
			return err
		}
	}
	return zw.Close()
}

func (m *Manager) writeTarGz(w io.Writer, artifacts []*storage.Artifact, flat bool) error {
	gw := gzip.NewWriter(w)
	tw := tar.NewWriter(gw)
	for _, a := range artifacts {
		f, info, err := m.blobs.OpenBlob(a.Digest)
		if err != nil {
			return fmt.Errorf("blob for %s: %w", a.Path, err)
		}
		hdr := &tar.Header{
			Name:    entryName(a, flat),
			Mode:    0644,
			Size:    a.Size,
			ModTime: info.ModTime(),
		}
		err = tw.WriteHeader(hdr)
		if err == nil {
			_, err = io.Copy(tw, f)
		}
		f.Close()
		if err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gw.Close()
}

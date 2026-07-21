package api

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func unpackZip(zipPath, destPath string, flat bool) error {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer reader.Close()

	for _, f := range reader.File {
		var targetPath string
		if flat {
			targetPath = filepath.Join(destPath, filepath.Base(f.Name))
		} else {
			targetPath = filepath.Join(destPath, f.Name)
		}
		if !strings.HasPrefix(filepath.Clean(targetPath), filepath.Clean(destPath)) {
			continue // Zip slip guard
		}

		if f.FileInfo().IsDir() {
			if !flat {
				_ = os.MkdirAll(targetPath, 0755)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return err
		}
		outFile, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			outFile.Close()
			return err
		}
		_, err = io.Copy(outFile, rc)
		rc.Close()
		outFile.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func unpackTarGz(tarPath, destPath string, flat bool) error {
	file, err := os.Open(tarPath)
	if err != nil {
		return err
	}
	defer file.Close()

	gzr, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		var targetPath string
		if flat {
			targetPath = filepath.Join(destPath, filepath.Base(header.Name))
		} else {
			targetPath = filepath.Join(destPath, header.Name)
		}
		if !strings.HasPrefix(filepath.Clean(targetPath), filepath.Clean(destPath)) {
			continue // Tar slip guard
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if !flat {
				if err := os.MkdirAll(targetPath, 0755); err != nil {
					return err
				}
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return err
			}
			outFile, err := os.Create(targetPath)
			if err != nil {
				return err
			}
			if _, err := io.Copy(outFile, tr); err != nil {
				outFile.Close()
				return err
			}
			outFile.Close()
		}
	}
	return nil
}

func isZipMagic(magic []byte) bool {
	return len(magic) >= 4 && magic[0] == 0x50 && magic[1] == 0x4B && magic[2] == 0x03 && magic[3] == 0x04
}

func isGzipMagic(magic []byte) bool {
	return len(magic) >= 2 && magic[0] == 0x1F && magic[1] == 0x8B
}

func readMagic(path string) []byte {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	magic := make([]byte, 4)
	n, _ := io.ReadFull(file, magic)
	return magic[:n]
}

// Recursive unpacks nested archives in place
func recursivelyUnpack(archivePath, destPath string, flat bool) error {
	magic := readMagic(archivePath)
	switch {
	case isZipMagic(magic):
		if err := unpackZip(archivePath, destPath, flat); err != nil {
			return err
		}
	case isGzipMagic(magic):
		if err := unpackTarGz(archivePath, destPath, flat); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported archive format")
	}

	var files []string
	err := filepath.Walk(destPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return err
	}

	for _, path := range files {
		magic := readMagic(path)
		if !isZipMagic(magic) && !isGzipMagic(magic) {
			continue
		}

		tempDir, err := os.MkdirTemp("", "dfcli-nested-*")
		if err != nil {
			continue
		}
		if err := recursivelyUnpack(path, tempDir, flat); err != nil {
			os.RemoveAll(tempDir)
			continue
		}

		err = filepath.Walk(tempDir, func(srcPath string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			var targetPath string
			if flat {
				targetPath = filepath.Join(destPath, filepath.Base(srcPath))
			} else {
				rel, _ := filepath.Rel(tempDir, srcPath)
				targetPath = filepath.Join(filepath.Dir(path), rel)
			}
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return err
			}
			return moveFile(srcPath, targetPath)
		})
		os.RemoveAll(tempDir)
		if err != nil {
			continue
		}
		os.Remove(path)
	}
	return nil
}

package api

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/viper"
)

func debugf(format string, args ...any) {
	if viper.GetBool("debug") {
		fmt.Fprintf(os.Stderr, format+"\n", args...)
	}
}

func printJSON(v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func formatSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(size)/float64(div), "KMGTPE"[exp])
}

var (
	nonWordPattern    = regexp.MustCompile(`[^\w\-\.]`)
	multiUnderscore   = regexp.MustCompile(`_+`)
	multiSlashPattern = regexp.MustCompile(`/+`)
)

func sanitizeVersion(version string) string {
	return strings.TrimSpace(nonWordPattern.ReplaceAllString(version, "_"))
}

func sanitizeFilePath(path string) string {
	path = filepath.ToSlash(path)
	path = strings.TrimSpace(path)
	path = multiSlashPattern.ReplaceAllString(path, "/")
	path = strings.Trim(path, "/")

	parts := strings.Split(path, "/")
	for i, part := range parts {
		part = nonWordPattern.ReplaceAllString(part, "_")
		part = multiUnderscore.ReplaceAllString(part, "_")
		parts[i] = part
	}
	return strings.Join(parts, "/")
}

// Rename with copy fallback for cross device moves
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Remove(src)
}

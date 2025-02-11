package utils

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

func FormatSize(bytes int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	size := float64(bytes)
	unitIndex := 0

	for size >= 1024 && unitIndex < len(units)-1 {
		size /= 1024
		unitIndex++
	}

	return fmt.Sprintf("%.1f %s", size, units[unitIndex])
}

func SanitizeFilePath(path string) string {
	// CONVERT SLASHES
	path = filepath.ToSlash(path)

	// RM WHITESPACE
	path = strings.TrimSpace(path)

	// RM MULTIPLE SLASHES
	path = regexp.MustCompile(`/+`).ReplaceAllString(path, "/")

	// RM EXTRA SLASHES
	path = strings.Trim(path, "/")

	// ESCAPE SPECIAL CHARACTERS BUT PRESERVE DIRECTORY STRUCTURE
	parts := strings.Split(path, "/")
	for i, part := range parts {

		// REMOVE NON-ALPHANUMERIC CHARS EXCEPT DASH AND UNDERSCORE
		part = regexp.MustCompile(`[^\w\-\.]`).ReplaceAllString(part, "_")

		// COLLAPSE MULTIPLE UNDERSCORES
		part = regexp.MustCompile(`_+`).ReplaceAllString(part, "_")
		parts[i] = part
	}

	return strings.Join(parts, "/")
}

func ValidateFilePath(path string) error {
	// PREVENT PATH TRAVERSAL
	if strings.Contains(path, "..") {
		return fmt.Errorf("path traversal not allowed")
	}

	// CHECK FOR ABSOLUTE PATHS
	if filepath.IsAbs(path) {
		return fmt.Errorf("absolute paths not allowed")
	}

	// CHECK PATH LENGTH
	if len(path) > 255 {
		return fmt.Errorf("path too long (max 255 characters)")
	}

	return nil
}

func SanitizeVersion(version string) string {
	// REMOVE SPECIAL CHARS BUT KEEP COMMON VERSION CHARS
	version = regexp.MustCompile(`[^\w\-\.]`).ReplaceAllString(version, "_")
	return strings.TrimSpace(version)
}

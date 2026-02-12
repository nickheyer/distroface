package utils

import "strings"

func SplitRepoName(fullName string) (namespace, name string) {
	parts := strings.SplitN(fullName, "/", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], parts[1]
}

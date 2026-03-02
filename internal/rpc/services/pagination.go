package services

import (
	"encoding/base64"
	"strconv"
)

const defaultPageSize = 20
const maxPageSize = 100

func parsePagination(pageSize int32, pageToken string) (limit, offset int) {
	limit = defaultPageSize
	if pageSize > 0 {
		limit = int(pageSize)
	}
	if limit > maxPageSize {
		limit = maxPageSize
	}

	if pageToken != "" {
		decoded, err := base64.StdEncoding.DecodeString(pageToken)
		if err == nil {
			if v, err := strconv.Atoi(string(decoded)); err == nil && v >= 0 {
				offset = v
			}
		}
	}
	return
}

func nextPageToken(offset, limit int, total int64) string {
	next := offset + limit
	if int64(next) >= total {
		return ""
	}
	return base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(next)))
}

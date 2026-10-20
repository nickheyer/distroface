package gitrepo

import (
	"bufio"
	"bytes"
	"regexp"
	"strconv"
	"strings"
)

// Parsed git lfs pointer file
type LFSPointer struct {
	OID  string
	Size int64
}

var lfsOIDPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// Reports whether bytes are a git lfs pointer
func isLFSPointer(buf []byte) bool {
	return ParseLFSPointer(buf) != nil
}

// Parses a pointer file, nil when the bytes are not one
func ParseLFSPointer(buf []byte) *LFSPointer {
	if len(buf) > 1024 || !bytes.HasPrefix(buf, []byte("version https://git-lfs.github.com/spec/v1")) {
		return nil
	}
	ptr := &LFSPointer{}
	sc := bufio.NewScanner(bytes.NewReader(buf))
	for sc.Scan() {
		key, value, ok := strings.Cut(sc.Text(), " ")
		if !ok {
			continue
		}
		switch key {
		case "oid":
			hex := strings.TrimPrefix(value, "sha256:")
			if !lfsOIDPattern.MatchString(hex) {
				return nil
			}
			ptr.OID = hex
		case "size":
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return nil
			}
			ptr.Size = n
		}
	}
	if ptr.OID == "" {
		return nil
	}
	return ptr
}

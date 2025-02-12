package utils

import (
	"errors"
	"io"
	"net"
	"strings"
	"syscall"
)

func IsNetworkError(err error) bool {
	if err == nil {
		return false
	}

	// CHECK COMMON NETWORK ERRORS
	if errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		strings.Contains(err.Error(), "broken pipe") ||
		strings.Contains(err.Error(), "reset by peer") {
		return true
	}

	// CHECK FOR TIMEOUT
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		return true
	}

	return false
}

package pages

import (
	"testing"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

func TestAfterTokenRoundTrip(t *testing.T) {
	token := AfterToken("abc-123")
	if got := After(&v1.PageRequest{PageToken: token}); got != "abc-123" {
		t.Errorf("After = %q, want abc-123", got)
	}
	if _, offset := Parse(&v1.PageRequest{PageToken: token}); offset != 0 {
		t.Errorf("keyset token parsed as offset %d", offset)
	}
	offsetToken := Info(0, 20, 100).NextPageToken
	if got := After(&v1.PageRequest{PageToken: offsetToken}); got != "" {
		t.Errorf("offset token read as keyset id %q", got)
	}
	if got := After(nil); got != "" {
		t.Errorf("nil request gave %q", got)
	}
}

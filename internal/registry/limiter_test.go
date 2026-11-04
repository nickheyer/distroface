package registry

import (
	"regexp"
	"testing"
)

// The scan answers exactly like the regexp it replaced
func TestIsManifestPath(t *testing.T) {
	legacy := regexp.MustCompile(`^/v2/.+/manifests/[^/]+$`)
	cases := []string{
		"/v2/ns/app/manifests/latest",
		"/v2/ns/app/manifests/sha256:abc",
		"/v2/a/b/c/manifests/v1.2",
		"/v2/a/manifests/b/manifests/c",
		"/v2/ns/app/manifests/",
		"/v2/ns/app/manifests",
		"/v2//manifests/x",
		"/v2/manifests/x",
		"/v2/ns/app/blobs/sha256:abc",
		"/v2/ns/app/tags/list",
		"/v2/",
		"/v2",
		"/api/v2/ns/app/manifests/latest",
		"/v2/ns/app/manifests/a/b",
		"/v2/manifests/manifests/x",
	}
	for _, p := range cases {
		if got, want := isManifestPath(p), legacy.MatchString(p); got != want {
			t.Errorf("isManifestPath(%q) = %v, regexp says %v", p, got, want)
		}
	}
}

package natsort

import (
	"cmp"
	"strconv"
	"strings"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Dotted numeric version with optional prerelease suffix
type Version struct {
	nums []int64
	pre  string
}

// Nil when the string is not version shaped
func ParseVersion(s string) *Version {
	body, pre, _ := strings.Cut(s, "-")
	if len(body) > 1 && body[0] == 'v' && body[1] >= '0' && body[1] <= '9' {
		body = body[1:]
	}
	parts := strings.Split(body, ".")
	nums := make([]int64, len(parts))
	for i, p := range parts {
		if p == "" || p[0] < '0' || p[0] > '9' {
			return nil
		}
		n, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			return nil
		}
		nums[i] = n
	}
	return &Version{nums: nums, pre: pre}
}

// Releases outrank prereleases at equal numbers
func CompareVersions(a, b *Version) int {
	for i := 0; i < len(a.nums) || i < len(b.nums); i++ {
		var av, bv int64
		if i < len(a.nums) {
			av = a.nums[i]
		}
		if i < len(b.nums) {
			bv = b.nums[i]
		}
		if av != bv {
			if av < bv {
				return -1
			}
			return 1
		}
	}
	if (a.pre == "") != (b.pre == "") {
		if a.pre == "" {
			return 1
		}
		return -1
	}
	return Compare(a.pre, b.pre)
}

// Digest cluster rank, latest then newest version
type TagGroupKey struct {
	hasLatest bool
	version   *Version
	name      string
}

func CompareTagGroups(a, b TagGroupKey) int {
	if a.hasLatest != b.hasLatest {
		if a.hasLatest {
			return 1
		}
		return -1
	}
	if (a.version != nil) != (b.version != nil) {
		if a.version != nil {
			return 1
		}
		return -1
	}
	if a.version != nil {
		if c := CompareVersions(a.version, b.version); c != 0 {
			return c
		}
	}
	return Compare(a.name, b.name)
}

// Latest leads a cluster, then newest version, then name
func CompareTagsInGroup(a, b *v1.Tag) int {
	if la, lb := a.Name == "latest", b.Name == "latest"; la != lb {
		if la {
			return 1
		}
		return -1
	}
	av, bv := ParseVersion(a.Name), ParseVersion(b.Name)
	if (av != nil) != (bv != nil) {
		if av != nil {
			return 1
		}
		return -1
	}
	if av != nil {
		if c := CompareVersions(av, bv); c != 0 {
			return c
		}
	}
	return Compare(a.Name, b.Name)
}

// Version order keeps shared digests adjacent, newest first
func TagVersionComparator(tags []*v1.Tag) func(a, b *v1.Tag) int {
	keys := make(map[string]TagGroupKey, len(tags))
	for _, t := range tags {
		k := keys[t.Digest]
		if t.Name == "latest" {
			k.hasLatest = true
		}
		if v := ParseVersion(t.Name); v != nil {
			if k.version == nil || CompareVersions(v, k.version) > 0 {
				k.version = v
				k.name = t.Name
			}
		} else if k.version == nil && Compare(t.Name, k.name) > 0 {
			k.name = t.Name
		}
		keys[t.Digest] = k
	}
	return func(a, b *v1.Tag) int {
		if a.Digest == b.Digest {
			return CompareTagsInGroup(a, b)
		}
		if c := CompareTagGroups(keys[a.Digest], keys[b.Digest]); c != 0 {
			return c
		}
		return cmp.Compare(a.Digest, b.Digest)
	}
}

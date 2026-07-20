package natsort

import (
	"slices"
	"testing"
)

func TestCompareOrdersVersions(t *testing.T) {
	got := []string{"v1.2", "v1.10", "v1.9", "v0.9.1", "latest", "v1.2-rc1"}
	SortDesc(got)
	want := []string{"v1.10", "v1.9", "v1.2-rc1", "v1.2", "v0.9.1", "latest"}
	if !slices.Equal(got, want) {
		t.Errorf("desc order = %v, want %v", got, want)
	}

	SortAsc(got)
	if got[0] != "latest" || got[len(got)-1] != "v1.10" {
		t.Errorf("asc order = %v", got)
	}
}

func TestCompareEdges(t *testing.T) {
	if Compare("", "") != 0 || Compare("a", "a") != 0 {
		t.Error("equal inputs must compare zero")
	}
	if Compare("2", "10") >= 0 {
		t.Error("numeric runs must compare numerically")
	}
	if Compare("abc", "abcd") >= 0 {
		t.Error("prefix must sort first")
	}
}

package mirror

import "testing"

func TestHunkAnchor(t *testing.T) {
	cases := []struct {
		name string
		hunk string
		line int
		side string
	}{
		{"added line", "@@ -10,3 +10,4 @@ func x() {\n a\n b\n+c\n", 12, "new"},
		{"removed line", "@@ -10,3 +10,2 @@\n a\n-b\n", 11, "old"},
		{"context line", "@@ -1,3 +1,3 @@\n a\n b\n c\n", 3, "new"},
		{"mixed tail", "@@ -5,4 +5,5 @@\n a\n-b\n+c\n+d\n e\n", 8, "new"},
		{"no newline marker", "@@ -1,2 +1,2 @@\n a\n-b\n\\ No newline at end of file\n", 2, "old"},
		{"single number header", "@@ -0,0 +1 @@\n+only\n", 1, "new"},
		{"garbage", "not a hunk", 0, "new"},
	}
	for _, c := range cases {
		line, side := hunkAnchor(c.hunk)
		if line != c.line || side != c.side {
			t.Errorf("%s: got %d %s, want %d %s", c.name, line, side, c.line, c.side)
		}
	}
}

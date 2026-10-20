package stores

import "testing"

func TestIssueOrderClause(t *testing.T) {
	cases := map[string]string{
		"":                           "git_issues.created_at DESC",
		"created_at DESC":            "git_issues.created_at DESC",
		"git_issues.created_at DESC": "git_issues.created_at DESC",
		"git_issues.updated_at ASC":  "git_issues.updated_at ASC",
		"comments DESC":              "git_issues.comment_count DESC",
		"git_issues.comments ASC":    "git_issues.comment_count ASC",
		"  number ASC  ":             "git_issues.number ASC",
	}
	for in, want := range cases {
		if got := issueOrderClause(in); got != want {
			t.Errorf("issueOrderClause(%q) = %q, want %q", in, got, want)
		}
	}
}

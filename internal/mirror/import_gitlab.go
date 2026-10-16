package mirror

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Gitlab caps list pages at one hundred entries
const gitlabPageSize = 100

// Reads gitlab metadata for the importer
type gitlabForge struct{}

func (gitlabForge) headers(f forgeCtx) map[string]string {
	h := map[string]string{}
	if f.token != "" {
		h["PRIVATE-TOKEN"] = f.token
	}
	return h
}

func (gitlabForge) projectURL(f forgeCtx, suffix string) string {
	return f.up.apiBase() + "/projects/" + url.PathEscape(f.up.Path) + suffix
}

type glUser struct {
	Username string `json:"username"`
	Email    string `json:"email"`
}

func (u *glUser) user() forgeUser {
	if u == nil {
		return forgeUser{}
	}
	return forgeUser{Login: u.Username, Email: u.Email}
}

type glLabel struct {
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description"`
}

type glMilestone struct {
	ID          int64     `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	State       string    `json:"state"`
	DueDate     string    `json:"due_date"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type glIssue struct {
	IID              int64        `json:"iid"`
	Title            string       `json:"title"`
	Description      string       `json:"description"`
	State            string       `json:"state"`
	Author           *glUser      `json:"author"`
	Labels           []string     `json:"labels"`
	Milestone        *glMilestone `json:"milestone"`
	DiscussionLocked bool         `json:"discussion_locked"`
	CreatedAt        time.Time    `json:"created_at"`
	UpdatedAt        time.Time    `json:"updated_at"`
	ClosedAt         *time.Time   `json:"closed_at"`
	WebURL           string       `json:"web_url"`
}

func (i glIssue) issue() forgeIssue {
	out := forgeIssue{
		Number: i.IID, Title: i.Title, Body: i.Description, State: i.State, Author: i.Author.user(), Labels: i.Labels,
		Locked: i.DiscussionLocked, CreatedAt: i.CreatedAt, UpdatedAt: i.UpdatedAt, ClosedAt: i.ClosedAt, URL: i.WebURL,
	}
	if i.Milestone != nil {
		out.MilestoneID = strconv.FormatInt(i.Milestone.ID, 10)
	}
	return out
}

type glNote struct {
	Author    *glUser   `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	System    bool      `json:"system"`
}

type glMergeRequest struct {
	glIssue
	Draft           bool       `json:"draft"`
	WorkInProgress  bool       `json:"work_in_progress"`
	SourceBranch    string     `json:"source_branch"`
	TargetBranch    string     `json:"target_branch"`
	SHA             string     `json:"sha"`
	MergeCommitSHA  string     `json:"merge_commit_sha"`
	SquashCommitSHA string     `json:"squash_commit_sha"`
	MergedAt        *time.Time `json:"merged_at"`
}

type glReleaseMeta struct {
	TagName         string     `json:"tag_name"`
	Name            string     `json:"name"`
	Description     string     `json:"description"`
	ReleasedAt      *time.Time `json:"released_at"`
	CreatedAt       time.Time  `json:"created_at"`
	Author          *glUser    `json:"author"`
	UpcomingRelease bool       `json:"upcoming_release"`
	Commit          struct {
		ID string `json:"id"`
	} `json:"commit"`
}

func (d gitlabForge) labels(ctx context.Context, f forgeCtx, page int) ([]forgeLabel, bool, error) {
	batch, more, err := pagedJSON[glLabel](ctx, f, d.projectURL(f, fmt.Sprintf("/labels?per_page=%d&page=%d", gitlabPageSize, page)), d.headers(f), gitlabPageSize)
	if err != nil {
		return nil, false, err
	}
	out := make([]forgeLabel, 0, len(batch))
	for _, l := range batch {
		out = append(out, forgeLabel{Name: l.Name, Color: l.Color, Description: l.Description})
	}
	return out, more, nil
}

func (d gitlabForge) milestones(ctx context.Context, f forgeCtx, page int) ([]forgeMilestone, bool, error) {
	batch, more, err := pagedJSON[glMilestone](ctx, f, d.projectURL(f, fmt.Sprintf("/milestones?per_page=%d&page=%d", gitlabPageSize, page)), d.headers(f), gitlabPageSize)
	if err != nil {
		return nil, false, err
	}
	out := make([]forgeMilestone, 0, len(batch))
	for _, ms := range batch {
		fm := forgeMilestone{ID: strconv.FormatInt(ms.ID, 10), Title: ms.Title, Description: ms.Description, State: "open"}
		if ms.State == "closed" {
			fm.State = "closed"
			at := ms.UpdatedAt
			fm.ClosedAt = &at
		}
		if t, err := time.Parse("2006-01-02", ms.DueDate); err == nil {
			fm.DueDate = &t
		}
		out = append(out, fm)
	}
	return out, more, nil
}

func (d gitlabForge) issues(ctx context.Context, f forgeCtx, page int) ([]forgeIssue, bool, error) {
	batch, more, err := pagedJSON[glIssue](ctx, f, d.projectURL(f, fmt.Sprintf("/issues?scope=all&order_by=created_at&sort=asc&per_page=%d&page=%d", gitlabPageSize, page)), d.headers(f), gitlabPageSize)
	if err != nil {
		return nil, false, err
	}
	out := make([]forgeIssue, 0, len(batch))
	for _, i := range batch {
		out = append(out, i.issue())
	}
	return out, more, nil
}

func (d gitlabForge) notes(ctx context.Context, f forgeCtx, kind string, number int64, page int) ([]forgeComment, bool, error) {
	batch, more, err := pagedJSON[glNote](ctx, f, d.projectURL(f, fmt.Sprintf("/%s/%d/notes?sort=asc&order_by=created_at&per_page=%d&page=%d", kind, number, gitlabPageSize, page)), d.headers(f), gitlabPageSize)
	if err != nil {
		return nil, false, err
	}
	out := make([]forgeComment, 0, len(batch))
	for _, n := range batch {
		// System notes narrate state changes recorded separately
		if n.System {
			continue
		}
		out = append(out, forgeComment{Author: n.Author.user(), Body: n.Body, CreatedAt: n.CreatedAt})
	}
	return out, more, nil
}

func (d gitlabForge) comments(ctx context.Context, f forgeCtx, number int64, page int) ([]forgeComment, bool, error) {
	return d.notes(ctx, f, "issues", number, page)
}

func (d gitlabForge) pulls(ctx context.Context, f forgeCtx, page int) ([]forgePull, bool, error) {
	batch, more, err := pagedJSON[glMergeRequest](ctx, f, d.projectURL(f, fmt.Sprintf("/merge_requests?scope=all&order_by=created_at&sort=asc&per_page=%d&page=%d", gitlabPageSize, page)), d.headers(f), gitlabPageSize)
	if err != nil {
		return nil, false, err
	}
	out := make([]forgePull, 0, len(batch))
	for _, mr := range batch {
		mergeSHA := mr.MergeCommitSHA
		if mergeSHA == "" {
			mergeSHA = mr.SquashCommitSHA
		}
		out = append(out, forgePull{
			forgeIssue: mr.issue(),
			HeadBranch: mr.SourceBranch,
			HeadSHA:    mr.SHA,
			BaseBranch: mr.TargetBranch,
			Draft:      mr.Draft || mr.WorkInProgress,
			Merged:     mr.State == "merged",
			MergedAt:   mr.MergedAt,
			MergeSHA:   mergeSHA,
		})
	}
	return out, more, nil
}

func (d gitlabForge) pullComments(ctx context.Context, f forgeCtx, number int64, page int) ([]forgeComment, bool, error) {
	return d.notes(ctx, f, "merge_requests", number, page)
}

// Approvals are the only review signal the api exposes
func (d gitlabForge) reviews(ctx context.Context, f forgeCtx, number int64) ([]forgeReview, error) {
	var resp struct {
		ApprovedBy []struct {
			User *glUser `json:"user"`
		} `json:"approved_by"`
	}
	err := fetchJSON(ctx, f.client, d.projectURL(f, fmt.Sprintf("/merge_requests/%d/approvals", number)), d.headers(f), &resp)
	var ue *upstreamError
	if errors.As(err, &ue) && (ue.status == http.StatusNotFound || ue.status == http.StatusForbidden) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]forgeReview, 0, len(resp.ApprovedBy))
	for _, a := range resp.ApprovedBy {
		out = append(out, forgeReview{Author: a.User.user(), State: "approved"})
	}
	return out, nil
}

func (d gitlabForge) releases(ctx context.Context, f forgeCtx, page int) ([]forgeRelease, bool, error) {
	batch, more, err := pagedJSON[glReleaseMeta](ctx, f, d.projectURL(f, fmt.Sprintf("/releases?per_page=%d&page=%d", gitlabPageSize, page)), d.headers(f), gitlabPageSize)
	if err != nil {
		return nil, false, err
	}
	out := make([]forgeRelease, 0, len(batch))
	for _, r := range batch {
		out = append(out, forgeRelease{
			Tag: r.TagName, Name: r.Name, Body: r.Description, Prerelease: r.UpcomingRelease,
			TargetSHA: r.Commit.ID, Author: r.Author.user(), PublishedAt: r.ReleasedAt, CreatedAt: r.CreatedAt,
		})
	}
	return out, more, nil
}

func (gitlabForge) pullHeadRef(number int64) string {
	return fmt.Sprintf("refs/merge-requests/%d/head", number)
}

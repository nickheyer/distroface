package mirror

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// Github caps list pages at one hundred entries
const githubPageSize = 100

// Reads github metadata, enterprise hosts use /api/v3
type githubForge struct{}

func (githubForge) headers(f forgeCtx) map[string]string {
	h := map[string]string{
		"Accept":               "application/vnd.github+json",
		"X-GitHub-Api-Version": "2022-11-28",
	}
	if f.token != "" {
		h["Authorization"] = "Bearer " + f.token
	}
	return h
}

func (githubForge) repoURL(f forgeCtx, suffix string) string {
	return f.up.apiBase() + "/repos/" + escapePath(f.up.Path) + suffix
}

type ghUser struct {
	Login string `json:"login"`
	Email string `json:"email"`
}

func (u *ghUser) user() forgeUser {
	if u == nil {
		return forgeUser{}
	}
	return forgeUser{Login: u.Login, Email: u.Email}
}

type ghLabel struct {
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description"`
}

type ghMilestone struct {
	Number      int64      `json:"number"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	State       string     `json:"state"`
	DueOn       *time.Time `json:"due_on"`
	ClosedAt    *time.Time `json:"closed_at"`
}

type ghIssue struct {
	Number      int64        `json:"number"`
	Title       string       `json:"title"`
	Body        string       `json:"body"`
	State       string       `json:"state"`
	User        *ghUser      `json:"user"`
	Labels      []ghLabel    `json:"labels"`
	Milestone   *ghMilestone `json:"milestone"`
	Locked      bool         `json:"locked"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
	ClosedAt    *time.Time   `json:"closed_at"`
	HTMLURL     string       `json:"html_url"`
	PullRequest *struct{}    `json:"pull_request"`
}

func (i ghIssue) issue() forgeIssue {
	out := forgeIssue{
		Number: i.Number, Title: i.Title, Body: i.Body, State: i.State, Author: i.User.user(),
		Locked: i.Locked, CreatedAt: i.CreatedAt, UpdatedAt: i.UpdatedAt, ClosedAt: i.ClosedAt, URL: i.HTMLURL,
	}
	for _, l := range i.Labels {
		out.Labels = append(out.Labels, l.Name)
	}
	if i.Milestone != nil {
		out.MilestoneID = strconv.FormatInt(i.Milestone.Number, 10)
	}
	return out
}

type ghComment struct {
	User      *ghUser   `json:"user"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

type ghPull struct {
	ghIssue
	Draft bool `json:"draft"`
	Head  struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
	MergedAt       *time.Time `json:"merged_at"`
	MergeCommitSHA string     `json:"merge_commit_sha"`
}

type ghReview struct {
	User        *ghUser    `json:"user"`
	State       string     `json:"state"`
	Body        string     `json:"body"`
	CommitID    string     `json:"commit_id"`
	SubmittedAt *time.Time `json:"submitted_at"`
}

type ghReleaseMeta struct {
	TagName         string     `json:"tag_name"`
	Name            string     `json:"name"`
	Body            string     `json:"body"`
	Draft           bool       `json:"draft"`
	Prerelease      bool       `json:"prerelease"`
	TargetCommitish string     `json:"target_commitish"`
	Author          *ghUser    `json:"author"`
	PublishedAt     *time.Time `json:"published_at"`
	CreatedAt       time.Time  `json:"created_at"`
}

func (d githubForge) labels(ctx context.Context, f forgeCtx, page int) ([]forgeLabel, bool, error) {
	batch, more, err := pagedJSON[ghLabel](ctx, f, d.repoURL(f, fmt.Sprintf("/labels?per_page=%d&page=%d", githubPageSize, page)), d.headers(f), githubPageSize)
	if err != nil {
		return nil, false, err
	}
	out := make([]forgeLabel, 0, len(batch))
	for _, l := range batch {
		out = append(out, forgeLabel{Name: l.Name, Color: l.Color, Description: l.Description})
	}
	return out, more, nil
}

func (d githubForge) milestones(ctx context.Context, f forgeCtx, page int) ([]forgeMilestone, bool, error) {
	batch, more, err := pagedJSON[ghMilestone](ctx, f, d.repoURL(f, fmt.Sprintf("/milestones?state=all&per_page=%d&page=%d", githubPageSize, page)), d.headers(f), githubPageSize)
	if err != nil {
		return nil, false, err
	}
	out := make([]forgeMilestone, 0, len(batch))
	for _, ms := range batch {
		out = append(out, forgeMilestone{ID: strconv.FormatInt(ms.Number, 10), Title: ms.Title, Description: ms.Description, State: ms.State, DueDate: ms.DueOn, ClosedAt: ms.ClosedAt})
	}
	return out, more, nil
}

func (d githubForge) issues(ctx context.Context, f forgeCtx, page int) ([]forgeIssue, bool, error) {
	batch, more, err := pagedJSON[ghIssue](ctx, f, d.repoURL(f, fmt.Sprintf("/issues?state=all&sort=created&direction=asc&per_page=%d&page=%d", githubPageSize, page)), d.headers(f), githubPageSize)
	if err != nil {
		return nil, false, err
	}
	out := make([]forgeIssue, 0, len(batch))
	for _, i := range batch {
		// Pull requests arrive through the pulls listing
		if i.PullRequest != nil {
			continue
		}
		out = append(out, i.issue())
	}
	return out, more, nil
}

func (d githubForge) comments(ctx context.Context, f forgeCtx, number int64, page int) ([]forgeComment, bool, error) {
	batch, more, err := pagedJSON[ghComment](ctx, f, d.repoURL(f, fmt.Sprintf("/issues/%d/comments?per_page=%d&page=%d", number, githubPageSize, page)), d.headers(f), githubPageSize)
	if err != nil {
		return nil, false, err
	}
	out := make([]forgeComment, 0, len(batch))
	for _, c := range batch {
		out = append(out, forgeComment{Author: c.User.user(), Body: c.Body, CreatedAt: c.CreatedAt})
	}
	return out, more, nil
}

func (d githubForge) pulls(ctx context.Context, f forgeCtx, page int) ([]forgePull, bool, error) {
	batch, more, err := pagedJSON[ghPull](ctx, f, d.repoURL(f, fmt.Sprintf("/pulls?state=all&sort=created&direction=asc&per_page=%d&page=%d", githubPageSize, page)), d.headers(f), githubPageSize)
	if err != nil {
		return nil, false, err
	}
	out := make([]forgePull, 0, len(batch))
	for _, p := range batch {
		out = append(out, forgePull{
			forgeIssue: p.issue(),
			HeadBranch: p.Head.Ref,
			HeadSHA:    p.Head.SHA,
			BaseBranch: p.Base.Ref,
			Draft:      p.Draft,
			Merged:     p.MergedAt != nil,
			MergedAt:   p.MergedAt,
			MergeSHA:   p.MergeCommitSHA,
		})
	}
	return out, more, nil
}

func (d githubForge) pullComments(ctx context.Context, f forgeCtx, number int64, page int) ([]forgeComment, bool, error) {
	return d.comments(ctx, f, number, page)
}

func (d githubForge) reviews(ctx context.Context, f forgeCtx, number int64) ([]forgeReview, error) {
	var out []forgeReview
	for page := 1; page <= importMaxPages; page++ {
		batch, more, err := pagedJSON[ghReview](ctx, f, d.repoURL(f, fmt.Sprintf("/pulls/%d/reviews?per_page=%d&page=%d", number, githubPageSize, page)), d.headers(f), githubPageSize)
		if err != nil {
			return nil, err
		}
		for _, r := range batch {
			out = append(out, forgeReview{Author: r.User.user(), State: r.State, Body: r.Body, CommitSHA: r.CommitID, SubmittedAt: r.SubmittedAt, Dismissed: r.State == "DISMISSED"})
		}
		if !more {
			break
		}
	}
	return out, nil
}

func (d githubForge) releases(ctx context.Context, f forgeCtx, page int) ([]forgeRelease, bool, error) {
	batch, more, err := pagedJSON[ghReleaseMeta](ctx, f, d.repoURL(f, fmt.Sprintf("/releases?per_page=%d&page=%d", githubPageSize, page)), d.headers(f), githubPageSize)
	if err != nil {
		return nil, false, err
	}
	out := make([]forgeRelease, 0, len(batch))
	for _, r := range batch {
		out = append(out, forgeRelease{
			Tag: r.TagName, Name: r.Name, Body: r.Body, Draft: r.Draft, Prerelease: r.Prerelease,
			TargetSHA: r.TargetCommitish, Author: r.Author.user(), PublishedAt: r.PublishedAt, CreatedAt: r.CreatedAt,
		})
	}
	return out, more, nil
}

func (githubForge) pullHeadRef(number int64) string {
	return fmt.Sprintf("refs/pull/%d/head", number)
}

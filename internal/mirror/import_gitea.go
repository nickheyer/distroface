package mirror

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// Reads gitea and forgejo metadata for the importer
type giteaForge struct{}

func (giteaForge) headers(f forgeCtx) map[string]string {
	h := map[string]string{}
	if f.token != "" {
		h["Authorization"] = "token " + f.token
	}
	return h
}

func (giteaForge) repoURL(f forgeCtx, suffix string) string {
	return f.up.apiBase() + "/repos/" + escapePath(f.up.Path) + suffix
}

type gtUser struct {
	Login string `json:"login"`
	Email string `json:"email"`
}

func (u *gtUser) user() forgeUser {
	if u == nil {
		return forgeUser{}
	}
	return forgeUser{Login: u.Login, Email: u.Email}
}

type gtLabel struct {
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description"`
}

type gtMilestone struct {
	ID          int64      `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	State       string     `json:"state"`
	DueOn       *time.Time `json:"due_on"`
	ClosedAt    *time.Time `json:"closed_at"`
}

type gtIssue struct {
	Number    int64        `json:"number"`
	Title     string       `json:"title"`
	Body      string       `json:"body"`
	State     string       `json:"state"`
	User      *gtUser      `json:"user"`
	Labels    []gtLabel    `json:"labels"`
	Milestone *gtMilestone `json:"milestone"`
	IsLocked  bool         `json:"is_locked"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
	ClosedAt  *time.Time   `json:"closed_at"`
	HTMLURL   string       `json:"html_url"`
}

func (i gtIssue) issue() forgeIssue {
	out := forgeIssue{
		Number: i.Number, Title: i.Title, Body: i.Body, State: i.State, Author: i.User.user(),
		Locked: i.IsLocked, CreatedAt: i.CreatedAt, UpdatedAt: i.UpdatedAt, ClosedAt: i.ClosedAt, URL: i.HTMLURL,
	}
	for _, l := range i.Labels {
		out.Labels = append(out.Labels, l.Name)
	}
	if i.Milestone != nil {
		out.MilestoneID = strconv.FormatInt(i.Milestone.ID, 10)
	}
	return out
}

type gtComment struct {
	User      *gtUser   `json:"user"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

type gtPull struct {
	gtIssue
	Head struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
	Merged         bool       `json:"merged"`
	MergedAt       *time.Time `json:"merged_at"`
	MergeCommitSHA string     `json:"merge_commit_sha"`
}

type gtReview struct {
	User        *gtUser    `json:"user"`
	State       string     `json:"state"`
	Body        string     `json:"body"`
	CommitID    string     `json:"commit_id"`
	SubmittedAt *time.Time `json:"submitted_at"`
	Dismissed   bool       `json:"dismissed"`
}

type gtReleaseMeta struct {
	TagName         string     `json:"tag_name"`
	Name            string     `json:"name"`
	Body            string     `json:"body"`
	Draft           bool       `json:"draft"`
	Prerelease      bool       `json:"prerelease"`
	TargetCommitish string     `json:"target_commitish"`
	Author          *gtUser    `json:"author"`
	PublishedAt     *time.Time `json:"published_at"`
	CreatedAt       time.Time  `json:"created_at"`
}

func (d giteaForge) labels(ctx context.Context, f forgeCtx, page int) ([]forgeLabel, bool, error) {
	batch, more, err := pagedJSON[gtLabel](ctx, f, d.repoURL(f, fmt.Sprintf("/labels?page=%d&limit=%d", page, giteaPageSize)), d.headers(f), giteaPageSize)
	if err != nil {
		return nil, false, err
	}
	out := make([]forgeLabel, 0, len(batch))
	for _, l := range batch {
		out = append(out, forgeLabel{Name: l.Name, Color: l.Color, Description: l.Description})
	}
	return out, more, nil
}

func (d giteaForge) milestones(ctx context.Context, f forgeCtx, page int) ([]forgeMilestone, bool, error) {
	batch, more, err := pagedJSON[gtMilestone](ctx, f, d.repoURL(f, fmt.Sprintf("/milestones?state=all&page=%d&limit=%d", page, giteaPageSize)), d.headers(f), giteaPageSize)
	if err != nil {
		return nil, false, err
	}
	out := make([]forgeMilestone, 0, len(batch))
	for _, ms := range batch {
		out = append(out, forgeMilestone{ID: strconv.FormatInt(ms.ID, 10), Title: ms.Title, Description: ms.Description, State: ms.State, DueDate: ms.DueOn, ClosedAt: ms.ClosedAt})
	}
	return out, more, nil
}

func (d giteaForge) issues(ctx context.Context, f forgeCtx, page int) ([]forgeIssue, bool, error) {
	batch, more, err := pagedJSON[gtIssue](ctx, f, d.repoURL(f, fmt.Sprintf("/issues?state=all&type=issues&page=%d&limit=%d", page, giteaPageSize)), d.headers(f), giteaPageSize)
	if err != nil {
		return nil, false, err
	}
	out := make([]forgeIssue, 0, len(batch))
	for _, i := range batch {
		out = append(out, i.issue())
	}
	return out, more, nil
}

// Comment listing is unpaged, one call returns the thread
func (d giteaForge) comments(ctx context.Context, f forgeCtx, number int64, page int) ([]forgeComment, bool, error) {
	if page > 1 {
		return nil, false, nil
	}
	var batch []gtComment
	if err := fetchJSON(ctx, f.client, d.repoURL(f, fmt.Sprintf("/issues/%d/comments", number)), d.headers(f), &batch); err != nil {
		return nil, false, err
	}
	out := make([]forgeComment, 0, len(batch))
	for _, c := range batch {
		out = append(out, forgeComment{Author: c.User.user(), Body: c.Body, CreatedAt: c.CreatedAt})
	}
	return out, false, nil
}

func (d giteaForge) pulls(ctx context.Context, f forgeCtx, page int) ([]forgePull, bool, error) {
	batch, more, err := pagedJSON[gtPull](ctx, f, d.repoURL(f, fmt.Sprintf("/pulls?state=all&page=%d&limit=%d", page, giteaPageSize)), d.headers(f), giteaPageSize)
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
			Merged:     p.Merged,
			MergedAt:   p.MergedAt,
			MergeSHA:   p.MergeCommitSHA,
		})
	}
	return out, more, nil
}

func (d giteaForge) pullComments(ctx context.Context, f forgeCtx, number int64, page int) ([]forgeComment, bool, error) {
	return d.comments(ctx, f, number, page)
}

func (d giteaForge) reviews(ctx context.Context, f forgeCtx, number int64) ([]forgeReview, error) {
	var batch []gtReview
	if err := fetchJSON(ctx, f.client, d.repoURL(f, fmt.Sprintf("/pulls/%d/reviews", number)), d.headers(f), &batch); err != nil {
		return nil, err
	}
	out := make([]forgeReview, 0, len(batch))
	for _, r := range batch {
		out = append(out, forgeReview{Author: r.User.user(), State: r.State, Body: r.Body, CommitSHA: r.CommitID, SubmittedAt: r.SubmittedAt, Dismissed: r.Dismissed})
	}
	return out, nil
}

func (d giteaForge) releases(ctx context.Context, f forgeCtx, page int) ([]forgeRelease, bool, error) {
	batch, more, err := pagedJSON[gtReleaseMeta](ctx, f, d.repoURL(f, fmt.Sprintf("/releases?page=%d&limit=%d", page, giteaPageSize)), d.headers(f), giteaPageSize)
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

func (giteaForge) pullHeadRef(number int64) string {
	return fmt.Sprintf("refs/pull/%d/head", number)
}

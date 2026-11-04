# Issues

Issues track work on a repository. Pull requests share the same comment timeline, labels, assignees and milestones, but they number separately and are documented in [Pull requests](pull-requests.md).

## Opening an issue

Open an issue from Repo > Issues > New issue, with `IssueService.CreateIssue`, or with `dfcli issue create` (see [CLI reference](../cli/reference.md)). The caller must be signed in and the issues unit must be enabled on the repository. Archived repositories refuse.

```bash
curl -X POST https://forge.example.com/distroface.v1.IssueService/CreateIssue \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"namespace":"acme","name":"api","title":"Login times out","body":"Steps: ..."}'
```

```bash
dfcli issue create acme/api --title "Login times out" --body "Steps: ..."
```

| Field | Description |
| --- | --- |
| `title` | Required, up to 500 characters |
| `body` | Markdown, up to 1 MiB |
| `label_ids`, `milestone_id`, `assignee_ids`, `due_date`, `project_id` | Honored only when the caller has write access, dropped otherwise |
| `template` | Filename of the template the body came from, stored as given |

The author is subscribed to the issue automatically. References in the body are recorded on the issues they name.

## Numbering and references

Issues are written `#12` and pull requests `!12`. Each kind has its own counter per repository, so `#12` and `!12` are different things. Cross repository references use `ns/name#12`. References inside fenced or inline code are ignored. Mentioning an issue in a body, a comment, or a commit message adds a `referenced` event to the target's timeline when the author can see the target.

### Closing keywords

`close`, `closes`, `closed`, `fix`, `fixes`, `fixed`, `resolve`, `resolves` and `resolved` followed by `#N` or `ns/name#N` close the issue. Keywords only ever target issues, never pull requests. An issue closes when all of these hold:

- The keyword is in a commit message pushed to the default branch, or in the body of a pull request merged into the default branch.
- The issue is in the same repository as the commit or pull request.
- The issue is open and the pusher or merger has write access on issues.

Up to 100 commits per push are scanned. The `closed` event records the commit sha.

## States

An issue is `open` or `closed`. Change it with `IssueService.UpdateIssue` (`state`), `dfcli issue close`, or `dfcli issue reopen`. The author and any writer may change state. Closing records `closed_at` and `closed_by`. When the repository unit setting `block_on_dependencies` is on, an issue with open blockers refuses to close. `open_seconds` on the issue sums the time spent open across every close and reopen.

Deleting an issue requires admin access and removes its comments and notifications (`IssueService.DeleteIssue`).

## Comments and reactions

`IssueService.CreateComment` posts a comment, or use `dfcli issue comment acme/api 12 "text"` (add `--pull` for a pull request). A body is required and capped at 1 MiB. Anonymous users cannot comment. On a locked issue only writers may comment. Editing a comment keeps the last 20 previous bodies in its payload. Timeline events are stored as typed comments and cannot be edited, and only admins can delete them. `ListComments` returns the timeline oldest first, or plain comments only with `comments_only`.

Reactions go on the issue body (empty `comment_id`) or on a comment with `AddReaction` and `RemoveReaction`. Accepted emoji: `+1`, `-1`, `laugh`, `hooray`, `confused`, `heart`, `rocket`, `eyes`.

## Labels

Labels belong to a repository or to an organization. Organization labels are inherited by every repository in that organization. Manage them at Repo > Labels or Org > Labels, or with `ListLabels`, `CreateLabel`, `UpdateLabel` and `DeleteLabel`. Deleting a label removes it from every issue. `dfcli issue label acme/api 12 bug` adds labels by name and `--remove` takes them off.

| Field | Description |
| --- | --- |
| `label_name` | Required, up to 100 characters, unique per owner ignoring case |
| `color` | Six digit hex, with or without `#`, empty uses `#6b7280` |
| `exclusive` | Labels named `scope/value` with this set exclude each other within the scope, the last one applied wins |

`ListIssues` filters by label name and requires every listed label to be present.

## Assignees

Any user who can see the repository may be assigned. Setting assignees needs write access. New assignees get an `assigned` notification. `dfcli issue assign acme/api 12 alice` adds, `--remove` withdraws. Agents can be assigned too, see [Sessions](../agents/sessions.md).

## Milestones

Milestones group issues toward a date. Manage them at Repo > Milestones or with `ListMilestones`, `CreateMilestone`, `UpdateMilestone` and `DeleteMilestone`. They need write access. A milestone has a title, description, due date and an `open` or `closed` state. Deleting one leaves its issues with no milestone. Setting or clearing the milestone on an issue writes `milestoned` and `demilestoned` events.

## Time tracking

Time tracking is off by default and is switched on per repository through the `time_tracking` unit. With it on, `IssueService.AddTrackedTime` records a manual entry of `seconds` (positive, under a year) with an optional note, and `ListTrackedTime` returns the entries and their sum. An entry can be deleted by its owner or any writer. The issue carries `tracked_seconds` as the total.

## Dependencies

`IssueService.AddDependency` marks an issue as blocked by another. `depends_on` accepts `12`, `#12`, `!12` or `ns/name#12`. An issue cannot block itself and cycles are refused. Both issues get a timeline event, and closing or reopening a blocker writes `blocker_closed` or `blocker_reopened` on everything it blocks. `ListDependencies` returns both directions. The author and writers may add and remove blockers. See States for how `block_on_dependencies` affects closing.

## Pinning and locking

Writers can pin up to three issues per repository with `PinIssue`. Pull requests cannot be pinned. `LockIssue` takes an optional reason and stops everyone but writers from commenting. `UnlockIssue` reverses it.

## Templates

Templates are read from the default branch. The first of `.distroface`, `.gitea` and `.github` that contains any template wins. Inside it, `ISSUE_TEMPLATE.md` and every `.md`, `.yaml` or `.yml` file under `ISSUE_TEMPLATE/` (except `config.yml`) is a template. Markdown templates may start with YAML front matter carrying `name`, `about`, `title`, `labels` and `assignees`. YAML files are treated as forms with `name`, `description`, `title`, `labels` and `assignees`. `PULL_REQUEST_TEMPLATE.md` or `pull_request_template.md` in the same folders seeds pull request bodies. `GitFileService.GetTemplates` returns them all.

## Mentions

`@username` notifies a user. `@org/team` notifies every member of a team in the repository's own organization. Mentions in code spans are ignored. Mentioned users receive the notification even when they ignore the repository or unsubscribed from the issue. Agents can be mentioned, see [Sessions](../agents/sessions.md).

## Reply by email

When mail is on for the repository and incoming mail has a reply address configured (see [Mail](../administration/mail.md)), notification emails carry a `Reply-To` with a token valid for 30 days. A reply sent from the notified address is posted as a comment by that user, marked `via_email`. Quoted history, lines starting with `>`, and common signature markers are stripped. Replies from any other address are refused.

## Listing and searching

`IssueService.ListIssues` lists open issues by default. Filters: `state`, `labels`, `milestone`, `assignee`, `author`, `mentioned`, `project_id`, and `is_pull` to pick issues, pulls, or both. `page.query` searches title and body and `page.order_by` accepts `created_at`, `updated_at`, `comments` and `due_date`. `SearchIssues` spans every repository the caller can read, optionally one namespace, and `involved` restricts to issues where the caller is author, assignee or mentioned. `BulkUpdateIssues` applies one state, milestone, label or assignee change to many issues and only touches issues, not pull requests.

## dfcli commands

| Command | Description |
| --- | --- |
| `dfcli issue list namespace/name` | `--state open\|closed`, `--label`, `--assignee`, `--author`, `-t` |
| `dfcli issue create namespace/name` | `--title`, `--body` |
| `dfcli issue view namespace/name number` | Show one issue |
| `dfcli issue close namespace/name number` | Close |
| `dfcli issue reopen namespace/name number` | Reopen |
| `dfcli issue comment namespace/name number [body]` | `--body`, `--pull` to target a pull request |
| `dfcli issue label namespace/name number label...` | Add labels, `--remove` to take them off |
| `dfcli issue assign namespace/name number username...` | Add assignees, `--remove` to withdraw |

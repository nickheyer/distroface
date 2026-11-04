# Pull requests

A pull request proposes the commits of a head branch for a base branch in the same repository or a fork. It carries an issue (title, body, comments, labels, assignees, milestone) plus reviews, checks and a merge.

## Opening a pull request

Open one from Repo > Pull requests > New pull request, with `PullRequestService.CreatePullRequest`, or with `dfcli pr create` (see [CLI reference](../cli/reference.md)). The caller must be signed in, the pulls unit must be enabled, and the repository must not be archived or a mirror. Pull requests number as `!N`, separately from issues.

```bash
curl -X POST https://forge.example.com/distroface.v1.PullRequestService/CreatePullRequest \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"namespace":"acme","name":"api","base":"main","head":"feature/login","title":"Fix login timeout","draft":false}'
```

```bash
dfcli pr create acme/api --base main --head feature/login --title "Fix login timeout"
dfcli pr create acme/api --base main --head alice/api:feature/login --title "From a fork"
```

| Field | Description |
| --- | --- |
| `base` | Branch in this repository, empty uses the default branch |
| `head` | Branch here, or `ns/name:branch` for a fork of this repository. The caller needs push access to the fork |
| `draft` | Open as a draft |
| `reviewer_ids`, `team_reviewer_ids` | Review requests sent on open, merged with CODEOWNERS matches |
| `allow_maintainer_edits` | Lets base writers push the head branch of a fork the author personally owns |

Head and base cannot be the same branch, and a second open pull request for the same head and base pair is refused. Pull request bodies can be seeded from a template, see [Issues templates](issues.md#templates). Opening fires the `pull_request` workflow event, see [Workflows](../actions/workflows.md).

## Pull refs

Every pull request has `refs/pull/N/head` in the base repository pointing at its head commit. It is written on open and refreshed on every push to the head branch, including pushes to a fork. Pushes that try to update `refs/pull/*` are refused. Fetch it to review locally:

```bash
git fetch origin +refs/pull/12/head:pr-12
dfcli pr checkout acme/api 12
```

`dfcli pr checkout` fetches `refs/pull/N/head` into a local branch named `pr-N` from the `origin` remote, or the one given with `--remote`.

## Head pushes

A push to the head branch records a `head_pushed` or `force_pushed` event with the commit count, resets the merge state, re-runs diff statistics, and triggers the `pull_request` workflow with action `synchronize`. Inline comments on files that changed between the old and new head are marked outdated. When the base branch rule sets `dismiss_stale_approvals`, approvals made on the old head are dismissed. A push by anyone other than the user who armed auto merge cancels it. Deleting the head branch marks the pull request `head_deleted`.

## Drafts

A draft cannot merge. Toggle it with `UpdatePullRequest` (`draft`), which writes `converted_to_draft` or `ready_for_review` and fires the matching workflow action.

## Reviews

A review is a batch of inline comments plus a state: `approved`, `changes_requested` or `commented`. `CreateReview` submits one in a single call, or starts a pending review when `state` is unspecified. Inline comments added to a pending review stay hidden until `SubmitReview`. Authors cannot approve or request changes on their own pull request. Reviews on a locked pull request need write access.

```bash
dfcli pr review acme/api 12 --approve
dfcli pr review acme/api 12 --request-changes --body "Handle the nil case"
dfcli pr review acme/api 12 --comment --body "Looks fine so far"
```

For protection rules only the newest review per reviewer counts, `commented` reviews carry no weight, and a reviewer must hold write access on pulls for an approval to count. `DismissReview` (writers) removes a review's weight and records who dismissed it.

Review requests go to users and to teams of the repository's organization with `RequestReviewers` and `RemoveReviewers`. The author cannot be requested. A request is fulfilled when that user submits a review. Requested users and team members get a `review_requested` notification. Agents can be requested as reviewers, see [Sessions](../agents/sessions.md).

## Inline comments

`CreateReviewComment` anchors a comment to `path` and `line` on side `new` (default) or `old`, with `start_line` for a range. Set `review_id` to add it to your pending review, or omit it to post immediately. Set `thread_id` to reply in a thread. `ListReviewComments` groups by thread, and `ResolveReviewThread` resolves or unresolves one (writers, the thread author, or the pull request author).

A comment containing a fenced `suggestion` block can be committed onto the head branch with `ApplySuggestion`. It applies on the new side only, needs push rights on the head branch, remaps the lines when the file changed since the comment, and resolves the thread.

## CODEOWNERS

The first of `CODEOWNERS`, `.distroface/CODEOWNERS`, `.gitea/CODEOWNERS`, `.github/CODEOWNERS` and `docs/CODEOWNERS` found on the base branch tip is used. Each line is a pattern followed by owners, and the last matching rule for a path wins. Owners are `@username`, an email address, or `@org/team` for a team in the repository's own organization.

On open, owners of every changed path are requested for review automatically, teams preferred over their members. When the base branch rule sets `require_code_owner_review`, each matched rule needs an approval from one of its owners, and a rule whose owners resolve to nobody blocks the merge.

## Mergeability

`GetMergeability` returns a state and the rules that fail. The conflict check merges base and head in memory once per head and base pair and caches the result on the pull request. Protection rules come from [Branches](branches.md) and are re-read on every call.

| State | Meaning |
| --- | --- |
| `clean` | No conflicts and no blockers |
| `conflict` | Base and head conflict, `conflict_paths` lists the files |
| `blocked` | At least one rule below fails |
| `unknown` | Not computed yet, `checking` while a refresh runs |

| Blocker rule | Cause |
| --- | --- |
| `archived`, `mirror`, `closed`, `draft` | Repository or pull request state |
| `conflict` | Conflicting files, a missing base branch, or a missing head commit |
| `approvals` | Fewer counted approvals than `required_approvals` |
| `changes_requested` | A weighted reviewer's newest review requests changes |
| `code_owners` | A matched CODEOWNERS rule has no approval from its owners |
| `status_checks` | A named context did not succeed. With no contexts named, every reported check must succeed and at least one must have reported |
| `signed_commits` | A commit between merge base and head is not verified |
| `linear_history` | A commit between merge base and head is a merge commit |
| `history` | More than 1000 commits, split the pull request |
| `up_to_date` | The head is behind the base |
| `merger` | The caller is not in `allowed_mergers` or `bypass_ids` |

`can_merge` is true when the caller has write access, the state is `clean`, and the merger rule passes. Commit statuses come from workflow jobs and `GitFileService.CreateCommitStatus`, see [Workflows](../actions/workflows.md).

## Merge methods

| Method | Result on the base branch |
| --- | --- |
| `merge` | One merge commit with base and head as parents. Default title `Merge pull request !N from <head>`, body the pull request title |
| `squash` | One commit with the base as parent, authored by the pull request author. Default title `<title> (!N)`, body a bullet per squashed commit |
| `rebase` | Head commits replayed one by one onto the base, no merge commit |
| `rebase_merge` | Head commits replayed, then a merge commit joining them to the base |
| `fast_forward` | The base moves to the head commit, refused when the base is not an ancestor of the head |

Rebase methods refuse to replay merge commits and pull requests over 1000 commits. Merge and squash commits are signed with the instance key when signing is on, see [Signing](signing.md).

## Repository merge settings

Set at Repo > Settings and stored as `GitMergeSettings` on the repository.

| Setting | Description |
| --- | --- |
| `allow_merge`, `allow_squash`, `allow_rebase`, `allow_rebase_merge`, `allow_fast_forward` | Methods offered on the pull request |
| `default_method` | Used when a merge names no method. Falls back to the first allowed method |
| `delete_branch_on_merge` | Delete the head branch after every merge |
| `allow_forks` | Whether the repository may be forked |

A base branch rule with `require_linear_history` additionally refuses `merge` and `rebase_merge`.

## Merging

`MergePullRequest` takes `method`, optional `title` and `message`, `delete_branch`, and `expected_head_sha`, which refuses the merge when the head moved past that sha. The merge is refused unless `can_merge` is true. Merging closes the pull request, writes a `merged` event, fires the `pull_request` workflow with action `closed`, records an audit entry, and closes issues named by closing keywords in the body when the base is the default branch (see [Issues closing keywords](issues.md#closing-keywords)). Merged pull requests cannot be reopened, deleted, or edited.

```bash
dfcli pr merge acme/api 12 --method squash --title "Fix login timeout" --delete-branch
```

## Auto merge

`ScheduleAutoMerge` (writers) stores a method, title, message and delete flag, writes `auto_merge_armed`, and retries the merge whenever a review is approved or the head or base branch is pushed. It merges as the user who armed it, using that user's access at merge time. `CancelAutoMerge` disarms it, as does closing the pull request or a head push by someone else.

## Closing, reopening and branches

`ClosePullRequest` closes without merging and optionally deletes the head branch, as does `dfcli pr close --delete-branch`. `ReopenPullRequest` needs the head branch to exist. Deleting the head branch needs push rights on it, never touches the default branch, and honors `block_deletion` on the head repository's rules. `RestoreHeadBranch` recreates a deleted head from `refs/pull/N/head`. `UpdateBranch` brings the head up to date by merging the base into it, or rebasing onto it with `rebase`.

## Conflicts

A `conflict` state lists the paths in `conflict_paths`. Resolve by pushing a new head, or by `UpdateBranch` when the base merges cleanly. The merge itself also fails with the conflicting paths when the branches moved since the last check.

## dfcli commands

| Command | Description |
| --- | --- |
| `dfcli pr list namespace/name` | `--state`, `--base`, `--head`, `--author`, `-t` |
| `dfcli pr create namespace/name` | `--base`, `--head branch` or `ns/name:branch`, `--title`, `--body`, `--draft` |
| `dfcli pr view namespace/name number` | Show one pull request |
| `dfcli pr checkout namespace/name number` | Fetch `refs/pull/N/head` into `pr-N`, `--remote` |
| `dfcli pr merge namespace/name number` | `--method merge\|squash\|rebase\|rebase-merge\|ff`, `--title`, `--message`, `--delete-branch` |
| `dfcli pr review namespace/name number` | `--approve`, `--request-changes` or `--comment`, `--body` required unless approving |
| `dfcli pr close namespace/name number` | `--delete-branch` |

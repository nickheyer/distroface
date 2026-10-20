# Branches and tags

`GitRefService` lists, creates, renames, and deletes branches and tags and stores the protection rules the pre receive hook and the merge path enforce.

## Listing

`GitRefService.ListBranches` pages branches with `page.query` filtering on name and `page.order_by` accepting `name` or `updated`. The default branch sorts first. Each branch carries its tip commit, `is_default`, `is_protected`, and `ahead` and `behind` relative to the default branch, counted up to 1000 commits. `GetBranch` returns one branch with its combined commit status. `ListGitTags` and `GetTag` return the peeled commit, and for annotated tags the tag object sha, tagger, message, and signature verification. `has_release` says a release exists for the tag, see [Releases](releases.md). The pages are Repo > Branches and Repo > Tags.

## Creating and deleting

| Method | Behavior |
| --- | --- |
| `CreateBranch` | `branch` from `from`, a branch, tag, or sha, the default branch when empty |
| `DeleteBranch` | Refuses the default branch |
| `RenameBranch` | Creates the new name, then deletes the old one. The default branch and names covered by a rule need admin access, exact pattern rules and open pull requests follow the new name, and the default branch setting moves with it |
| `CreateTag` | `target` defaults to the default branch, a non empty `message` writes an annotated tag object, signed with the instance key when signing is on |
| `DeleteTag` | Deletes the ref |

Writes need write on the code unit. Ref names follow git's rules, and every write passes the same pre receive policy as a push, so protected names, archived repositories, and mirrors refuse them.

```bash
curl -X POST https://df.example.com/distroface.v1.GitRefService/CreateBranch \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"namespace":"myteam","name":"app","branch":"feature","from":"main"}'
```

## Changing the default branch

Set `default_branch` with `GitService.UpdateGitRepository` or under Repo > Settings > General. The branch must exist unless the repository is empty, and `HEAD` moves with it. The first push to an empty repository picks the configured name when it is among the pushed branches, otherwise the first branch pushed.

## Protecting a branch

Rules live under Repo > Settings > Branches and are written with `GitRefService.SetBranchProtection`, which creates a rule or replaces the one named by `id`. Admin access is required. `ListBranchProtections` returns rules ordered by `priority` then creation time, and the first rule whose pattern matches a branch is the one applied. Patterns are globs over the short name, `release/*` matches one segment and `**` crosses slashes.

| Field | Description |
| --- | --- |
| `pattern` | Branch glob, required, no whitespace |
| `priority` | Lower values are checked first |
| `block_force_push` | The old tip must be an ancestor of the new one, unverifiable history is refused |
| `block_deletion` | Deleting a matching branch is refused, a rename may still retire the old name |
| `require_pull_request` | Direct updates are refused, changes land through merges |
| `required_approvals` | Approvals needed before a merge |
| `dismiss_stale_approvals` | New commits on the head dismiss existing approvals |
| `require_code_owner_review` | Every CODEOWNERS rule matching a changed path needs an approval from one of its owners |
| `require_status_checks` | Listed contexts must succeed on the head commit |
| `status_check_contexts` | Contexts to require, empty requires every reported context and at least one report |
| `require_signed_commits` | Every new commit must verify against a stored key or the instance key, see [Signing](signing.md) |
| `require_linear_history` | Merge commits are refused |
| `require_up_to_date` | The head must contain the base tip before merging |
| `allowed_pushers` | User and team ids that may push, empty allows every writer |
| `allowed_mergers` | User and team ids that may merge, empty allows every writer |
| `bypass_ids` | Users and teams the push rules and the merger list never apply to |

```bash
curl -X POST https://df.example.com/distroface.v1.GitRefService/SetBranchProtection \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"namespace":"myteam","name":"app","protection":{"pattern":"main","blockForcePush":true,"blockDeletion":true,"requirePullRequest":true,"requiredApprovals":1,"requireStatusChecks":true}}'
```

## How rules are enforced on push

The pre receive hook runs for every push over http and ssh and for every web commit, branch or tag write, and server side merge. Before any rule it refuses pushes to archived repositories, to mirrors, while an import is still running, to a repository over its size quota, to `refs/pull/` and `refs/distroface/`, to anything outside `refs/heads`, `refs/tags`, and `refs/notes`, and deletion of the default branch.

For each updated branch the first matching rule is applied in this order. A pusher in `bypass_ids`, directly or through a team, skips the rule. A deletion is refused when `block_deletion` is set. `require_pull_request` refuses the update unless the server is applying a merge. `allowed_pushers` is checked next. `block_force_push` walks the history to confirm the old tip is an ancestor of the new one. `require_linear_history` and `require_signed_commits` then examine every new commit, at most 10000 per push, refusing merge commits or any commit whose signature is not `VERIFIED` or `INSTANCE`. Rejections are printed to the client as `remote:` lines.

## How rules are enforced on merge

When a pull request targets a protected branch, the merge view lists blockers from the rule and the merge is refused while any remain. Approvals count the newest non dismissed review per reviewer, excluding the author, imported reviews, and reviewers without write on the pulls unit, and a standing changes requested review blocks on its own. `require_status_checks` reads the latest status per context on the head commit. Signed and linear history checks cover the commits between the merge base and the head, at most 1000. `require_up_to_date` requires the merge base to equal the base tip. A merger outside `allowed_mergers` and `bypass_ids` is blocked. Server side merges pass the hook with the pull request and signed commit requirements waived, since the rule was already satisfied through review. See [Pull requests](pull-requests.md).

## CODEOWNERS

The file is read from the base branch at the first of `CODEOWNERS`, `.distroface/CODEOWNERS`, `.gitea/CODEOWNERS`, `.github/CODEOWNERS`, or `docs/CODEOWNERS`. Each line is a pattern followed by owners, `#` starts a comment, and the last matching rule for a path wins. Owners are `@username`, an email address, or `@org/team`, where the team must belong to the repository's own org. Code owners are requested as reviewers automatically when a pull request opens. With `require_code_owner_review` every rule matching a changed path needs an approval from one of its resolved users, and a rule that resolves to nobody blocks the merge. `GitFileService.GetTemplates` reports `has_codeowners`.

## Protecting tags

`GitRefService.SetTagProtection`, `ListTagProtections`, and `DeleteTagProtection` manage tag rules under Repo > Settings > Tags. Each rule has a `pattern`, `block_deletion`, and `allowed_pushers`. Every rule whose pattern matches a tag applies on push, there is no priority.

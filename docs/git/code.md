# Code

`GitFileService` reads trees, files, history, diffs, and blame straight from the bare repository and commits web edits through the same policy path as a push. Every read needs read on the code unit, every write needs write.

## Browsing

| Method | Behavior |
| --- | --- |
| `GetTree` | Lists `path` at `ref`, the default branch when empty. `recursive` walks the subtree, `with_last_commit` resolves the last commit touching each entry within a budget of 2000 commits. Listings stop at 100000 entries and report `truncated` |
| `GetBlob` | Reads one file. Content above 1 MiB is cut and `truncated` set, `is_binary`, `content_type`, and the detected `language` are filled, and an LFS pointer is reported through `is_lfs` and `lfs` |
| `GetReadme` | Finds a readme for a directory, see below |
| `ListCommits` | Pages history from `ref` with `path`, `author` substring, `since`, and `until`. The page token is a commit sha cursor, 30 commits by default and 100 at most |
| `GetCommit` | One commit with its signature verification, statuses, and the branches and tags pointing at it |
| `GetCommitDiff` | Files changed by a commit against `parent`, paged, `page.query` keeps paths containing the text |
| `CompareRefs`, `ListCompareCommits`, `GetCompareDiff` | Compare `base` and `head`, diffing from the merge base when one exists. `head` may name another visible repository as `namespace/name:ref`, its objects are fetched into the base repository first |
| `GetDiffFile` | Hunks of one path, `base` empty diffs `head` against its first parent |
| `Blame` | Runs of lines by commit for files up to 1 MiB within a 30 second timeout |
| `SearchCode` | Regular expression grep of one ref with an optional `path_glob` such as `src/**/*.go` and `case_insensitive`, at most 500 matches |

Diffs carry stats for the first 1000 files and hunks only on single file reads. A file with more than 20000 changed lines or a blob above 2 MiB is reported without hunks. The pages are Repo > Tree, Blob, Commits, Commit, Compare, and Blame.

```bash
curl -X POST https://df.example.com/distroface.v1.GitFileService/GetTree \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"namespace":"myteam","name":"app","ref":"main","path":"cmd","withLastCommit":true}'
```

## Raw files

`GetBlob` returns `raw_url`, which is `/namespace/name/-/raw/<ref>/<path>`. The route streams the blob with a sniffed content type, an `ETag` of the blob sha, inline disposition for text, images, and json, and an attachment otherwise. `?lfs=1` serves the LFS object behind a pointer. Browsers use their session cookie, scripts use a token, and an unauthenticated request receives a `Bearer` challenge rather than a password prompt.

## README rendering

The repository home and every directory page render the readme returned by `GetReadme`. Candidates are tried in order `README.md`, `README.markdown`, `README`, `README.txt`, `readme.md`, `readme.markdown`, `readme`, `readme.txt`, `Readme.md`, first in the directory itself, then in its `.distroface`, `docs`, and `.github` folders.

## Editing files

The web editor under Repo > Edit, New, and Upload calls `GitFileService.UpdateFiles`, which commits a batch of operations in one commit.

| Field | Description |
| --- | --- |
| `branch` | Branch to commit on, the default branch when empty |
| `new_branch` | Commit to this new branch created from `branch` instead |
| `message` | Required commit message |
| `operations` | `kind` of `CREATE`, `UPDATE`, `DELETE`, or `RENAME` with `path`, `from_path` for renames, `content`, and `executable` |
| `expected_head_sha` | Refused with `Aborted` when the branch moved past this sha |

Each file is limited to 25 MiB, larger files arrive by push. The author and committer are your display name and account email, or `username@noreply.<host>` without one. The commit passes the pre receive hook, so branch protection, archived repositories, mirrors, running imports, and the size quota apply exactly as on a push. With `git.signing.enabled` the commit is signed by the instance key, see [Signing](signing.md).

```bash
curl -X POST https://df.example.com/distroface.v1.GitFileService/UpdateFiles \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"namespace":"myteam","name":"app","branch":"main","message":"Add config","operations":[{"kind":"FILE_OPERATION_KIND_CREATE","path":"config.yaml","content":"cG9ydDogODA4MAo="}]}'
```

`RevertCommit` and `CherryPickCommit` replay one commit onto `branch` or `new_branch` through the merge engine. Merge commits are refused, a conflict fails with `conflicts in <paths>`, and a revert that changes nothing is refused.

## Archive download

`GitFileService.GetArchiveURL` with `format` `zip` or `tar.gz` returns `/namespace/name/-/archive/<ref>.<format>`. The route also accepts `.tar`, names the top level folder `name-ref/`, streams the archive as an attachment, and counts against `git.max_concurrent_transfers` like a fetch. The repository page offers both formats from the clone box, and each release page links the archives of its tag.

## Commit statuses

`CreateCommitStatus` records a check result for a commit with `context`, `state`, `target_url`, and `description`, `ListCommitStatuses` pages them newest first, and `GetCombinedStatus` folds the latest status per context into one state. Statuses feed the required status checks rule in [Branches](branches.md) and workflow runs in [Actions](../actions/overview.md).

## Templates

`GetTemplates` reads the default branch for issue templates, a pull request template, and a CODEOWNERS file from `.distroface`, `.gitea`, and `.github`. See [Issues](issues.md) and [Pull requests](pull-requests.md).

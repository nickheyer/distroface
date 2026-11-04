# GitHub API

The server answers a subset of the GitHub REST API so that octokit based tooling inside jobs, such as `actions/github-script` and release actions, works against the instance. It is a compatibility surface for workflows, not a replacement for the ConnectRPC API, see [API overview](../api/overview.md).

## Base URL and authentication

The handler is mounted at `/api/v3`, the GitHub Enterprise convention, and at `/api/v1`, where act points jobs. Only `/api/v1/artifacts` and `/api/v1/auth/` are taken by the legacy artifact API, see [v1 API](../artifacts/v1-api.md). Inside a job `GITHUB_API_URL` and `github.api_url` are `<instance>/api/v1` and `GITHUB_SERVER_URL` is the instance.

Credentials come from the `Authorization` header as `Bearer <token>`, `token <token>` or `Basic` with the token as the password, or from the session cookie. A job uses `GITHUB_TOKEN`, a person uses a personal access token, see [Tokens](../administration/tokens.md). Without credentials the caller is anonymous when anonymous access is on, otherwise the answer is 401. When no auth provider is enabled every caller is admin.

```yaml
- uses: actions/github-script@v7
  with:
    github-token: ${{ secrets.GITHUB_TOKEN }}
    script: |
      const run = await github.rest.actions.getWorkflowRun({
        owner: context.repo.owner, repo: context.repo.repo, run_id: context.runId,
      })
      core.info(`run ${run.data.run_number} is ${run.data.status}`)
```

Responses carry the `X-GitHub-Media-Type` header. Errors are `{"message": ..., "documentation_url": ..., "errors": [...]}` with GitHub's status codes: 404 hides repositories the caller may not see, 410 answers a disabled unit, 403 with `Resource not accessible by integration` answers a missing write level, 422 answers validation failures. Lists take `page` and `per_page` (at most 100) and answer a `Link` header.

Ids are integers derived from the row ids, within the safe JavaScript range. Paths accept either the integer or the row id, and every object carries a `_distroface` member with the row ids.

## Routes

### Repository

| Method and path | Notes |
| --- | --- |
| `GET /user` | The caller |
| `GET /rate_limit` | A constant 5000 budget, never enforced |
| `GET /repos/{owner}/{repo}` | |
| `GET /repos/{owner}/{repo}/branches/{branch}` | |
| `GET /repos/{owner}/{repo}/commits/{ref}` | |
| `GET /repos/{owner}/{repo}/commits/{ref}/statuses` | Statuses of a commit |
| `GET /repos/{owner}/{repo}/commits/{ref}/status` | Combined status |
| `POST /repos/{owner}/{repo}/statuses/{sha}` | `state` (`error`, `failure`, `pending`, `success`), `context`, `description`, `target_url`. Write on the code unit. |
| `GET /repos/{owner}/{repo}/git/ref/{ref}`, `GET .../git/refs/{ref}` | |
| `GET /repos/{owner}/{repo}/contents/{path}` | A file with base64 `content` (`encoding: none` past the inline limit) or a directory listing, `?ref=` selects the revision |

### Releases

| Method and path | Notes |
| --- | --- |
| `GET /repos/{owner}/{repo}/releases` | |
| `POST /repos/{owner}/{repo}/releases` | `tag_name`, `target_commitish`, `name`, `body`, `draft`, `prerelease` |
| `GET /repos/{owner}/{repo}/releases/latest` | |
| `GET /repos/{owner}/{repo}/releases/tags/{tag}` | |
| `GET`, `PATCH`, `DELETE /repos/{owner}/{repo}/releases/{id}` | |
| `GET /repos/{owner}/{repo}/releases/{id}/assets` | |
| `POST /repos/{owner}/{repo}/releases/{id}/assets?name=<file>&label=` | Body is the asset, `Content-Type` is stored, an existing name is refused |
| `GET`, `PATCH`, `DELETE /repos/{owner}/{repo}/releases/assets/{id}` | `GET` with `Accept: application/octet-stream` returns the file instead of its metadata |

### Issues and pull requests

| Method and path | Notes |
| --- | --- |
| `GET /repos/{owner}/{repo}/issues` | `state` (`open`, `closed`, `all`), `labels`, `creator`, `assignee`, `milestone` |
| `POST /repos/{owner}/{repo}/issues` | `title`, `body`, `labels`, `assignees`, `assignee`, `milestone` |
| `GET`, `PATCH /repos/{owner}/{repo}/issues/{number}` | `PATCH` takes `title`, `body`, `state`, `labels`, `assignees`, `assignee`, `milestone` |
| `GET`, `POST /repos/{owner}/{repo}/issues/{number}/comments` | `body` |
| `GET`, `PATCH`, `DELETE /repos/{owner}/{repo}/issues/comments/{id}` | |
| `GET`, `POST`, `PUT /repos/{owner}/{repo}/issues/{number}/labels` | `POST` adds, `PUT` replaces |
| `DELETE /repos/{owner}/{repo}/issues/{number}/labels/{name}` | |
| `GET /repos/{owner}/{repo}/labels` | |
| `GET /repos/{owner}/{repo}/pulls` | |
| `POST /repos/{owner}/{repo}/pulls` | `title`, `body`, `head` (`branch` or `owner:branch`), `base`, `draft`, `maintainer_can_modify` |
| `GET /repos/{owner}/{repo}/pulls/{number}` | |
| `GET`, `POST /repos/{owner}/{repo}/pulls/{number}/comments` | Conversation comments, the same as the issue comments |

Issues and pull requests number separately on the instance. A `{number}` in these routes is looked up as an issue first and falls back to the pull request with that number.

### Actions

| Method and path | Notes |
| --- | --- |
| `GET /repos/{owner}/{repo}/actions/runs/{run_id}` | By row id, integer id or run number |
| `GET /repos/{owner}/{repo}/actions/runs/{run_id}/jobs` | Jobs with steps, labels and runner name |
| `GET /repos/{owner}/{repo}/actions/runs/{run_id}/artifacts` | `?name=` filters |
| `GET /repos/{owner}/{repo}/actions/artifacts` | Live artifacts of the repository, `?name=` filters |
| `GET /repos/{owner}/{repo}/actions/artifacts/{artifact_id}` | By artifact number |
| `GET /repos/{owner}/{repo}/actions/artifacts/{artifact_id}/zip` | 302 to a signed download link, see [Artifacts and cache](artifacts-and-cache.md) |

Run status values are `waiting`, `queued`, `in_progress` and `completed`. The actions routes answer 503 when Actions is not configured on the instance.

## Everything else

Every other GitHub path answers 404 and `github.graphql_url` is empty. Run objects carry `cancel_url`, `rerun_url`, `logs_url` and `workflow_url` the way GitHub does, but those routes are not served. The native API covers that ground: `ActionsService` lists, dispatches, cancels and reruns workflows, `GitFileService` records commit statuses, `IssueService` searches, and `UserService` and `OrganizationService` manage accounts, see [Services](../api/services.md).

# API overview

The control plane is ConnectRPC. Every method is a `POST` to `/distroface.v1.<Service>/<Method>` with a JSON body, so any HTTP client works. Bytes move over plain HTTP data planes listed below.

## Calling a method

```bash
curl -X POST http://localhost:8080/distroface.v1.GitService/ListGitRepositories \
  -H 'Authorization: Bearer df_...' \
  -H 'Content-Type: application/json' \
  -d '{"page":{"pageSize":50}}'
```

Field names are accepted in `snake_case` or `camelCase`, responses use `camelCase`. Request bodies are capped at 64 MiB. Streaming methods such as `ActionsService.StreamJobLog` use the Connect streaming protocol. The interactive console with an OpenAPI download is in the UI at `/docs/api`. Every service is listed in [Services](services.md). The protos under `proto/distroface/v1` are the source of truth.

## Authentication

Send a bearer token in the `Authorization` header. Three kinds work:

| Token | Source | Lifetime |
| --- | --- | --- |
| Session token | `AuthService.Login`, `AuthService.RefreshSession`, the OIDC callback | `auth.session_timeout_seconds`, default one day |
| Personal access token, `df_...` | Settings > Tokens or `dfcli token create` | Until revoked or its expiry, see [Tokens](../administration/tokens.md) |
| Job token | Injected into Actions jobs as `GITHUB_TOKEN` | The job |

The UI sends the session as the `distroface_session` cookie, which the server also accepts. Login, register and password change are throttled per client ip, see [Users and roles](../administration/users-and-roles.md).

Without a token the caller is anonymous. Anonymous calls succeed only when `auth.anonymous_access` is on, or the method is public such as `HealthService.HealthCheck` and `AuthService.Login`. When no auth provider is enabled at all, every caller is treated as admin.

A scoped token reaches its repository or organization, read only lists, and its own identity. Every other method answers `permission_denied` with `credential scope does not cover this procedure`.

## Authorization

Each method maps to a resource and action in the RBAC tables, with an optional object id taken from the request. The server denies a call when the caller's roles hold no matching grant, and refuses any method without a mapping. Org and repository level rules are applied inside the service after the role check. See [Users and roles](../administration/users-and-roles.md).

## Pagination

List methods take a `page` object and return a `page` object:

| Field | Description |
| --- | --- |
| `page.page_size` | Default 20, hard cap 500 |
| `page.page_token` | Opaque cursor from the previous response, empty for the first page |
| `page.query.text` | Free text matched against the endpoint's default fields |
| `page.query.filters[]` | `field`, `match` (`CONTAINS` default, `EQUALS`, `PREFIX`), `value`, all must match |
| `page.order_by` | `"column direction"`, columns allowlisted per endpoint |
| response `page.next_page_token` | Empty on the last page |
| response `page.total_count` | Rows matching the filters |

## Errors

Errors use Connect codes: `unauthenticated`, `permission_denied`, `not_found`, `invalid_argument`, `already_exists`, `failed_precondition`, `resource_exhausted` for lockouts and rate limits, `unimplemented` when a feature is off. The body is `{"code": "...", "message": "..."}` with the fix in the message where there is one.

## Reflection

gRPC reflection is mounted for tools such as `grpcurl` and `buf curl`. It requires a valid token.

## HTTP data planes

| Path | Purpose |
| --- | --- |
| `/v2/` | OCI distribution API, see [Registry](../registry/overview.md) |
| `/auth/token` | Docker token endpoint |
| `/<ns>/<name>[.git]/info/refs`, `git-upload-pack`, `git-receive-pack` | Git smart http, `.wiki` suffix for the wiki, see [Cloning](../git/cloning.md) |
| `/<ns>/<name>[.git]/info/lfs/` | Git LFS batch and transfer, see [LFS](../git/lfs.md) |
| `/<ns>/<name>/-/releases/<tag>/assets`, `/<ns>/<name>/releases/download/` | Release asset upload and download, see [Releases](../git/releases.md) |
| `/<ns>/<name>/-/raw/<ref>/<path>`, `/<ns>/<name>/-/archive/<ref>.zip` | Raw files and repository archives, see [Code](../git/code.md) |
| `/<ns>/<name>/-/actions/artifacts/` | Workflow artifact downloads, see [Artifacts and cache](../actions/artifacts-and-cache.md) |
| `/api/v1/artifacts/`, `/api/v1/auth/` | Legacy artifact facade, see [v1 API](../artifacts/v1-api.md) |
| `/api/v3/`, rest of `/api/v1/` | GitHub flavored REST for octokit tooling, see [GitHub API](../actions/github-api.md) |
| `/api/actions/cache/`, `/api/actions/artifacts/`, `/api/actions_pipeline/`, `/twirp/github.actions.results.api.v1.ArtifactService/` | Actions cache and artifact protocols, see [Artifacts and cache](../actions/artifacts-and-cache.md) |
| `/api/v1/auth/oidc/login`, `/api/v1/auth/oidc/callback` | OIDC redirect flow, see [OIDC](../administration/oidc.md) |
| `/api/avatars/{id}` | Uploaded profile images, public and cached |
| `/acme/` | Built in ACME directory, see [Certificate authority](../administration/certificate-authority.md) |
| `/.well-known/distroface/ca.pem` | Instance trust bundle |
| `/debug/pprof/` | Only on the `DISTROFACE_PPROF_ADDR` listener, see [Logging and profiling](../getting-started/logging-and-profiling.md) |

Data plane requests take the same bearer tokens. Git and the registry also take HTTP basic auth with a password or personal access token.

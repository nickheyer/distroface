# Registry

DistroFace embeds distribution v3 and serves the OCI distribution API under `/v2/` on the main listener. Every image lives under a namespace that is a user or an organization, and access follows the same roles, memberships and tokens as the rest of the app.

## Namespaces and names

An image is addressed as `<host>/<namespace>/<name>:<tag>`. The namespace is a username or an organization name. A repository record is created automatically on the first manifest push, owned by the user or organization that owns the namespace. A repository created by a push is public unless a git repository with the same name exists in the namespace, in which case the package links to it and copies its visibility. See [Linked repositories](../git/linked-repositories.md).

Repositories can also be created ahead of a push with `RepositoryService.CreateRepository`, which is required for mirrors. Names given to that RPC must match `^[a-z0-9]+(?:[._-][a-z0-9]+)*$`. Namespace owners and organization owners or admins create freely, anyone else needs the `manage` action on `repositories`.

## Logging in

`docker login` talks to `GET` or `POST /auth/token`, the Docker token endpoint. The bearer challenge on `/v2/` points at `/auth/token` on the host that was asked, so the same flow works on every hostname and [portal](../administration/portals.md).

```bash
docker login registry.example.com
```

The password is either the account password or an API token starting with `df_`, see [Tokens](../administration/tokens.md). Tokens are signed with ES256 for the service `distroface-registry` and expire after `auth.token_expiry_seconds`, 900 by default. Too many failed credentials from one address trip the login lockout and the endpoint answers 429 with `Retry-After: 60`. A deactivated account fails the same way as a wrong password. Successful logins and denied credentials are recorded in the [audit log](../administration/audit.md) as `Registry/login`.

If the instance uses its own certificate authority, `dfcli trust install` places the instance CA in Docker's per registry trust store, see the [CLI reference](../cli/reference.md).

## Pushing an image

```bash
docker tag app:latest registry.example.com/myorg/app:1.0
docker push registry.example.com/myorg/app:1.0
```

Push is granted to the namespace owner, to any member of the organization, and to users whose roles carry `manage` on `repositories` for an existing user or organization namespace. Anonymous users never push. A successful manifest push bumps the push counter, fires the `push` [webhook](../webhooks.md), writes a `Registry/push` audit row and lands in the activity feed. Manifests are validated before they are stored, see [Manifests](manifests.md).

Pushes made with a workflow job token are attributed to the run, and the registry token stops working once the job finishes, see [Actions](../actions/overview.md).

## Pulling an image

```bash
docker pull registry.example.com/myorg/app:1.0
```

For a public repository the caller needs the `pull` action on `repositories`, which the default `anonymous` role carries, or a namespace relationship. For a private repository the caller must be the owner, a member of the organization, someone the linked git repository admits, a holder of `manage`, or a role with an explicit grant on `<namespace>/<name>` or `<namespace>`. Roles are described in [Users and roles](../administration/users-and-roles.md). Manifest pulls bump the pull counter, fire the `pull` webhook and write a `Registry/pull` audit row.

## Anonymous pulls

Requests without credentials get an anonymous token when `auth.anonymous_access` is on, or when the portal serving the host allows it. The anonymous identity carries the `anonymous` role, which by default has `read` and `pull` on `repositories`, so public images pull without logging in. Anonymous requests for a repository that does not exist are refused, and anonymous users never receive push, delete or catalog scopes.

## Tags and deletion

`GET /v2/<name>/tags/list` returns at most 1000 tags per page. `DELETE /v2/<name>/manifests/<tag>` removes the tag and `DELETE /v2/<name>/manifests/<digest>` removes the manifest, both answer 202. Deleting a tag that does not exist returns `MANIFEST_UNKNOWN`. Blobs stay on disk until [garbage collection](garbage-collection.md). Delete is granted to the namespace owner, organization owners and admins, holders of `manage` and explicit grants, and only on repositories that already exist. Deletes fire the `delete` webhook and write a `Registry/delete` audit row.

## Catalog and scopes

The token endpoint grants `repository:<namespace>/<name>:pull,push,delete` scopes filtered by the rules above. The `registry:catalog:*` scope behind `GET /v2/_catalog` is granted only to roles with `manage` on `repositories`, never to anonymous or scoped credentials, and never on a portal host. The catalog returns at most 1000 entries per page and rejects a larger `n` with `PAGINATION_NUMBER_INVALID`. Credentials scoped to a git repository or an organization only receive scopes inside that object, and read only credentials never receive push or delete.

## Pull rate limits

| Setting | Default | Description |
| --- | --- | --- |
| `rate_limit.pull_per_minute` | `0` | Manifest pulls per minute per authenticated user, zero disables |
| `rate_limit.anon_pull_per_minute` | `0` | Manifest pulls per minute per client address for anonymous tokens, zero disables |

Limits apply to `GET` and `HEAD` manifest requests that carry a bearer token. Every limited response sets `X-RateLimit-Limit`, `X-RateLimit-Remaining` and `X-RateLimit-Reset`. Over the limit the registry answers 429 with `TOOMANYREQUESTS` and a `Retry-After` header. With a [shared cache](../getting-started/cache.md) the counts hold across instances.

## Visibility

`RepositoryService.UpdateRepository` changes a repository between public and private and edits its description. The owner, organization owners and admins, and holders of `manage` may change it. Private repositories are hidden from listings for everyone without a relationship to them.

## Image repositories in the UI and API

Images is a top level navigation entry. `/images` lists repositories the viewer may see, and `/images/<namespace>/<name>` shows tags with digest, size, platforms and the manifest media type. Tags that match a release of the linked git repository carry a release marker.

| RPC | Permission | Description |
| --- | --- | --- |
| `RepositoryService.ListRepositories` | `repositories` read | Paginated, filtered by namespace, private ones only with a relationship |
| `RepositoryService.GetRepository` | `repositories` read | One repository with `source_repository` set when linked |
| `RepositoryService.ListTags` | `repositories` read | Tags ordered by `version desc` by default, also `name`, `size` and `pushed_at` |
| `RepositoryService.ResolveTag` | `repositories` read | Manifest descriptor with children and parsed image config |
| `RepositoryService.CreateRepository` | `repositories` create | Create ahead of a push, mirrors need it |
| `RepositoryService.UpdateRepository` | `repositories` update | Description, visibility and mirror settings |
| `RepositoryService.DeleteRepository` | `repositories` delete | Removes the repository record and its stars |
| `RepositoryService.MoveRepository` | `repositories` update | Rename or transfer, refused for linked packages and while uploads are in flight |
| `RepositoryService.SyncRepository`, `StopRepositorySync` | `repositories` update | Mirror control, see [Mirrors](../mirrors.md) |
| `RepositoryService.StarRepository`, `UnstarRepository`, `ListStarredRepositories` | signed in | Stars |

```bash
curl -X POST https://registry.example.com/distroface.v1.RepositoryService/ListTags \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"namespace": "myorg", "name": "app", "page": {"pageSize": 50}}'
```

`dfcli image list` prints the first 100 repositories as JSON and `dfcli image tags myorg/app` prints the first 100 tags, see the [CLI reference](../cli/reference.md).

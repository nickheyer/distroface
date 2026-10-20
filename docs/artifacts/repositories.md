# Artifact repositories

An artifact repository holds versioned files that are not container images. Each repository lives under a namespace, which is either a username or an organization name.

## Namespaces and names

A repository is addressed as `namespace/name`. Names match `^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`. When a request omits the namespace, it defaults to the caller's username.

Read style RPCs (`GetArtifactRepository`, `ListArtifacts`, `SearchArtifacts`, `InitiateArtifactUpload` and the other repo scoped calls) resolve a bare name in two steps. The caller's own namespace is tried first. If nothing is there, the name is matched against every repository the caller can see. One match is used, more than one is rejected with an `InvalidArgument` error that lists the qualified candidates. Destructive RPCs (`UpdateArtifactRepository`, `DeleteArtifactRepository`, `DeleteArtifact`, `UpdateArtifact`, `SetArtifactProperties`) never fall back, they only look in the caller's namespace unless one is given.

## Creating a repository

Create a repository from the Artifacts page in the UI, with dfcli, or with `ArtifactService.CreateArtifactRepository`.

```bash
dfcli artifact create builds -d "CI build outputs"
dfcli artifact create acme/builds --private
dfcli artifact create builds --namespace acme
```

```bash
curl -X POST https://df.example.com/distroface.v1.ArtifactService/CreateArtifactRepository \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"builds","namespace":"acme","description":"CI build outputs","is_private":false}'
```

| Parameter | Description |
|---|---|
| `name` | Repository name, required |
| `namespace` | Username or organization, defaults to the caller |
| `description` | Free text |
| `is_private` | Hide the repository from users without access |
| `type` | `ARTIFACT_REPO_TYPE_FILE` by default, release mirror types take a `mirror` block, see [mirrors](../mirrors.md) |

A user can create in their own namespace, in any organization they belong to, or, with the `artifacts` `manage` permission, in any existing user or organization namespace. The caller needs the `artifacts` `create` action. Creating an existing `namespace/name` fails with `AlreadyExists`.

## Visibility

Public repositories are visible to anyone holding the `artifacts` `read` action, which the default `anonymous` role has. Private repositories are visible only to principals that pass the access rules below.

`is_private` is taken from the request. When it is false and the target namespace is not the caller's own username, the effective `artifacts.private_by_default` setting for that namespace decides. A repository created with the same name as a private git repository in the same namespace is always created private.

Change visibility and description later with `ArtifactService.UpdateArtifactRepository` or from the repository settings page in the UI (Artifacts > repository > Settings).

## Listing repositories

`ArtifactService.ListArtifactRepositories` returns the repositories the caller can see, sorted by name. `namespace` narrows the list to one namespace. `page.query.text` matches name, namespace and description, and `page.query.filters` accept the fields `name`, `namespace` and `description`. Pagination follows [pagination](../api/overview.md).

```bash
dfcli artifact list
dfcli artifact list --namespace acme
```

Every repository in a list or get response carries `artifact_count` and `total_size` (bytes), computed from the stored artifacts, plus `star_count` and `is_starred`.

## Linked git repository

An artifact repository with the same name as a git repository in the same namespace links to it. The link is made when the artifact repository is created, or when the git repository is created by a user who manages the artifact repository. A linked repository reports the git repository in `source_repository`, shares the git repository's access rules, and cannot be moved or renamed on its own. Deploy keys only reach artifact repositories through such a link. See [linked repositories](../git/linked-repositories.md) for linking and unlinking by hand.

## Moving a repository

`ArtifactService.MoveArtifactRepository` renames a repository or transfers it to another namespace. It needs `manage` access on the repository and create rights in the target namespace. Linked repositories are refused, move the git repository instead.

## Deleting a repository

`ArtifactService.DeleteArtifactRepository` removes the repository, every artifact in it, and every blob no other artifact references. The same happens from the Artifacts list page in the UI. The v1 route is `DELETE /api/v1/artifacts/repos/{name}`, see [v1 API](v1-api.md). dfcli has no repository delete command.

## Access rules

Every repository operation first requires the matching `artifacts` action in RBAC (`read`, `create`, `update`, `delete`, `push`, `pull`, `manage`). The default `user` role holds all but `manage`, `anonymous` holds `read` and `pull`. See [users and roles](../administration/users-and-roles.md).

For a private repository, or for any mutation, the caller must also pass one of these checks, in order:

| Check | Grants |
|---|---|
| Repository owner | Everything |
| Linked git repository allows the action | Everything the git repository would allow |
| `artifacts` `manage` permission | Everything |
| Namespace equals the caller's username | Everything |
| Organization member | `read`, `pull`, `push` for any member, other actions for org owners and admins |
| Scoped RBAC grant on `namespace/name` | The granted action |

Scoped credentials (tokens limited to one repository or namespace) are checked against their scope before any of the above.

## Settings

Runtime settings live under `artifacts` in [settings](../administration/settings.md). System values are changed with `SettingsService.UpdateSettings`. Organizations override the marked rows under Org > Settings.

| Setting | Default | Description |
|---|---|---|
| `artifacts.max_file_size_mb` | `10240` | Largest accepted upload, zero means unlimited, org override |
| `artifacts.private_by_default` | `false` | New repositories in the namespace start private, org override |
| `artifacts.stale_upload_cleanup_hours` | `24` | Age before an abandoned upload session is removed, see [uploads](upload-and-download.md) |
| `artifacts.v1_compat` | `true` | Serves the HTTP routes under `/api/v1/artifacts`, system only, see [v1 API](v1-api.md) |
| `artifacts.retention.*` | | Pruning rules, org override, see [retention](retention.md) |
| `artifacts.reaper.*` | | Scheduled sweep, system only, see [retention](retention.md) |

Blobs are stored on disk under `artifacts.storage_path` from the config file, which defaults to `<storage.data_dir>/artifacts`. Content is addressed by sha256 digest, so identical files uploaded to different repositories share one blob.

# Releases

A release is a git tag with notes, uploaded asset files, and container images from the same namespace. Asset files are artifacts in the artifact repository that shares the git repository's name, see [Where assets live](#where-assets-live). Releases need write access on the releases unit to create or change and read access to view.

## Creating a release

Create one from Repo > Releases > New release, with `ReleaseService.CreateRelease`, or with `dfcli release create` (see [CLI reference](../cli/reference.md)).

```bash
curl -X POST https://forge.example.com/distroface.v1.ReleaseService/CreateRelease \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"namespace":"acme","name":"api","tag":"v1.2.0","target":"main","title":"v1.2.0","body":"Notes","prerelease":false}'
```

```bash
dfcli release create acme/api v1.2.0 --title "v1.2.0" --body "Notes" --target main
```

| Field | Description |
| --- | --- |
| `tag` | Required, must be a valid tag name, one release per tag |
| `target` | Branch or sha the tag is created at when it does not exist yet, empty uses the default branch |
| `title` | Empty uses the tag |
| `body` | Markdown notes |
| `draft` | Hidden from everyone without write access |
| `prerelease` | Excluded from the latest release |

When the tag does not exist it is created at `target`. Mirrors cannot create tags, so a release on a mirror needs an existing tag. An existing annotated tag resolves to the commit it points at. Publishing notifies watchers (`released`), fires the `git_release` webhook, and fires the `release` workflow event, see [Workflows](../actions/workflows.md). `GenerateReleaseNotes` drafts a body from merged pull requests and commits since the previous tag, picked by natural version order when not given.

## Drafts and prereleases

Drafts are invisible to readers: `GetRelease` answers not found, `ListReleases` only includes them for writers with `include_drafts`, and asset downloads 404. `PublishRelease` (or `UpdateRelease` with `draft: false`) sets `published_at` and announces the release. `GetLatestRelease` returns the newest published release that is not a prerelease, ordered by publish time.

## Releases from tags

With `release_on_tag` on, pushing an annotated tag creates a draft release for it. The first line of the tag message is the title and the rest is the body. Lightweight tags and tags that already have a release are skipped.

| Setting | Default | Description |
| --- | --- | --- |
| `git.release_on_tag` | `false` | Annotated tag pushes create draft releases |

It lives in the git settings, see [Settings](../administration/settings.md).

## Where assets live

Release assets are not a separate store. Each asset is an artifact in the artifact repository `<namespace>/<name>`, the package linked to the git repository by name (see [Linked repositories](linked-repositories.md)). The package is created on the first upload when it does not exist yet, as a file repository that is private when the git repository is private. An existing unlinked package of that name is linked on the first upload, and made private when the git repository is private. Linking shares the package with the git repository's readers and writers, so it takes manage access on that package: an upload by anyone else, or by a mirror sync, is refused with `is not linked to this repository` until someone who manages the package uploads an asset or links it by hand. Making the git repository private also makes that package private. Reading a release never changes the package.

When the artifact repository of that name is linked by hand to a different git repository, it is not this repository's package: its releases list no assets and asset uploads are refused with `belongs to another source repository`. Unlink or rename that package to use assets here.

| Artifact field | Value for a release asset |
| --- | --- |
| `version` | The release tag. `/` is stored as `%2F` and `%` as `%25` because versions take no slashes. A tag longer than 128 characters in that form cannot carry assets |
| `path`, `name` | The asset file name |
| `properties` | `release=true`, the flag that makes an artifact an asset |
| `label` | Optional display name |
| `download_count` | Downloads through any route |

`Release.assets` is a list of the same `Artifact` message `ArtifactService` returns. Everything that applies to artifacts applies to assets: they show up on the package page and in `SearchArtifacts`, they download through `/api/v1/artifacts`, they count against `artifacts.max_file_size_mb`, and the package's [retention](../artifacts/retention.md) rules prune them like any other artifact. An artifact uploaded to the package at the tag version becomes an asset of that release once it carries the `release=true` property, and stops being one when the property is removed. Artifacts at the tag version without the flag stay plain artifacts and are left alone when the release is deleted.

Access through the release routes follows the git repository's releases unit. Access through the artifact routes follows the package, which a link shares with the git repository's readers and writers. Draft releases hide their assets on the release routes only, so keep the package private when drafts must stay private.

## Uploading assets

Assets stream over HTTP, one file per request, with the file as the raw body:

```text
POST /<namespace>/<name>/-/releases/<tag>/assets?name=<file>&label=<label>&replace=1
Authorization: Bearer <token>
Content-Type: application/octet-stream
```

| Parameter | Description |
| --- | --- |
| `name` | Required file name. No path separators, no control characters, up to 255 bytes |
| `label` | Optional display label shown instead of the name |
| `replace` | `1` or `true` replaces an existing asset of the same name, keeping its id, label and download count |

```bash
curl -X POST "https://forge.example.com/acme/api/-/releases/v1.2.0/assets?name=api-linux-amd64.tar.gz&label=Linux%20amd64" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/gzip" \
  --data-binary @api-linux-amd64.tar.gz
```

```bash
dfcli release upload acme/api v1.2.0 api-linux-amd64.tar.gz --label "Linux amd64" --replace
```

The response is `201` with the stored `Artifact` as JSON. A duplicate name without `replace` answers `422`, and a body over the effective `artifacts.max_file_size_mb` answers `413`. The content type comes from the request header, or from the file extension when the header is empty or `application/octet-stream`. Uploads to drafts work for writers only. `UpdateReleaseAsset` renames or relabels an asset and `DeleteReleaseAsset` removes the artifact along with its blob when nothing else references it. Both take the artifact id as `asset_id`.

## Downloading assets

Every asset downloads at `/<namespace>/<name>/releases/download/<tag>/<file>`, where `<file>` is the artifact `name`. The same file is also served at `/<namespace>/<name>/-/releases/<tag>/<file>`. Both accept `GET` and `HEAD`, send the file as an attachment with an ETag of its digest, and count each full `GET` in the artifact's `download_count`. Requests with a `Range` header and cache revalidations answered with `304` are not counted. Source archives are at `/<namespace>/<name>/-/archive/<tag>.zip` and `.tar.gz`.

```bash
dfcli release download acme/api v1.2.0 -o ./dist --asset "*.tar.gz"
```

## Container images

A release lists images from the same namespace in two ways:

- Linked image repositories (see [Linked repositories](linked-repositories.md)) that have a tag equal to the release tag appear automatically.
- `AttachReleaseImage` pins an explicit `image_name` and `image_tag`. The image repository must be in the release's namespace and either linked to the git repository or manageable by the caller, and the tag must exist in the registry. Pinned images show `pinned: true` and `DetachReleaseImage` removes them.

```bash
dfcli release attach-image acme/api v1.2.0 api:v1.2.0
```

Each image carries its digest, platforms, size, a `docker pull` command and a link to the image page.

## Deleting a release

`DeleteRelease` removes the release and its asset artifacts. With `delete_tag` the git tag is deleted as well, except on mirrors. `dfcli release delete acme/api v1.2.0 --delete-tag` does the same.

## dfcli commands

| Command | Description |
| --- | --- |
| `dfcli release create namespace/name tag` | `--title`, `--body`, `--target`, `--draft`, `--prerelease` |
| `dfcli release list namespace/name` | `-t`, `--drafts`, `--prereleases` (default true) |
| `dfcli release view namespace/name tag` | Show one release with assets and images |
| `dfcli release upload namespace/name tag file...` | `--label`, `--replace` |
| `dfcli release download namespace/name tag` | `-o dir`, `--asset glob` |
| `dfcli release attach-image namespace/name tag image:tag` | Pin an image from the same namespace |
| `dfcli release delete namespace/name tag` | `--delete-tag` |

# Releases

A release is a git tag with notes, uploaded asset files, and container images from the same namespace. Releases need write access on the releases unit to create or change and read access to view.

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
| `git.max_release_asset_mb` | `2048` | Per file cap on asset uploads, `0` removes the cap |

Both live in the git settings, see [Settings](../administration/settings.md).

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
| `replace` | `1` or `true` replaces an existing asset of the same name, keeping its id, created time and download count |

```bash
curl -X POST "https://forge.example.com/acme/api/-/releases/v1.2.0/assets?name=api-linux-amd64.tar.gz&label=Linux%20amd64" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/gzip" \
  --data-binary @api-linux-amd64.tar.gz
```

```bash
dfcli release upload acme/api v1.2.0 api-linux-amd64.tar.gz --label "Linux amd64" --replace
```

The response is `201` with the `ReleaseAsset` as JSON. A duplicate name without `replace` answers `422`, and a body over `max_release_asset_mb` answers `413`. The content type comes from the request header, or from the file extension when the header is empty or `application/octet-stream`. Uploads to drafts work for writers only. `UpdateReleaseAsset` renames or relabels an asset and `DeleteReleaseAsset` removes it along with its blob when nothing else references it.

## Downloading assets

Every asset has a `download_url` of the form `/<namespace>/<name>/releases/download/<tag>/<file>`. The same file is also served at `/<namespace>/<name>/-/releases/<tag>/<file>`. Both accept `GET` and `HEAD`, send the file as an attachment with an ETag of its digest, and count each `GET` in `download_count`. Source archives are at `/<namespace>/<name>/-/archive/<tag>.zip` and `.tar.gz`.

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

`DeleteRelease` removes the release and its asset files. With `delete_tag` the git tag is deleted as well, except on mirrors. `dfcli release delete acme/api v1.2.0 --delete-tag` does the same.

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

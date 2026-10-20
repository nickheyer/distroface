# Uploading and downloading artifacts

Artifact bytes move over plain HTTP under `/api/v1/artifacts`. RPCs open and close an upload, the file itself is sent with a PATCH. Downloads are plain GET requests.

The HTTP routes are served by the v1 facade and need `artifacts.v1_compat` enabled (the default). With it off every transfer returns 404. See [v1 API](v1-api.md).

## Uploading a file

An upload is three calls.

1. `ArtifactService.InitiateArtifactUpload` opens a session and returns `upload_id` and `upload_url`.
2. `PATCH` the file bytes to `upload_url`. Each PATCH appends to the session, so a file may be sent in one request or several.
3. `ArtifactService.CompleteArtifactUpload` hashes the session, stores the blob, and records the artifact.

```bash
curl -X POST https://df.example.com/distroface.v1.ArtifactService/InitiateArtifactUpload \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"repo_name":"builds","namespace":"acme"}'
# {"uploadId":"3f1c...","uploadUrl":"/api/v1/artifacts/_ns/acme/builds/upload/3f1c..."}

curl -X PATCH https://df.example.com/api/v1/artifacts/_ns/acme/builds/upload/3f1c... \
  -H "Authorization: Bearer $TOKEN" --data-binary @app-1.4.2.tar.gz

curl -X POST https://df.example.com/distroface.v1.ArtifactService/CompleteArtifactUpload \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"repo_name":"builds","namespace":"acme","upload_id":"3f1c...","version":"1.4.2","path":"linux/app.tar.gz","properties":{"os":"linux","arch":"amd64"}}'
```

| Parameter | Description |
|---|---|
| `repo_name`, `namespace` | Target repository, bare names resolve as described in [repositories](repositories.md) |
| `upload_id` | Session returned by initiate |
| `version` | Required, at most 128 characters, no `/` or `\` |
| `path` | Relative path inside the version, at most 255 characters, no leading `/`, no `..`, no `\`. Defaults to the repository name with unsafe characters replaced by `-` |
| `properties` | Key value strings, see [search and properties](search-and-properties.md) |
| `metadata` | JSON object string, defaults to `{}`, rejected when not valid JSON |

Upload sessions belong to the principal that opened them. A PATCH or complete from a different user is refused, any token of the same user may continue the session. The `upload_url` carries the `_ns/<namespace>` marker whenever the repository is outside the caller's own namespace.

On complete the server detects the MIME type from the first 512 bytes, computes the sha256 digest, and moves the session into blob storage. An identical blob already on disk is reused. If the session exceeds the effective `artifacts.max_file_size_mb`, the session is cancelled and the call fails with `InvalidArgument`.

An artifact is identified by repository, version, path and property set. Completing an upload with the same four values replaces the earlier artifact and releases its blob when nothing else references it. Retention rules run for the repository after every completed upload, see [retention](retention.md).

With dfcli, see [the CLI reference](../cli/reference.md):

```bash
dfcli artifact upload builds ./dist/app-1.4.2.tar.gz -v 1.4.2 -p linux/app.tar.gz --property os=linux --property arch=amd64
dfcli artifact upload acme/builds ./dist/app.tar.gz
```

When `-v` is omitted the version is the file name, and when `-p` is omitted the path is the file name. dfcli replaces characters outside word characters, dash and dot in the version with `_` and does the same to each path segment.

## Downloading with a query

`GET /api/v1/artifacts/<name>/query` or `GET /api/v1/artifacts/_ns/<namespace>/<name>/query` selects artifacts and streams them as one archive. The caller needs the `artifacts` `pull` action and visibility of the repository.

| Parameter | Description |
|---|---|
| `version` | Substring match on version |
| `path` | Substring match on path |
| `name` | Substring match on file name |
| `num` | Number of artifacts to include, default `1` |
| `sort` | `name`, `version`, `path`, `size`, `created_at` or `updated_at`, default `created_at` |
| `order` | `ASC` or `DESC`, default `DESC` |
| `format` | `zip` or `tar.gz`, anything else becomes `zip` |
| `flat` | `1` names each entry by file name only, otherwise entries are `<version>/<path>` |

Any other query key is treated as a property filter and must equal the stored value exactly. The keys `num`, `format`, `name`, `version`, `upload_id`, `path`, `sort`, `order`, `flat` and `kind` are reserved. With no match the response is 404.

```bash
curl -L -o latest.zip -H "Authorization: Bearer $TOKEN" \
  "https://df.example.com/api/v1/artifacts/_ns/acme/builds/query?version=1.4&os=linux&num=3&format=zip"
```

```bash
dfcli artifact download builds -v 1.4.2 -o ./out
dfcli artifact download acme/builds --property os=linux --num 3 --sort created_at --order DESC --format tar.gz
dfcli artifact download builds -p linux/app.tar.gz --unpack --flat -o ./out
```

Without `--unpack` dfcli writes `<repo>_artifacts.<format>` into `-o` (default the current directory). With `--unpack` it extracts the archive there, descending into nested archives, and `--flat` drops directories.

## Downloading one file

`GET /api/v1/artifacts/<name>/<version>/<path>` (or the `_ns` form) returns a single artifact with `Content-Disposition: attachment`. The stored MIME type is used, falling back to the file extension and then to content sniffing. The download button on the repository page in the UI uses this route.

```bash
curl -O -H "Authorization: Bearer $TOKEN" \
  https://df.example.com/api/v1/artifacts/_ns/acme/builds/1.4.2/linux/app.tar.gz
```

## Deleting an artifact

`ArtifactService.DeleteArtifact` removes one artifact by `id`, or by `version` and `path` when `id` is empty. The blob is deleted once no artifact references it. The caller needs `delete` access on the repository.

```bash
dfcli artifact delete builds 1.4.2 linux/app.tar.gz
```

## Stale upload cleanup

Sessions that never complete stay under `_uploads` in the artifact storage directory. The retention reaper removes sessions whose last write is older than `artifacts.stale_upload_cleanup_hours` at the end of each sweep.

| Setting | Default | Description |
|---|---|---|
| `artifacts.stale_upload_cleanup_hours` | `24` | Age in hours before an unfinished session is removed, zero disables |

The sweep reads the system value and runs only while `artifacts.reaper.enabled` is on, see [retention](retention.md).

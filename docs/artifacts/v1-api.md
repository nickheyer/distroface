# Artifacts v1 HTTP API

The v1 API is a plain HTTP facade under `/api/v1/artifacts` and `/api/v1/auth` kept for older dfcli releases and CI scripts. It shares the storage, validation and access rules of `ArtifactService`.

The same routes carry the upload and download data plane used by the current RPC flow, see [uploads and downloads](upload-and-download.md).

## Enabling the facade

| Setting | Default | Description |
|---|---|---|
| `artifacts.v1_compat` | `true` | Serves `/api/v1/artifacts` and `/api/v1/auth/login`, `/api/v1/auth/refresh`, system only |

With the setting off every route below returns 404, including the PATCH used by `InitiateArtifactUpload` and the query download. The setting is read on each request, so no restart is needed.

## Authentication

Routes read the `Authorization: Bearer <token>` header, or the `distroface_session` cookie. Any session or API token works, see [tokens](../administration/tokens.md). Without a token the request runs as the anonymous user when anonymous access is enabled, otherwise it fails with 401. The two auth routes mint session tokens with a username and password.

```bash
curl -X POST https://df.example.com/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"alice","password":"secret"}'
# {"token":"...","expires_in":3600,"issued_at":"...","username":"alice","groups":["user"]}

curl -X POST https://df.example.com/api/v1/auth/refresh \
  -H "Content-Type: application/json" \
  -d '{"refresh_token":"<current token>"}'
```

Failed logins count toward the authentication failure lockout configured under `rate_limit`, see [settings](../administration/settings.md). Logins and every mutating route are recorded in the [audit log](../administration/audit.md).

## Qualified names

Routes name a repository by bare name. The namespace is the caller's username unless the path starts with the `_ns` marker:

```text
/api/v1/artifacts/<name>/...
/api/v1/artifacts/_ns/<namespace>/<name>/...
```

The marker is stripped before routing and the namespace is applied to the request. There is no fallback to other namespaces for bare names on these routes. Initiate upload returns a `Location` with the marker whenever the repository is outside the caller's namespace.

## Routes

Every route first checks the matching `artifacts` RBAC action, then the repository access rules from [repositories](repositories.md).

| Method and path | Parameters | Response |
|---|---|---|
| `POST /api/v1/artifacts/repos` | JSON body `name`, `namespace`, `description`, `private` | 201, 409 when the name exists |
| `GET /api/v1/artifacts/repos` | `namespace` query | 200, JSON array of repositories |
| `DELETE /api/v1/artifacts/repos/{repo}` | | 204 |
| `POST /api/v1/artifacts/{repo}/upload` | | 202 with `Location` and `Upload-ID` headers |
| `PATCH /api/v1/artifacts/{repo}/upload/{uuid}` | Raw bytes body, appended | 202 |
| `PUT /api/v1/artifacts/{repo}/upload/{uuid}` | `version` (required) and `path` query, `properties` query as JSON object, JSON object body also read as properties | 201 |
| `GET /api/v1/artifacts/{repo}/{version}/{path}` | | 200 file with `Content-Disposition: attachment` |
| `GET /api/v1/artifacts/{repo}/query` | See [query parameters](upload-and-download.md) | 200 zip or tar.gz, 404 with no match |
| `DELETE /api/v1/artifacts/{repo}/{version}/{path}` | | 204 |
| `GET /api/v1/artifacts/{repo}/versions` | | 200, JSON object keyed by version with artifact arrays |
| `PUT /api/v1/artifacts/{repo}/{id}/metadata` | JSON object body | 200 |
| `PUT /api/v1/artifacts/{repo}/{id}/properties` | JSON object body of string pairs, replaces the set | 200, 409 on identity collision |
| `PUT /api/v1/artifacts/{repo}/{id}/rename` | JSON body `name` (required), `path`, `version` | 200, 409 on collision |
| `GET /api/v1/artifacts/search` | See below | 200 |

`{id}` is the artifact id from a versions or search response. Errors use plain text bodies: 400 for invalid input, 401 `INVALID TOKEN`, 403 for RBAC or access failures, 404 for unknown repositories, artifacts or upload sessions, 405 for a known path with the wrong method.

Metadata updates are allowed for anyone who can see the repository, property and rename updates need `update` access.

## Search

`GET /api/v1/artifacts/search` searches one repository or every visible one.

| Parameter | Description |
|---|---|
| `repo` | Repository name, resolved in `namespace` or the caller's namespace |
| `namespace` | Limits a cross repository search, or qualifies `repo` |
| `name`, `version`, `path` | Substring filters |
| `num` | Result limit, default 9999 |
| `offset` | Rows to skip |
| `sort` | `name`, `version`, `path`, `size`, `created_at` or `updated_at`, default `created_at`, 400 otherwise |
| `order` | `ASC` or `DESC`, default `DESC` |

Any other key is an exact property filter. The reserved keys `username`, `repo`, `namespace`, `num`, `offset`, `archive`, `format`, `name`, `version`, `path`, `sort`, `order` and `kind` are never treated as properties.

```json
{"results":[...],"total":12,"limit":12,"offset":0,"sort":"created_at","order":"DESC"}
```

`total` is the number of rows returned, not the number of matches, and `offset` is always 0 in the response.

## JSON shapes

Repository:

```json
{"id":7,"name":"builds","namespace":"acme","full_name":"acme/builds","description":"","owner":"alice","private":false,"created_at":"...","updated_at":"..."}
```

Artifact:

```json
{"id":"<uuid>","repo_id":7,"name":"app.tar.gz","path":"linux/app.tar.gz","upload_id":"<uuid>","version":"1.4.2","size":1048576,"mime_type":"application/gzip","metadata":"{}","properties":{"os":"linux"},"created_at":"...","updated_at":"..."}
```

## Example

```bash
BASE=https://df.example.com/api/v1/artifacts/_ns/acme/builds
UPLOAD=$(curl -s -D - -o /dev/null -X POST $BASE/upload -H "Authorization: Bearer $TOKEN" | awk '/^Upload-ID:/ {print $2}' | tr -d '\r')
curl -X PATCH $BASE/upload/$UPLOAD -H "Authorization: Bearer $TOKEN" --data-binary @app.tar.gz
curl -X PUT "$BASE/upload/$UPLOAD?version=1.4.2&path=linux/app.tar.gz" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" -d '{"os":"linux"}'
curl -o app.tar.gz -H "Authorization: Bearer $TOKEN" $BASE/1.4.2/linux/app.tar.gz
```

# Searching artifacts and properties

Every artifact carries a version, a path, a free form metadata document and a set of key value properties. Search filters on the first two and on properties, metadata is stored and returned as is.

## Properties

Properties are string pairs attached at upload through the `properties` field of `ArtifactService.CompleteArtifactUpload`. Pairs with an empty key are dropped.

The property set is part of an artifact's identity. Two artifacts in one repository may share a version and path as long as their property sets differ, which lets one version hold the same file name for several platforms. Uploading again with an identical version, path and property set replaces the earlier artifact.

`ArtifactService.SetArtifactProperties` replaces the full set on an existing artifact. The call fails with `AlreadyExists` when the new set would collide with another artifact at the same version and path.

```bash
curl -X POST https://df.example.com/distroface.v1.ArtifactService/SetArtifactProperties \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"repo_name":"builds","namespace":"acme","id":"<artifact id>","properties":{"os":"linux","arch":"arm64"}}'
```

Properties can also be edited from the repository page in the UI. Both paths need `update` access on the repository.

## Metadata

`metadata` is an arbitrary JSON object stored as a string. It is set at upload and replaced through the `metadata` field of `ArtifactService.UpdateArtifact`. An empty string resets it to `{}`. Metadata is not a search filter.

## Renaming an artifact

`ArtifactService.UpdateArtifact` changes `name`, `path` or `version`. Setting `path` also sets `name` to its last segment, setting `name` alone rewrites the last segment of the current path. The same validation as upload applies, and a collision with an existing version and path fails with `AlreadyExists`.

## Searching

`ArtifactService.SearchArtifacts` searches one repository when `repo_name` is set, otherwise every repository the caller can see, optionally limited to a `namespace`.

| Parameter | Description |
|---|---|
| `repo_name`, `namespace` | Limit the search to one repository or one namespace |
| `properties` | Every pair must match a stored property exactly |
| `page.query.text` | Substring matched against name, version and path |
| `page.query.filters` | Field predicates on `name`, `version` or `path` with match `CONTAINS` (default), `EQUALS` or `PREFIX` |
| `page.order_by` | `<field> asc` or `<field> desc`, fields `name`, `version`, `path`, `size`, `created_at`, `updated_at`, default `created_at desc` |
| `page.page_size` | Default 20, capped at 500 |
| `page.page_token` | Cursor from the previous response |

Filters outside the allowlist are rejected with `InvalidArgument`. Text search and every filter are case insensitive, and `%` or `_` in a value are matched literally.

```bash
curl -X POST https://df.example.com/distroface.v1.ArtifactService/SearchArtifacts \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{
    "repo_name":"builds","namespace":"acme",
    "properties":{"os":"linux"},
    "page":{"page_size":50,"order_by":"created_at desc",
      "query":{"filters":[{"field":"version","match":"MATCH_KIND_PREFIX","value":"1.4"}]}}
  }'
```

The response holds `artifacts` and `page` with `next_page_token` (empty on the last page) and `total_count`.

Each artifact has `id`, `repo_id`, `name`, `path`, `version`, `size`, `mime_type`, `metadata`, `properties`, `digest` (`sha256:<hex>`), `created_at` and `updated_at`.

## Searching with dfcli

```bash
dfcli artifact search --name app --property os=linux -t
dfcli artifact search -r acme/builds -v 1.4 --sort size --order ASC --num 10
dfcli artifact search --namespace acme --offset 50 --num 50
```

| Flag | Description |
|---|---|
| `-r`, `--repo` | One repository, optionally `namespace/name` |
| `--namespace` | Namespace for a bare `--repo` or for a cross repository search |
| `--name`, `-v`, `-p` | Substring filters on name, version and path |
| `--property k=v` | Exact property match, repeatable |
| `--num` | Maximum results, default all |
| `--offset` | Skip this many results |
| `--sort`, `--order` | Sort field and `ASC` or `DESC`, default `created_at DESC` |
| `-t`, `--table` | Print a table instead of JSON |

dfcli pages through the RPC until `--num` results are collected. A failed search prints an empty result set. See [the CLI reference](../cli/reference.md).

## Listing without a search

`ArtifactService.ListArtifacts` returns the artifacts of one repository newest first, with an optional exact `version` filter. `ArtifactService.ListArtifactVersions` groups them by version. Its `page.order_by` accepts `version` (natural order, the default, newest first) or `created_at` for most recent activity first, each with `asc` or `desc`.

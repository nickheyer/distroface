# Manifest validation

Pushed manifests are validated the way upstream distribution validates them before anything is stored. Two groups of rules are configurable, layer urls and the completeness of image indexes. Every key below is also an environment variable named `DISTROFACE_REGISTRY_<KEY>`.

## Settings

| Setting | Default | Description |
| --- | --- | --- |
| `registry.manifest_urls_allow` | `[]` | Regular expressions a layer url must match, empty refuses every url |
| `registry.manifest_urls_deny` | `[]` | Regular expressions that refuse a layer url even when allowed |
| `registry.index_platforms` | `all` | Which child manifests of an index must exist, `all`, `none` or `list` |
| `registry.index_platform_list` | `[]` | `os/arch` entries checked when `index_platforms` is `list` |

## What every manifest must satisfy

The registry accepts OCI image manifests, OCI image indexes, Docker schema 2 manifests and Docker manifest lists. A manifest must declare schema version 2. Every blob a manifest references, the config and each layer, must already have been uploaded to the repository, and a child manifest referenced by another manifest must exist there too. A reference to something missing fails the push with HTTP 400 and the error code `MANIFEST_BLOB_UNKNOWN`, with the missing digest in the error detail. A reference whose digest is malformed fails with `DIGEST_INVALID`.

## Layer urls

A layer descriptor may carry `urls` that point at content hosted elsewhere, which is how Windows base images from Microsoft are distributed. With no allow and no deny pattern configured, any manifest carrying a layer url is refused. To accept them list a pattern that matches the host:

```yaml
registry:
  manifest_urls_allow:
    - '^https://mcr\.microsoft\.com/'
```

A url passes when it parses, uses `http` or `https`, has no fragment, matches at least one allow pattern, and matches no deny pattern. Patterns are Go regular expressions, and a pattern that does not compile fails config validation with `registry manifest url pattern ... `. A manifest with a url that fails any of these checks is refused with an `UNKNOWN` error reading `invalid URL on layer` followed by `MANIFEST_BLOB_UNKNOWN` for that layer digest. The first error decides the HTTP status, which is 500.

When a url is accepted and the layer uses a non distributable media type, the layer itself does not need to be present in the repository. Layers with a regular media type must be uploaded even when they carry urls.

## Index completeness

An image index or manifest list names child manifests per platform. By default every child must already exist in the repository, otherwise the push fails with `MANIFEST_BLOB_UNKNOWN` naming the missing child. `registry.index_platforms` relaxes that.

| Value | Behavior |
| --- | --- |
| `all` | Every child manifest must exist, the default |
| `none` | Children are not checked, an index may reference manifests that were never pushed |
| `list` | Only children whose `os/arch` appears in `registry.index_platform_list` must exist |

```yaml
registry:
  index_platforms: list
  index_platform_list:
    - linux/amd64
    - linux/arm64
```

Upstream logs a warning at boot for `none` and `list`, because other tooling may expect indexes to be complete. The value is case insensitive and whitespace is trimmed.

## Startup validation

The following combinations are refused when the config loads, and the process does not start:

| Condition | Error |
| --- | --- |
| `index_platforms` is not `all`, `none` or `list` | `registry.index_platforms must be all, none or list` |
| `index_platform_list` set while `index_platforms` is not `list` | `registry.index_platform_list needs index_platforms set to list` |
| `index_platforms` is `list` with an empty list | `registry.index_platforms list needs at least one index_platform_list entry` |
| A list entry is not `os/arch` with both parts | `registry.index_platform_list entry ... must look like os/arch` |
| An allow or deny pattern does not compile | `registry manifest url pattern ...` |

## After a manifest is accepted

Once stored, the manifest triggers the push side effects described in [Registry](overview.md). On the first push to a new repository the registry also reads the image config, up to one megabyte, and if its `org.opencontainers.image.source` label points at a git repository on this instance in the same namespace, the package is linked to it. See [Linked repositories](../git/linked-repositories.md).

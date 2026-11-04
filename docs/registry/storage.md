# Registry storage

Images live on the local filesystem under `registry.storage_path` in the upstream distribution layout, written by a driver that makes durability a setting. Every key below is also an environment variable named `DISTROFACE_REGISTRY_<KEY>`.

## Settings

| Setting | Default | Description |
| --- | --- | --- |
| `registry.storage_path` | `<storage.data_dir>/registry` | Root of the blob store, created at boot |
| `registry.fsync` | `commit` | How much of each write waits for the disk, `always`, `commit` or `never` |
| `registry.blob_cache_size` | `10000` | Layer descriptors kept in process memory, ignored with a `redis` block |
| `registry.upload_purge_age` | `168h` | Abandoned upload sessions older than this are removed |
| `registry.upload_purge_interval` | `24h` | Time between purge passes |
| `registry.tag_lookup_concurrency` | `0` | Parallel tag reads when a manifest delete looks up tags, zero means the cpu count |

Manifest validation keys are covered in [Manifests](manifests.md). The remaining upstream knobs are fixed: deletes are enabled, blob redirects are disabled, and both the catalog and a tag listing return at most 1000 entries per page.

## Layout on disk

```text
<storage_path>/docker/registry/v2/
  blobs/sha256/<xx>/<digest>/data
  repositories/<namespace>/<name>/_layers/sha256/<digest>/link
  repositories/<namespace>/<name>/_manifests/revisions/sha256/<digest>/link
  repositories/<namespace>/<name>/_manifests/tags/<tag>/...
  repositories/<namespace>/<name>/_uploads/<id>/...
```

Blob data is stored once under `blobs` and shared by every repository. A repository directory holds only links to the digests it references, so a layer shared by ten images costs disk once. Renaming or transferring a repository moves its link tree, which is refused while `_uploads` holds an upload in flight or when the target directory already exists. Deleting an organization removes its namespace directory. Small files such as links and manifests are written to a temporary name and renamed into place, so a crash never leaves a partial file.

## Fsync modes

| Mode | Behavior |
| --- | --- |
| `always` | Upstream behavior, every committed blob, every uncommitted close and every small write and its directory are synced |
| `commit` | Committed blobs, manifests and links sync like upstream, upload scratch under `_uploads` does not |
| `never` | Nothing syncs, the page cache decides when bytes reach the disk |

`commit` keeps finished content as durable as upstream while skipping the chunk boundaries and bookkeeping files of an upload in progress, which a client restarts anyway after a crash. `never` survives a process crash but not a power loss and suits replicated or disposable storage. An unknown value fails config validation.

```yaml
registry:
  fsync: never
```

## Blob descriptor cache

Layer existence checks go through a descriptor cache so a `HEAD` on a known blob does not touch the disk. Without redis the cache sits in process memory, sized by `registry.blob_cache_size`, and starts empty on every boot. With a `redis` block the descriptors move there under the `blobs::` and `repository::` keys, survive restarts and are shared by every instance on the same storage, and `registry.blob_cache_size` is ignored. The boot log names the backend in use. [Garbage collection](garbage-collection.md) clears the cache after every real sweep.

Run redis with `allkeys-lru` eviction, descriptor keys never expire on their own. See [Cache](../getting-started/cache.md).

## Upload purging

Resumable upload state is signed with a key derived from the auth secret, so an upload interrupted by a restart can continue afterwards. Sessions nobody resumes are removed by the upstream purger once they are older than `registry.upload_purge_age`. The first pass starts at a random point within an hour of boot and later passes run every `registry.upload_purge_interval`. Negative durations fail config validation, zero falls back to the defaults.

## Health probes

The storage driver is probed every ten seconds. After three failed probes in a row every request under `/v2/` is answered with 503 and the error code `UNAVAILABLE`, message `registry storage failed its health check`, until a probe succeeds again. `HealthService.HealthCheck` reports `degraded` for as long as a probe is failing, and the same status appears when the shared cache is unreachable. See [Health](../administration/health.md).

## Tag lookup concurrency

Deleting a manifest by digest makes the registry find every tag that points at it, which reads every tag directory in the repository. `registry.tag_lookup_concurrency` caps how many of those reads run in parallel. Zero leaves the upstream default, the number of cpus available to the process. A negative value fails config validation.

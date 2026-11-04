# Maintenance

Repositories are repacked and pruned on a schedule, their sizes are measured after every write, and Admin > Git reports where they live and how much they hold.

## Storage layout

| Path | Contents |
| --- | --- |
| `<git.storage_path>/repos/<namespace>/<name>.git` | Bare repository |
| `<git.storage_path>/repos/<namespace>/<name>.wiki.git` | Its wiki |
| `<storage.data_dir>/keys/` | Ssh host keys and the instance signing key |
| `<artifacts.storage_path>/blobs/sha256/` | LFS objects and release assets, see [LFS](lfs.md) |

`git.storage_path` is a config key defaulting to `<storage.data_dir>/git`, see [Configuration](../getting-started/configuration.md). Names are validated before any path is built, and an empty namespace directory is removed when its last repository goes.

## Admin page

Admin > Git holds the hosting defaults, the SSH listener, storage and transfer limits, the Maintenance switch, and the Signing switch. The Instance card shows the ssh host key fingerprints, the signing public key, the repositories directory, and totals: the repository count, the sum of recorded object bytes, and the sum of LFS bytes. `SettingsService.GetGitServerInfo` returns the same values as `storage_path`, `repository_count`, `repository_bytes`, and `lfs_bytes`.

```bash
curl -X POST https://df.example.com/distroface.v1.SettingsService/GetGitServerInfo \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" -d '{}'
```

## Scheduled gc

| Setting | Default | Description |
| --- | --- | --- |
| `git.gc.enabled` | `true` | Repack and prune every repository on a schedule, system tier only |
| `git.gc.interval_hours` | `168` | Hours between sweeps, a value of zero or less is treated as 168 |

A ticker checks once an hour whether a sweep is due. With a shared cache the schedule clock lives in redis and the sweep takes a five minute lease that is kept alive while it runs, so one instance sweeps per cluster, see [Cache](../getting-started/cache.md). Each repository and its wiki are processed in turn:

1. Every object reachable from any ref is written into one new pack without holding the writer lock.
2. The pack is swapped in under the lock only when no ref moved meanwhile. Older packs are deleted unless younger than one minute, since a push may still be writing them, and loose objects now in the pack are removed.
3. Unreachable loose objects older than one hour are pruned.
4. The object store is measured and `size_bytes` updated.

A failure on one repository is logged and the sweep continues. Objects pinned by staging refs under `refs/distroface/`, used by compare across repositories and fork sync, are released once those refs are dropped and fall to the next prune.

## Running maintenance by hand

`GitService.RunMaintenance` repacks one repository right away and returns its `size_bytes`. Admin access is required, and one run per repository happens at a time. The button is Run maintenance under Repo > Settings > General > Storage.

```bash
curl -X POST https://df.example.com/distroface.v1.GitService/RunMaintenance \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"namespace":"myteam","name":"app"}'
```

## Size accounting

`size_bytes` is the byte count of the repository's `objects` directory, packs and loose objects alike. It is refreshed after every push, web commit, ref write, mirror fetch, and maintenance run. Loose object directories whose modification time did not change are trusted between measurements, a maintenance run stats every file. `lfs_size_bytes` is the sum of the LFS objects linked to the repository and is updated on every LFS upload. Both appear on `GitRepository` and under Repo > Settings > General > Storage. The push quota in [Repositories](repositories.md) is checked against the recorded `size_bytes`, so it reflects the state after the previous write.

`branch_count`, `tag_count`, `push_count`, `pull_count`, and `last_pushed_at` are recorded with the size. Fetches over http and ssh increment `pull_count`.

## Open repositories and transfers

Repositories are opened per request and closed when the request ends, releasing their pack descriptors. A 256 MiB object cache and a pool of 1024 pack descriptors are shared by every repository on the instance. Pack transfers that move no bytes for five minutes are cut, and `git.max_concurrent_transfers` bounds how many run at once, see [Cloning](cloning.md). Every write to a repository holds a per repository lock, so pushes, web commits, merges, maintenance swaps, and renames never interleave.

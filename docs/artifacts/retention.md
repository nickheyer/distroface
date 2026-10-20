# Artifact retention

Retention prunes old artifacts from a repository by version count, age and total size. Rules are evaluated after every upload and, when the reaper is enabled, on a schedule across every repository.

## Settings

| Setting | Default | Description |
|---|---|---|
| `artifacts.retention.enabled` | `false` | Turns pruning on for the scope |
| `artifacts.retention.max_versions` | `5` | Artifacts kept per path and property set, zero means unlimited |
| `artifacts.retention.max_age_days` | `0` | Artifacts older than this are pruned, zero disables |
| `artifacts.retention.max_total_size_bytes` | `0` | Cap on the summed size of a repository, zero means unlimited |
| `artifacts.retention.exclude_latest` | `true` | Never prune the newest artifact of each group by age or size |
| `artifacts.reaper.enabled` | `false` | Runs the scheduled sweep, system only |
| `artifacts.reaper.interval_hours` | `24` | Hours between sweeps, values of zero or less fall back to 24, system only |

A policy does nothing when `enabled` is false or when all three limits are zero. System values are set with `SettingsService.UpdateSettings`, see [settings](../administration/settings.md).

## Per organization override

The `artifacts.retention` subtree is overridable at organization scope under Org > Settings. The policy applied to a repository is resolved from its namespace, so repositories under an organization use that organization's values and repositories under a username use the system values. The reaper settings are system only.

## What gets deleted

Artifacts in a repository are grouped by path and property set, and each group is ordered newest first by creation time. Two phases run.

1. Version count and age. Within a group, any artifact past the first `max_versions` entries is deleted. Any artifact created before `now - max_age_days` is deleted, except the newest of its group when `exclude_latest` is on.
2. Total size. If the size of the survivors exceeds `max_total_size_bytes`, artifacts are deleted oldest first until the repository fits. With `exclude_latest` on, the newest artifact of every group is skipped in this phase.

Deleting an artifact removes its row and, when no other artifact references the same digest, its blob on disk. Each deletion is written to the server log.

With the defaults of `max_versions` 5 and `exclude_latest` true, enabling retention keeps the five most recent uploads of every path and property combination.

## When retention runs

`ArtifactService.CompleteArtifactUpload` applies the repository's effective policy right after the new artifact is stored. A failure here is logged and does not fail the upload.

The reaper applies the policy to every repository on a schedule. Once a minute it reads the live settings, skips when `artifacts.reaper.enabled` is off, and starts a sweep once `interval_hours` have passed since the last one. A sweep:

1. Pages through all repositories 200 at a time, resolving the policy once per namespace and applying it to each repository.
2. Removes upload sessions older than `artifacts.stale_upload_cleanup_hours`, see [uploads](upload-and-download.md).
3. Logs the repository count and the number of stale uploads removed.

Only one sweep runs at a time. When a shared cache is configured the lock and the schedule are held there so that one instance in a cluster performs the sweep, see [cache](../getting-started/cache.md).

There is no RPC or UI control to start a sweep by hand. Lower `interval_hours` or upload to the repository to trigger pruning sooner.

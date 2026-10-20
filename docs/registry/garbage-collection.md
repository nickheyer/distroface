# Garbage collection

Deleting a tag or a manifest only drops a link, the blob data stays under the shared blob store. Garbage collection runs the upstream mark and sweep over registry storage and removes blobs nothing references any more.

## Settings

| Setting | Default | Description |
| --- | --- | --- |
| `gc.enabled` | `false` | Run collections on a schedule |
| `gc.interval_hours` | `24` | Hours between scheduled runs, zero or less falls back to 24 |
| `gc.remove_untagged` | `false` | Scheduled runs also delete manifests no tag points at |

These are runtime settings in the system scope, stored through `SettingsService.UpdateSettings`, see [Settings](../administration/settings.md).

```bash
curl -X POST https://registry.example.com/distroface.v1.SettingsService/UpdateSettings \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"scope": {"type": "SETTINGS_SCOPE_TYPE_SYSTEM"}, "settings": {"gc": {"enabled": true, "intervalHours": 24, "removeUntagged": true}}, "updateMask": "gc.enabled,gc.intervalHours,gc.removeUntagged"}'
```

## Scheduled runs

The scheduler checks once a minute whether a run is due and starts one when `gc.enabled` is on and the interval has passed. A scheduled run is never a dry run. With a [shared cache](../getting-started/cache.md) the schedule clock lives in redis, so it survives restarts and several instances on the same storage do not each start their own run. A run that cannot start because another is in progress is logged and skipped.

## Running a collection by hand

Admin > Storage shows the current state, a toggle for removing untagged manifests, and the buttons Run now and Dry run. The page polls every two seconds while a run is in progress. The same actions are available over RPC.

```bash
curl -X POST https://registry.example.com/distroface.v1.GCService/RunGC \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"dryRun": false, "removeUntagged": true}'
```

`GCService.RunGC` returns at once and the run continues in the background. Starting a run while one is already running fails with `FailedPrecondition` and the message `garbage collection is already running`, suffixed with the instance name when a shared cache is in use.

| RPC | Permission | Description |
| --- | --- | --- |
| `GCService.RunGC` | `settings` update | Start a run with `dry_run` and `remove_untagged` |
| `GCService.GetGCStatus` | `settings` read | Running state, last run, schedule, shared cache backend and error |
| `GCService.GetStorageUsage` | `settings` read | Registry and artifact disk usage per namespace and repository |

## Running a dry run

A dry run walks the store exactly like a real run against a driver that deletes nothing and reports what a real run would have removed in `blobs_deleted` and `bytes_freed`. Dry runs never pause writes and never clear the descriptor cache. The last run on Admin > Storage is labeled Dry run with the counts shown as eligible and reclaimable.

## Write pause

A manifest pushed while a sweep is in progress could lose layers the sweep decided were unreferenced, so a real run pauses registry writes for its whole length. `POST`, `PUT`, `PATCH` and `DELETE` requests under `/v2/` are answered with 503, error code `UNAVAILABLE` and the message `registry is read only while garbage collection runs, retry shortly`. Pushes, deletes and mirror syncs are affected, pulls continue. The `Retry-After` header estimates the remaining pause from the length of the previous run minus the time already spent, falls back to five seconds once that estimate has run out, and never exceeds sixty seconds.

Before sweeping the run waits up to two minutes for writes already in flight. If they do not finish the run aborts without touching anything and `last_run.error` reads `pausing registry writes: ...`.

## Several instances

With a shared cache the collector takes the `gc` lease, so only one instance sweeps a given storage at a time, and the write pause is announced to every instance over pub/sub. Each peer refuses writes, reports once its in flight writes drained, and the sweep starts only when every live peer has reported. Peers poll the lease every five seconds as a safety net, so a missed message or a crashed leader still resolves. The result is published, so `GCService.GetGCStatus` shows `running_on` and `last_run.ran_on` on every instance. Without redis every instance runs its own collector.

## Descriptor cache clearing

The blob descriptor cache would keep reporting swept layers as present, so a real run clears it once the sweep finishes. In process memory the cache is replaced with an empty one, in redis the `blobs::` and `repository::` keys are deleted. A failed reset is recorded in `last_run.error`. See [Registry storage](storage.md).

## Reading the status

```bash
curl -X POST https://registry.example.com/distroface.v1.GCService/GetGCStatus \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}'
```

| Field | Description |
| --- | --- |
| `running`, `running_on` | Whether a run is in progress and on which instance |
| `scheduled`, `interval_hours` | The schedule as currently resolved |
| `last_run.started_at`, `last_run.finished_at` | Timing of the most recent run |
| `last_run.dry_run`, `last_run.remove_untagged` | Options the run used |
| `last_run.blobs_deleted`, `last_run.bytes_freed` | Blob directories removed and bytes of their data files, or eligible on a dry run |
| `last_run.error`, `last_run.ran_on` | Failure message, and the sweeping instance with a shared cache |
| `cache`, `cache_error` | Shared cache backend, `memory` without redis, and why it is unreachable |

## Storage usage card

The admin dashboard shows a Storage card with registry bytes, the five largest namespaces with their repository counts, and the artifact side. Numbers come from `GCService.GetStorageUsage`, which walks the blob store and attributes each digest once per namespace. A result is served from a local copy for thirty seconds and from the shared cache for two minutes, after which the next caller gets the stale copy while a refresh runs in the background. A cold instance waits up to two seconds for the first walk and then answers with `pending` set, which the card shows as Measuring. A completed real run drops the cached numbers so freed space shows right away.

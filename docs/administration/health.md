# Health

`HealthService.HealthCheck` reports whether the instance and its storage are serving. It needs no authentication and backs the container healthcheck.

## Checking an instance

```bash
curl -X POST http://localhost:8080/distroface.v1.HealthService/HealthCheck \
  -H 'Content-Type: application/json' -d '{}'
```

```json
{"status":"ok","timestamp":"2026-11-03T12:00:00Z","version":"1.4.0"}
```

| Field | Description |
| --- | --- |
| `status` | `ok`, or `degraded` when a subsystem check fails |
| `timestamp` | Server time of the check |
| `version` | The `APP_VERSION` environment variable, `dev` when unset |

The HTTP status is `200` either way. A failing check is also logged as a warning with its reason.

## Degraded status

Two checks feed the status:

| Check | Fails when |
| --- | --- |
| Registry storage | The storage driver probe, run every 10 seconds, has failed 3 times in a row |
| `redis` | A shared cache is configured and the connection is down, see [Cache](../getting-started/cache.md) |

While the storage probe is failing every registry API request answers `503` with the error `registry storage failed its health check`. The control plane keeps serving. Storage configuration is covered in [Registry storage](../registry/storage.md).

## Container healthcheck

The image ships this check:

```dockerfile
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD wget -qO /dev/null --post-data='{}' --header='Content-Type: application/json' \
    http://127.0.0.1:8080/distroface.v1.HealthService/HealthCheck || exit 1
```

It passes whenever the endpoint answers, including a `degraded` status, so an orchestrator that should restart on degraded storage needs to inspect the body. Change the port in the command when `server.port` is not `8080`.

Profiling and log settings for diagnosing a degraded instance are in [Logging and profiling](../getting-started/logging-and-profiling.md).

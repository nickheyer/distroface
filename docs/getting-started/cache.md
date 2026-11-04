# Cache

Every cache sits in process memory by default. A `redis:` block turns it into a shared cache so several instances on the same storage and database behave as one.

## Enabling redis

```yaml
redis:
  url: "redis://127.0.0.1:6379/0"
```

Or `DISTROFACE_REDIS_URL=redis://127.0.0.1:6379/0`. The url accepts the go-redis query options such as `?dial_timeout=2s`. `rediss://` turns on TLS against the host trust store. `redis.url` and `redis.addrs` are exclusive.

| Key | Default | Description |
| --- | --- | --- |
| `redis.url` | | `redis://` or `rediss://` url for a single node |
| `redis.addrs` | | Addresses, several without a master name mean a cluster |
| `redis.master_name` | | Sentinel master, with `addrs` naming the sentinels |
| `redis.sentinel_username`, `redis.sentinel_password` | | Sentinel credentials |
| `redis.username`, `redis.password`, `redis.db` | | Node credentials and database index |
| `redis.protocol` | `3` | RESP version |
| `redis.tls` | `false` | Forced on by `rediss://` or any tls file below |
| `redis.tls_ca_file` | | CA bundle instead of the host trust store |
| `redis.tls_cert_file`, `redis.tls_key_file` | | Client certificate pair, both or neither |
| `redis.dial_timeout`, `redis.read_timeout`, `redis.write_timeout` | `1s` | Per operation timeouts |
| `redis.max_retries` | `1` | Retries per operation |
| `redis.pool_size`, `redis.min_idle_conns`, `redis.max_idle_conns`, `redis.max_active_conns` | client default | Pool sizing |
| `redis.pool_timeout`, `redis.conn_max_idle_time`, `redis.conn_max_lifetime` | client default | Pool timing |
| `redis.route_by_latency`, `redis.route_randomly` | `false` | Cluster read routing |

Run redis with `allkeys-lru` eviction. Registry descriptor keys never expire, leases refresh often enough to stay warm. The compose file starts a `redis:8-alpine` service with 256 MB and that policy.

## What the shared cache holds

Every key carries the `df:` prefix.

- Resolved sessions and api tokens, served for a minute instead of three database reads per request.
- Login lockouts and pull rate limit windows, counted across instances.
- Registry blob descriptors, instead of the in process descriptor cache.
- Git diff stats and ahead or behind counts, surviving restarts.
- The storage usage card, computed once for everyone.
- Change notices for settings, portals, certificates and roles, published over pub/sub so every instance reloads at once.
- ACME server nonces and orders, so a client may talk to any instance.

## Leases

Background jobs take a lease so one instance runs each at a time: registry garbage collection and its write pause, artifact retention, mirror syncs, git maintenance, certificate renewal, mail delivery and inbound mail polling, and audit pruning. Without redis every instance runs its own.

## Failure behavior

Every miss falls back to the database or disk. An unreachable redis marks the health endpoint `degraded` while the instance keeps serving from process memory, see [Health](../administration/health.md). Boot waits up to five seconds for the first ping. Admin > Storage shows the backend and whether it answers.

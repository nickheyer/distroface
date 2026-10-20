# Logging and profiling

The server logs to stdout and, by default, to a rotating file under the data dir. A Go profiler listener is available on demand.

## Logging

| Key | Default | Description |
| --- | --- | --- |
| `logging.enabled` | `true` | Write the file log, stdout is always on |
| `logging.dir` | `<data_dir>/logs` | Log directory |
| `logging.default_module` | `distroface-app` | File name without `.log` |
| `logging.max_size` | `10` | Megabytes per file before rotation |
| `logging.max_backups` | `5` | Rotated files kept |
| `logging.max_age` | `30` | Days a rotated file is kept |
| `logging.compress` | `true` | Gzip rotated files |

Lines are queued and written by one goroutine so no request blocks on disk. The queue holds 8192 lines. Overflow is dropped and the drop count is reported inline.

Registry request logging, audit entries and Actions job logs are separate from this file. See [Audit](../administration/audit.md) and [Artifacts and cache](../actions/artifacts-and-cache.md).

## Profiling

Set `DISTROFACE_PPROF_ADDR` to an address and the process serves Go runtime profiles on a private listener. Nothing listens without the variable.

```bash
DISTROFACE_PPROF_ADDR=127.0.0.1:6060 ./distroface
go tool pprof http://127.0.0.1:6060/debug/pprof/profile?seconds=30
```

| Path | Profile |
| --- | --- |
| `/debug/pprof/` | Index, heap, goroutine, mutex, block, allocs, threadcreate |
| `/debug/pprof/profile` | CPU, `seconds` parameter |
| `/debug/pprof/trace` | Execution trace |
| `/debug/pprof/cmdline`, `/debug/pprof/symbol` | Process arguments, symbol lookup |

Mutex profiling samples one in five contention events and block profiling samples at 100 microseconds. Profiles expose internals, keep the listener on loopback or behind a firewall. A non loopback bind logs a warning.

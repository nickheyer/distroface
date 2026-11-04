# Configuration

Boot wiring lives in one `config.yaml`. Everything tunable at runtime lives in settings stored in the database and edited under Admin, see [Settings](../administration/settings.md). Every default is listed in [`config.example.yaml`](../../config.example.yaml).

## Config file and environment

The server looks for `config.yaml` in the working directory, then `./config`, then `/etc/distroface`. A missing file is fine, every key has a default.

Any key is also an environment variable: prefix `DISTROFACE_`, upper case, dots replaced by underscores. `database.dsn` becomes `DISTROFACE_DATABASE_DSN` and `settings.auth.oidc.client_id` becomes `DISTROFACE_SETTINGS_AUTH_OIDC_CLIENT_ID`.

## Keys

| Key | Default | Description |
| --- | --- | --- |
| `server.port` | `8080` | Main listener, TLS and cleartext on the same port |
| `server.host` | `0.0.0.0` | Bind address |
| `server.read_timeout` | `15` | Seconds |
| `server.write_timeout` | `15` | Seconds |
| `server.idle_timeout` | `60` | Seconds |
| `server.trusted_proxies` | loopback and private ranges | CIDRs whose forwarded headers decide the client ip for rate limits and audit |
| `storage.data_dir` | `./data` | Parent of every derived path below |
| `database.driver` | inferred | `sqlite` or `postgres`, see [Database](database.md) |
| `database.path` | `<data_dir>/distroface.db` | SQLite file |
| `database.dsn` | | Postgres url, required for postgres |
| `database.max_connections` | `25` | Pool size |
| `database.max_idle_conns` | `5` | Idle connections kept |
| `database.conn_max_lifetime` | `300` | Seconds |
| `redis.*` | off | Shared cache, see [Cache](cache.md) |
| `registry.storage_path` | `<data_dir>/registry` | Image blobs and manifests |
| `registry.*` | | Blob cache, manifest rules, fsync, upload purge, see [Registry storage](../registry/storage.md) |
| `artifacts.storage_path` | `<data_dir>/artifacts` | Artifact files and git LFS objects, content addressed |
| `git.storage_path` | `<data_dir>/git` | Bare repositories |
| `logging.*` | | See [Logging and profiling](logging-and-profiling.md) |
| `tls.cert_file`, `tls.key_file` | | On disk pair served when the certificate source is `CERT_SOURCE_CONFIG`, see [TLS](../administration/tls.md) |
| `auth.jwt_secret` | generated | Session signing secret, stored in the database when unset |
| `auth.sealing_key` | generated | Encrypts stored secrets at rest, written to `<data_dir>/sealing.key` when unset |
| `bootstrap` | | Users and orgs created on first boot |
| `settings` | | Runtime settings seeded on first boot |
| `overrides` | | Runtime settings pinned forever |

Config validation fails on an unknown `database.driver`, a postgres driver without a dsn, bad manifest url patterns, an invalid `registry.fsync` or `registry.index_platforms`, and mismatched redis client cert and key.

## Seeding settings

The `settings:` block is written to the system settings tier on the first boot only. Every leaf of it is also an environment variable, `DISTROFACE_SETTINGS_<PATH>`.

```yaml
settings:
  server: { public_hostname: "registry.example.com" }
  tls: { mode: "TLS_MODE_HTTPS_ONLY", primary_source: "CERT_SOURCE_ACME" }
  acme: { enabled: true, email: "ops@example.com", challenge_port: "80" }
  git: { ssh_enabled: true, ssh_public_host: "registry.example.com:2222" }
```

```bash
DISTROFACE_SETTINGS_GIT_SSH_ENABLED=true
DISTROFACE_SETTINGS_GIT_SSH_PUBLIC_HOST=localhost:2222
DISTROFACE_SETTINGS_JSON='{"git":{"sshEnabled":true,"sshPublicHost":"localhost:2222"}}'
```

## Pinning settings

The `overrides:` block, its per key form `DISTROFACE_OVERRIDES_<PATH>`, or `DISTROFACE_OVERRIDES_JSON`, pins fields on every boot. Pinned fields show as locked in the UI and `SettingsService.GetSettings` lists them under `locked_paths`.

```yaml
overrides:
  acme:
    directory_url: "https://step-ca.internal/acme/acme/directory"
  portals:
    require_hostname_approval: true
```

## Bootstrap

Entries are created once and never updated. Existing usernames and org names are skipped.

```yaml
bootstrap:
  users:
    - username: "admin"
      password: "changeme"        # Required
      email: "admin@example.com"  # Optional
      roles: ["admin"]            # System roles, default roles when omitted
  orgs:
    - name: "platform"
      display_name: "Platform Team"
      members:
        - username: "admin"
          role: "owner"           # owner, admin, or member
```

One extra user comes from the environment: `DISTROFACE_BOOTSTRAP_USERNAME` and `DISTROFACE_BOOTSTRAP_PASSWORD`, with optional `DISTROFACE_BOOTSTRAP_EMAIL` and a comma separated `DISTROFACE_BOOTSTRAP_ROLES`.

## Retired keys

Older keys still load with a warning on stderr:

- `server.hostname`, `tls.enabled` and `tls.acme.*` seed `server.public_hostname`, `tls.mode` and the matching `acme.*` settings on first boot, like the `settings:` block, and a field the block sets wins. `tls.acme.domains` are registered as approved certificate domains.
- `auth.session_timeout`, `auth.token_expiry`, `auth.anonymous_access`, `auth.local`, `auth.oidc`, `gc`, `rate_limit`, `webhooks`, `security`, `artifacts.max_file_size_mb`, `artifacts.v1_compat`, `artifacts.retention`, `artifacts.reaper` and `artifacts.stale_upload_cleanup_hours` are ignored. The same options are runtime settings, move them to `settings` or `overrides`.

# Installation

DistroFace ships as one static binary with the web UI embedded, and as the `nickheyer/distroface` container image. Everything it stores lives under one data directory.

## Running the container

```bash
docker run -p 8080:8080 -v distroface:/app/data nickheyer/distroface:latest
```

Open http://localhost:8080 and create an account. The first account becomes admin.

The image runs as uid 1000, keeps its data at `/app/data`, and exposes ports 8080 and 22. The binary carries `cap_net_bind_service`, so portals and git ssh can bind privileged ports without root. The image healthcheck posts to `HealthService.HealthCheck` every 30 seconds.

| Port | Purpose |
| --- | --- |
| 8080 | Web UI, API, registry, git over http. TLS and cleartext share this port. |
| 22 | Git over ssh, off until an admin enables it under Admin > Git. |
| 80 | ACME http-01 challenge listener when automatic issuance is on, see [ACME](../administration/acme.md). |

Publish git ssh on another host port when 22 is taken and tell clone urls about it:

```bash
docker run -p 8080:8080 -p 2222:22 \
  -e DISTROFACE_SETTINGS_JSON='{"git":{"sshEnabled":true,"sshPublicHost":"localhost:2222"}}' \
  -v distroface:/app/data nickheyer/distroface:latest
```

## Running the binary

Static `distroface` binaries for linux and windows are on the [releases page](https://github.com/nickheyer/distroface/releases). The binary reads `config.yaml` from the working directory, `./config`, or `/etc/distroface`, and creates `./data` when no data dir is configured.

```bash
tar -xzf distroface_<version>_linux_amd64.tar.gz
./distroface
```

## Docker Compose

`docker-compose.yaml` in the repository builds the image from source and runs it with host networking. It carries commented out Postgres and Redis services. Uncomment them and the matching environment lines to run with an external database or a shared cache, see [Database](database.md) and [Cache](cache.md).

Jobs run by the built in Actions runner are sibling containers, so the data dir must sit at the same path on the host and inside the container, and the docker socket must be mounted. The compose file shows both bindings, see [Runners](../actions/runners.md).

## First boot

- The first registered account gets the admin role. Registration stays open for everyone afterwards unless an admin turns off `auth.local_allow_registration`.
- Seed accounts and organizations without the UI through the `bootstrap:` block or the `DISTROFACE_BOOTSTRAP_*` variables, see [Configuration](configuration.md).
- Runtime settings such as the public hostname, TLS mode and ACME can be seeded once with `DISTROFACE_SETTINGS_JSON`.
- The git ssh listener, Actions, Agents, mail, registry GC and the artifact reaper are all off until an admin enables them.

## Upgrading

Stop the old process, replace the binary or pull the new image, start it again. Every start compares a fingerprint of the expected database schema with the one stored in the `schema_states` table and runs the migration only when they differ, see [Database](database.md). Coming from the v1 release layout needs the migrate tool, see [Migrating from v1](../migration.md).

## Installing dfcli

Static `dfcli` binaries for linux, mac and windows are on the releases page, or build one with `make dfcli`. See [CLI overview](../cli/overview.md).

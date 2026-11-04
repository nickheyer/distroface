# Database

SQLite is the default and needs no setup. Postgres is supported for shared or larger deployments.

## SQLite

The file lives at `database.path`, which defaults to `<data_dir>/distroface.db`. Plain paths get these connection options: a 5 second busy timeout, WAL journal mode, foreign keys on, and immediate transaction locking. A path that already carries a query string, or starts with `file:`, is used as given.

## Postgres

Set `database.dsn` to the url of a postgres server. The driver is inferred from a dsn starting with `postgres://` or `postgresql://`, or containing `host=` or `dbname=`. Set `database.driver` to `postgres` to force it. The driver names `sqlite`, `sqlite3`, `postgres`, `postgresql`, `pg` and `pgx` are accepted.

```bash
docker run -p 8080:8080 -v distroface:/app/data \
  -e DISTROFACE_DATABASE_DSN='postgres://distroface:secret@db:5432/distroface?sslmode=disable' \
  nickheyer/distroface:latest
```

`docker-compose.yaml` carries a commented out `postgres:18-alpine` service with a healthcheck the app waits on.

Logs print the dsn with the password blanked, including a password passed as a url parameter.

## Connection pool

| Key | Default | Description |
| --- | --- | --- |
| `database.max_connections` | `25` | Open connections |
| `database.max_idle_conns` | `5` | Idle connections kept |
| `database.conn_max_lifetime` | `300` | Seconds before a connection is recycled |

## Schema migration

Every start hashes the expected schema and compares it with the fingerprint stored in the `schema_states` table, row id 1. The full automigrate pass runs only when they differ or no row exists, then the new fingerprint is saved. Delete that row to force a full pass on the next start:

```sql
DELETE FROM schema_states WHERE id = 1;
```

Named data migrations under `internal/db/migrations` run alongside the schema pass.

## Moving from the v1 layout

The previous release used a different database and storage layout. The `migrate` command copies users, orgs, artifacts and images into a running v2 instance, see [Migrating from v1](../migration.md).

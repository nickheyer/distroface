# Migrating from v1

The `migrate` command copies users, organizations, container images, and artifact repositories from a DistroFace v1 install into a v2 instance. It opens the v1 database read only and never changes v1 data, so it is safe to run repeatedly against the same source.

## What v1 is

v1 is the previous DistroFace release layout. It kept a single SQLite file, `distro.db`, next to a storage root with three directories:

| Path under the storage root | Contents |
| --- | --- |
| `blobs/sha256/<hex>` | Image blob content, flat and unsharded |
| `repositories/<name>/_manifests/revisions/sha256:<hex>` | Raw manifest bytes |
| `repositories/<name>/_manifests/tags/<tag>/current/link` | The manifest digest a tag points at |
| `repositories/<name>/_layers/...` | Link tree, ignored by the tool |
| `artifacts/repos/<repo>/versions/<version>/files/<uploadID>/<path>` | Artifact files |

Image names are one or two path segments deep, for example `app` or `team/app`. The tool reads six tables from `distro.db`: `users`, `groups`, `image_metadata`, `artifact_repositories`, `artifacts`, and `artifact_properties`. Nothing else in the v1 database is read.

## Building the tool

The Makefile has no target for it. Build it from the repository root with the Go toolchain.

```bash
go build -o build/migrate ./cmd/migrate
```

## Commands

The first argument is the command, flags follow it. Run `migrate <command> -h` to list the flags of one command.

| Command | What it does |
| --- | --- |
| `report` | Dry run analysis of v1 data, cross checking the database against the storage tree, with no writes |
| `users` | Imports v1 users into v2 with bcrypt hashes preserved and groups mapped to roles |
| `orgs` | Creates the legacy org plus one org per two level v1 namespace |
| `images` | Replays v1 images into the v2 registry by pushing through the registry API |
| `artifacts` | Imports v1 artifact repositories, blobs, rows, and properties |
| `verify` | Tag, digest, visibility, and artifact parity report of v1 against v2 |
| `all` | Runs `users`, `orgs`, `images`, `artifacts`, then `verify` and stops at the first failing step |

## Flags

Every command accepts the same flag set. Each command only requires the flags it needs and reports which one is missing.

| Flag | Default | Description |
| --- | --- | --- |
| `-v1-db` | `distro.db` | Path to the v1 `distro.db` |
| `-v1-root` | none | Path to the v1 storage root containing `blobs`, `repositories`, and `artifacts` |
| `-v2-db` | none | v2 database, a SQLite path or a Postgres DSN, used for direct writes of users, orgs, visibility, and the webhook toggle |
| `-v2-artifacts` | none | v2 artifact storage root, the value of `artifacts.storage_path` in the v2 config |
| `-v2-url` | none | v2 registry `host[:port]`, for example `registry.example.com:8080` |
| `-v2-user` | none | v2 username for registry pushes, also the owner of created orgs |
| `-v2-pass` | `$V2_PASSWORD` | v2 password or `df_` API token, read from the `V2_PASSWORD` environment variable when empty |
| `-plain-http` | `false` | Talk to the v2 registry over plain HTTP |
| `-legacy-ns` | `legacy` | Org namespace that flat v1 image names are mapped into |
| `-dry-run` | `false` | Print planned actions without writing anything |
| `-jobs` | `1` | Concurrent repository pushes and artifact file copies |
| `-v` | `false` | Verbose logging, prints every imported, skipped, and pushed item |

The v2 server must have started at least once before the tool runs. With a SQLite `-v2-db` the file must already exist, and the `-v2-artifacts` directory must already exist. Both are created by the server on first start. By default the server places them at `<data_dir>/distroface.db` and `<data_dir>/artifacts`, where `storage.data_dir` defaults to `./data`.

## Name mapping

A flat v1 name such as `app` becomes `<legacy-ns>/app` in v2. A two level name such as `team/app` passes through unchanged. This matches the portal `map_unqualified` rule, which maps single segment names into an org namespace, so a portal on the legacy org can keep serving the old flat names.

Every namespace must satisfy the v2 name rule, lowercase letters, digits, `_`, `.`, and `-`, between 3 and 40 characters, starting and ending with a letter or digit. v1 usernames that break this rule are imported as they are, but the report flags them because the UI would reject them at registration.

## Phases

The `all` command runs these in order and stops at the first failure. Each phase is also its own command so a failed step can be rerun alone. Existing users, orgs and artifacts are skipped on a rerun, and tags that already match are counted as up to date.

| Phase | Needs | What it does |
| --- | --- | --- |
| `users` | `-v2-db` | Creates a local v2 account per v1 user, keeping the username, bcrypt hash and timestamps. Groups map to roles, `admins` to `admin`, `developers` and `readers` to `user`, anything else to `user` with a warning |
| `orgs` | `-v2-db` | Creates the legacy org and one org per two level namespace prefix found in v1 image names, owned by `-v2-user` (default `jenkins`), who must exist already |
| `images` | `-v1-root`, `-v2-url`, admin credentials | Pushes every tag of every v1 repository through the registry API, manifest bytes unchanged so digests are preserved. Tags whose digest v2 already serves are skipped. Child manifests of an index are pushed first. With `-v2-db` the tool deactivates webhooks for the replay and applies v1 privacy afterwards, without it private repositories stay public |
| `artifacts` | `-v1-root`, `-v2-db`, `-v2-artifacts` | Prints the import plan, creates each repository under its owner's namespace, copies files into the content addressed blob store deduplicated by digest, and inserts rows keeping ids, upload ids, properties and timestamps. Only the newest row per repository, version, path and property set survives |
| `verify` | `-v1-root`, `-v2-url` | Read only. HEADs every v1 tag in v2 and reports `MISSING`, `MISMATCH`, `BROKEN`, or `EXTRA`. With `-v2-db` also reports `VISIBILITY` differences and checks every planned artifact by id or identity, size and property hash. Exits non zero on any divergence |

`report` is not part of `all`. It performs the analysis of every phase without writing: users per role, unknown groups, usernames that break the v2 name rule, flat and two level image names, orgs to create, tags that cannot be replayed, storage and database mismatches, and the artifact plan totals.

## What is not migrated

- Docker schema 1 manifests. The v2 registry rejects them, so re-push those tags from a modern client.
- Tags whose digest link, manifest, or referenced blobs are missing or corrupt in v1 storage.
- Image names that exist in `distro.db` but have no directory under `repositories`.
- The v1 image owner column. It is read but never applied, the v2 repository rows are created by the push itself.
- Foreign, nondistributable, and URL backed layers. The manifest still references them.
- Older duplicate artifact rows with the same repository, version, path, and property set. Only the newest survives.
- Artifact rows with a version or path that fails v2 validation.
- Orphaned artifact rows and properties whose repository was deleted in v1.
- v1 group definitions. Only membership maps, and `developers` and `readers` both become the `user` role.
- Anything outside the six tables listed above.

## Running the migration

Start the v2 server once so the database and the artifact root exist, then stop it or leave it running. The tool writes users and orgs directly to the database and pushes images through the running registry, so the server must be up for the `images` and `verify` phases.

Run `report` first and read the warnings.

```bash
export V2_PASSWORD='df_...'

./build/migrate report \
  -v1-db /srv/v1/distro.db \
  -v1-root /srv/v1/data \
  -v2-db /srv/v2/data/distroface.db
```

Then run each writing phase with `-dry-run` to see exactly what it would do. `users` and `orgs` print the planned accounts and orgs with their mapped roles and names. `images` prints every repository with its mapped name, tag count, and visibility, and lists tags that would fail. `artifacts` prints the import plan. `verify` has no dry run mode because it only reads.

```bash
./build/migrate images -dry-run \
  -v1-db /srv/v1/distro.db \
  -v1-root /srv/v1/data \
  -v2-url registry.example.com:8080
```

Finally run `all`, or each phase in order. Re-running is safe. Existing users, orgs, and artifacts are skipped, and tags that already match are counted as up to date.

```bash
./build/migrate all \
  -v1-db /srv/v1/distro.db \
  -v1-root /srv/v1/data \
  -v2-db /srv/v2/data/distroface.db \
  -v2-artifacts /srv/v2/data/artifacts \
  -v2-url registry.example.com:8080 \
  -v2-user admin \
  -jobs 4
```

Pass `-plain-http` when the v2 registry has no TLS, and `-v` to print every item instead of only the totals. Interrupting the tool with Ctrl-C stops it cleanly, and the webhook rows are restored on the way out.

## Schema migrations on server start

Schema migrations are separate from the v1 import. The server checks its own database schema on every start and migrates it when needed, see [Database](getting-started/database.md).

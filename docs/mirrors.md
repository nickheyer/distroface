# Mirrors and imports

A mirror is a repository that DistroFace keeps in sync with an upstream on a schedule. An import copies an upstream once and then leaves a standard repository behind.

## Kinds of mirrors

| Kind | Upstream | What syncs |
| --- | --- | --- |
| Image mirror | An OCI repository, bare names resolve to Docker Hub | Tags that match the filter, multi arch indexes preserved |
| Artifact mirror | GitHub, GitLab, or Gitea releases | Published release assets as versioned artifacts, drafts excluded |
| Git pull mirror | Any https or ssh git url | Every branch and tag, force fetched |
| Git push mirror | Any https or ssh git url | Every branch and tag of a local repository, force pushed |
| Import | Any git url, forge api when the host is known | Code once, plus wiki, LFS, issues, pull requests, labels, milestones, and releases on request |

GitHub, GitLab, and Gitea (also Forgejo and Codeberg) are recognized by host name. Unknown hosts are probed for `/api/v1/version` (Gitea), `/api/v4/version` (GitLab), and `/api/v3/meta` (GitHub Enterprise). A recognized forge adds release mirroring on git mirrors, metadata and release import, token conventions for git over https, and the registry used by linked image mirrors (`ghcr.io`, `registry.gitlab.com` or `registry.<host>`, or the Gitea host itself).

Every mirror shares one `MirrorConfig`: `upstream`, `username`, `auth_token` (write only, reads return `auth_token_set`), `pattern` (a glob), `include_prereleases`, `sync_depth` (newest entries kept, zero keeps all), `sync_interval_minutes` (zero takes the system default), and `paused`. Git mirrors add `mirror_releases`. Creation and every config change validate the upstream live, so a wrong url or token fails the request with `InvalidArgument`.

## Creating an image mirror

In the UI open Images > New repository and pick the type Pull-through mirror. The repository page carries the mirror settings, Sync now, and Stop sync.

```bash
curl -X POST https://distroface.example.com/distroface.v1.RepositoryService/CreateRepository \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"namespace":"infra","name":"nginx","type":"REPOSITORY_TYPE_MIRROR",
       "mirror":{"upstream":"nginx","pattern":"1.*","syncDepth":5}}'
```

`pattern` matches tags. Tags sort newest first by natural order before `sync_depth` applies. A tag whose digest already matches the local copy is skipped. A token without a username is sent as basic auth for user `oauth2`. The first sync runs on the next sweep, within a minute. Use `RepositoryService.SyncRepository` and `RepositoryService.StopRepositorySync` to run or cancel a sync. See [registry overview](registry/overview.md).

## Creating an artifact mirror

In the UI open Artifacts > New repository and pick GitHub, GitLab, or Gitea releases. The repository page has Sync now and Stop sync, Settings has the Mirror source section.

```bash
curl -X POST https://distroface.example.com/distroface.v1.ArtifactService/CreateArtifactRepository \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"namespace":"infra","name":"helm","type":"ARTIFACT_REPO_TYPE_GITHUB_RELEASES",
       "mirror":{"upstream":"helm/helm","pattern":"*linux-amd64*","includePrereleases":false}}'
```

`upstream` is `owner/repo` or a project url. Bare slugs assume github.com, gitlab.com, or codeberg.org by type. `pattern` matches asset file names. Each sync lists up to ten pages of releases with a conditional request, skips drafts and prereleases unless `include_prereleases` is on, keeps the newest `sync_depth` releases, and ingests assets oldest first under the release tag as the version. Gitea and GitLab source archives are included as assets. Files that exceed the artifact size limit are reported as errors. Each artifact carries the properties `mirror.source` and `mirror.upstream`. The first sync runs on the next sweep, within a minute. Run or cancel with `ArtifactService.SyncArtifactRepository` and `ArtifactService.StopArtifactRepositorySync`. See [artifact repositories](artifacts/repositories.md).

## Creating a git mirror

In the UI open Git > New repository and pick Mirror. Afterwards Repo > Settings > Mirror shows the upstream, the SSH key, and Detach upstream.

```bash
curl -X POST https://distroface.example.com/distroface.v1.GitService/CreateGitRepository \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"namespace":"infra","name":"distroface","type":"GIT_REPO_TYPE_MIRROR",
       "mirror":{"upstream":"https://github.com/nickheyer/distroface.git","authToken":"ghp_x"},
       "mirrorReleases":true,"mirrorImages":["nickheyer/distroface"]}'
```

With dfcli, see [cli reference](cli/reference.md):

```bash
dfcli repo create infra/distroface --mirror https://github.com/nickheyer/distroface.git \
  --mirror-username x-access-token --mirror-token ghp_x
```

`upstream` accepts https, `ssh://`, scp style `git@host:owner/repo.git`, `host/owner/repo`, or `owner/repo` (github.com). For https the token is sent as basic auth, the username defaults to `x-access-token` on GitHub, `oauth2` on GitLab, and `token` elsewhere. An ssh upstream needs a repository key: call `GitService.GenerateMirrorKey` (or Generate under SSH key in the UI), install the returned ed25519 public key upstream, then sync. The remote host key is pinned on first contact. `pattern` matches branch and tag names, refs that stop matching are deleted locally except the default branch. `sync_depth` is ignored for git mirrors.

`mirror_releases` copies upstream release entries and their asset files on every sync and removes releases the upstream dropped, see [releases](git/releases.md). `mirror_images` creates linked image mirrors on the forge registry named after the repository. An entry is the upstream `owner/repo` path itself, a path below it such as `cli` (becomes `owner/repo/cli`, local name `<repo>/cli`), or a full reference with a registry host. Tags and digests are stripped. The first sync starts right after creation. `GitService.SyncGitRepository` and `GitService.StopGitRepositorySync` run or cancel a sync, `GitService.DetachMirror` turns the repository into a standard one and drops the upstream. Archived mirrors are not synced.

## Adding a push mirror

Open Repo > Settings > Push mirrors > Add push mirror, or call `GitService.CreatePushMirror`.

```bash
curl -X POST https://distroface.example.com/distroface.v1.GitService/CreatePushMirror \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"namespace":"infra","name":"distroface","remoteUrl":"https://gitlab.com/infra/distroface.git",
       "username":"oauth2","authToken":"glpat-x","syncOnPush":true,"intervalMinutes":60}'
```

A push mirror force pushes every branch and tag. `sync_on_push` pushes 30 seconds after the last client push, `interval_minutes` schedules pushes like a pull mirror (zero uses the system default). An ssh remote requires `use_ssh_key` and the repository key from `GitService.GenerateMirrorKey`. `GitService.SyncPushMirror` pushes now, `GitService.UpdatePushMirror` and `GitService.DeletePushMirror` manage it. A stored token is dropped when the remote moves to another host.

## Importing from GitHub, Gitea, or GitLab

In the UI open Git > New repository and pick Import a repository. Via RPC send `import_source` instead of `mirror` on `GitService.CreateGitRepository`. The repository is created as a standard repository and the import runs in the background.

```bash
curl -X POST https://distroface.example.com/distroface.v1.GitService/CreateGitRepository \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"namespace":"infra","name":"distroface",
       "importSource":{"upstream":"nickheyer/distroface","authToken":"ghp_x",
                       "metadata":true,"wiki":true,"lfs":true,"releases":true,
                       "images":["nickheyer/distroface"]}}'
```

The import walks these phases in order and saves a cursor after each one: `fetch` (branches and tags), `wiki` (the companion `.wiki.git` when it exists), `lfs` (objects referenced by pointer files on branch tips, through the upstream batch api, within the LFS size limit), `labels`, `milestones`, `issues`, `pulls`, `releases`. Metadata phases need a recognized forge, other hosts import code only and leave a note.

Issues and pull requests keep their upstream numbers. Each carries an imported marker with the upstream author and link instead of a local author, comments likewise. Pull request heads are fetched to `refs/pull/<n>/head`, merged and closed state is recorded as timeline events, reviews are stored as imported and never count toward approval (GitLab exposes approvals only). `releases` copies release entries, and with the option on, their asset files. `images` copies the named upstream images once into linked image repositories that then become standard repositories.

The token is used for the forge api, the git fetch, and LFS. Private GitHub repositories need a token with repository read access, private GitLab projects need the `read_api` scope, private Gitea repositories need a token with read access. See [issues](git/issues.md), [pull requests](git/pull-requests.md), and [wiki](git/wiki.md).

Progress shows on the repository as `import_status` (upstream, phase, running, error, notes) and streams through `MirrorService.WatchSyncs` with kind `import` and a message such as `importing issues (#120)`. A failed import resumes from its cursor on a later sweep once its failure backoff lapses, or right away on `GitService.SyncGitRepository`. `GitService.StopGitRepositorySync` cancels it, `GitService.DismissGitImport` cancels and forgets it while the repository keeps what arrived. Pushes are refused while an import runs. When the import finishes the upstream and credentials are dropped. With mirroring disabled the import waits until an admin enables it.

## Sync schedule and limits

The scheduler ticks every minute and skips while `mirror.enabled` is off or the previous sweep is still running. A sweep collects every mirror that is due: not paused, not cooling down, and either never synced or older than its interval. The interval is `sync_interval_minutes` or `mirror.default_interval_minutes`, never below `mirror.min_interval_minutes`. Due jobs run with `mirror.max_concurrent_syncs` workers, each bounded by `mirror.sync_timeout_minutes`. Every upstream http request, including redirects and OCI pulls, waits `mirror.per_host_spacing_ms` after the previous request to the same host. Release listings cover at most ten pages per sync.

A 429, or a 403 with `X-RateLimit-Remaining: 0`, puts the mirror in a rate limit cooldown. The deadline comes from `Retry-After`, `X-RateLimit-Reset`, or `RateLimit-Reset`, otherwise from `mirror.rate_limit_cooldown_minutes`, and is clamped between one minute and `mirror.max_cooldown_minutes`. Any other failure counts toward an exponential backoff of `mirror.failure_backoff_minutes` doubled per consecutive failure with up to 25 percent jitter, capped by `mirror.max_cooldown_minutes`. A success clears both. The next attempt time is exposed as `mirror_next_attempt`, the last outcome as `mirror_last_error`.

`mirror.max_sync_depth` caps `sync_depth` for image and artifact mirrors, a repository value of zero or above the cap takes the cap.

A manual sync is refused with `FailedPrecondition` while mirroring is disabled, while a sync for the repository is running, or during a rate limit cooldown. Failure backoff does not block a manual sync. A stopped sync is recorded as `sync stopped by user` with no failure accounting.

## Watching syncs

`MirrorService.WatchSyncs` is a server stream of `SyncEvent` messages. On connect it replays every sync in flight, then sends `SYNC_PHASE_STARTED`, `SYNC_PHASE_COMPLETED`, or `SYNC_PHASE_FAILED` (with `error`) per repository, and an empty keepalive frame every 45 seconds. `kind` is `image`, `artifact`, `git`, `git_push`, or `import`. `repo_id` is the repository uuid, the numeric artifact repository id, or the push mirror id. Events for private repositories reach only callers who can read them. The stream uses the Connect streaming envelope:

```bash
printf '\x00\x00\x00\x00\x02{}' | curl -N -X POST \
  https://distroface.example.com/distroface.v1.MirrorService/WatchSyncs \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/connect+json" \
  --data-binary @-
```

## Settings

Admin > Mirroring edits these, see [settings](administration/settings.md). Values are re-read on every sweep and manual sync.

| Setting | Default | Description |
| --- | --- | --- |
| `mirror.enabled` | `true` | Off pauses every scheduled and manual sync and push |
| `mirror.default_interval_minutes` | `60` | Interval for mirrors whose `sync_interval_minutes` is zero |
| `mirror.allow_private_networks` | `false` | Permit upstreams on private ip ranges |
| `mirror.min_interval_minutes` | `5` | Floor on per repository intervals, zero disables, max 1440 |
| `mirror.max_concurrent_syncs` | `3` | Repositories synced in parallel per sweep, max 32 |
| `mirror.per_host_spacing_ms` | `750` | Delay between requests to one upstream host, max 60000 |
| `mirror.sync_timeout_minutes` | `45` | Hard cap on one repository sync, 1 to 720 |
| `mirror.rate_limit_cooldown_minutes` | `15` | Cooldown when the upstream limits without a deadline, 1 to 1440 |
| `mirror.max_cooldown_minutes` | `360` | Longest honored cooldown or backoff, 1 to 10080, never below the rate limit cooldown |
| `mirror.failure_backoff_minutes` | `5` | Base of the exponential failure backoff, 1 to 1440 |
| `mirror.max_sync_depth` | `0` | Cap on releases or tags per sync, zero unlimited |

## Credentials

Upstream tokens, push mirror tokens, and generated ssh private keys are stored sealed under a key derived from `auth.sealing_key`, see [configuration](getting-started/configuration.md). When no sealing key is configured the server generates one into `<data_dir>/sealing.key` on first start. Reads never return a token, only `auth_token_set`. On update, omit `auth_token` to keep the stored token, send an empty string to clear it. A kept token never follows a change of upstream host.

Upstream addresses are checked at dial time for http, git, and ssh. Loopback, link local, multicast, and unspecified addresses are always refused. Private ranges are refused unless `mirror.allow_private_networks` is on, which also covers DNS names that resolve into them.

## Interactions

Registry garbage collection pauses registry writes. Image mirror pushes go through the embedded registry and get HTTP 503 with a `Retry-After` header while the pause lasts, the copy retries briefly and otherwise fails the sync, which the failure backoff reschedules. With a shared cache the pause reaches every instance, see [garbage collection](registry/garbage-collection.md).

With a shared Redis cache each sync takes a lease `mirror:<kind>:<id>` with a two minute TTL that is kept alive while it runs, so only one instance syncs a given repository, see [cache](getting-started/cache.md). Without a shared cache the lease is local to the process.

Fetches into a git mirror do not trigger workflows and scheduled workflows skip mirror repositories unless `git.actions.run_on_mirror` is on, default `false`, see [workflows](actions/workflows.md). Client pushes to a git mirror are refused with `repository is a mirror, push to the upstream instead`, detach the mirror first. Pushes during an import are refused until it finishes.

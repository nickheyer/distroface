# Git Repositories: Design and Implementation Plan

Third content kind. Images are served by the embedded Distribution registry under
`/v2/`, artifacts by our blob store under `/api/v1/artifacts/`, and git repositories will
be served by the embedded go-git v6 server at the root of the host (`https://host/ns/name`)
and over SSH. The target is the feature set of Gitea and Forgejo: smart HTTP and SSH,
LFS, teams and per unit permissions, branch protection, code browsing and editing,
releases, issues, pull requests with reviews, wiki, projects, notifications with email,
mirrors, imports, and Actions. Every section names the existing file whose pattern it
copies, and the places we write something go-git does not ship are called out as such.

## 1. What changed from the previous draft

The previous draft was written around the wrong engine and the wrong scope. Corrections:

- Engine. It proposed `git http-backend` behind `net/http/cgi` plus the git binary for
  SSH, browsing, and mirroring, and listed go-git v6 as a "future swap target". The
  engine is go-git v6.0.0-beta.1, in process, no git binary anywhere in the server. This
  mirrors how Distribution is embedded for images.
- Scope. It declared pull requests, issues, code review, wiki, and CI out of scope. They
  are in scope and designed below, as are teams, email, and imports. Nothing is deferred.
- Addresses. It mounted everything under `/git/`. The project lives at the root like
  Forgejo, `https://host/ns/name` is both the page and the clone URL, and the image repo
  page moves to `/images/ns/name` beside `/artifacts/ns/name`. Section 3 and 26.
- Storage access. It used `exec git` wrappers for tree, blob, log, and diff. All reads go
  through go-git's `object` and `storage/filesystem` packages, the way `RegistryAccess`
  reads Distribution storage directly.
- Hooks. It proposed hook scripts calling back over a unix socket. go-git v6 beta.1 runs
  `PreReceive` and `PostReceive` as Go callbacks inside `transport.ReceivePack`.
- Protocol facts. It claimed go-git served protocol v0 only. beta.1 serves v2 for
  upload-pack (`ls-refs`, `fetch` with the full deepen family) and v0/v1 for
  receive-pack, which is also what git itself does for push.
- SSH. It said SSH was not portal aware and picked port 2222. Namespaces over SSH work
  the way GitHub's do, the path carries them, so `git@ci.example.com:ci/app.git` works on
  any hostname that reaches us. The default port is 22 so no git command ever names a port,
  and portals that want unqualified names and read only enforcement over SSH too get their
  own port. Section 7.
- Permissions. It gave org members a flat write level. Permissions follow the Gitea and
  Forgejo model: teams with per unit levels, collaborators, and an Owners equivalent.
  Section 10.
- Mirroring. It proposed `git fetch` with `GIT_ASKPASS` and `curloptResolve` pinning.
  Mirrors use `Repository.FetchContext` with `client.WithHTTPClient` carrying the
  existing `safeTransport` and `pacedTransport`.
- Stars. It rebuilt the `stars` table into a polymorphic row. Git gets its own
  `git_stars` table, the way every kind in this codebase has its own tables.

Kept because it was right: deploy tokens as scoped API tokens, LFS objects in the artifact
blob store, the transfer concurrency cap, and the portal middleware shape.

## 2. The engine: go-git v6.0.0-beta.1

### 2.1 Mapping to how Distribution is embedded

| Images today | Git | Notes |
| --- | --- | --- |
| `handlers.NewApp(ctx, cfg)` mounted at `/v2/` in `rpc/server.go` | `gitrepo.HTTP` wrapping the catch all handler in `rpc/server.go` | A 150 line port of go-git `backend/http.go`, because `backend.Backend` does not accept `ReceivePackHooks` in beta.1, see 6.2 |
| `distribution/registry/storage` filesystem driver | `storage/filesystem.NewStorageWithOptions` over `osfs.New(root, osfs.WithBoundOS())` | One bare repo directory per repo, see 5 |
| `registry.RegistryAccess` reads tags and manifests from storage | `gitrepo.Engine` opens a `*git.Repository` per call for refs, trees, blobs, log, diff, blame, grep, archive | go-git `object` package, see 14 |
| `repositorymiddleware.Register("distroface", ...)` observer for counters, webhooks, audit | `transport.ReceivePackHooks{PreReceive, PostReceive}` | In process, synchronous, see 8 |
| `auth.TokenHandler` at `/auth/token` | Basic auth on every request, LFS bearer tokens minted by `git-lfs-authenticate` | Git has no token endpoint, see 9 |
| `registry.PullRateLimit` | Same limiters applied to `POST git-upload-pack` | See 6.2 |
| `portal.Manager` opening one HTTP listener per portal port | `gitrepo.SSHManager` opening one SSH listener per SSH port | See 7 |
| `mirror.ociSyncer` pushes into the embedded handler | `mirror.gitSyncer` calls `Repository.FetchContext` into the bare storer | See 20 |

### 2.2 What beta.1 serves

Verified in the module source at `plumbing/transport/{serve,upload_pack,receive_pack,upload_archive}.go`:

- upload-pack: protocol v0, v1, and v2. v2 serves `ls-refs` (ref-prefix, symrefs, peel)
  and `fetch` with `shallow`, `deepen`, `deepen-since`, `deepen-not`, `deepen-relative`,
  `include-tag`, `no-progress`, `ofs-delta`, sideband-64k, `object-format` (sha1 and
  sha256). v0 serves `multi_ack_detailed`, `shallow` with `deepen`, sideband.
- receive-pack: v0/v1 with `report-status`, `delete-refs`, `push-options`, `quiet`,
  `ofs-delta`, sideband-64k, and `no-thin` (clients send full packs). Hooks receive the
  proposed `packp.Command` list, push options, the storer with the new objects already
  unpacked, and a sideband progress writer for messages to the client.
- upload-archive: `tar`, `tar.gz`, `tgz`, `zip`, `--prefix`, path filters, ref names only.
- Dumb HTTP paths are recognised and answered 403.

Not served by beta.1, stated plainly so nobody discovers it in production:

- `filter` (partial clone). Git clients warn "filtering not recognized by server" and
  fall back to a full clone.
- `thin-pack` on push, `atomic` push, `push-cert`, `deepen-since` and `deepen-not` on
  protocol v0 (v2 has them), `allow-reachable-sha1-in-want`.
- Three way merge. `Repository.Merge` is fast forward only and `Worktree.CherryPick`
  resolves conflicts by picking a side. The pull request merge engine in 16.3 is ours.
- LFS. The LFS batch API, basic transfer, locks, and `git-lfs-transfer` in 6.4 and 7
  are ours, backed by the artifact blob store.
- SSH server. go-git has an SSH client only. The server in 7 is `golang.org/x/crypto/ssh`,
  already a dependency, feeding channels into `transport.UploadPack` and friends.

### 2.3 Dependencies

Go: `github.com/go-git/go-git/v6 v6.0.0-beta.1`, `github.com/go-git/go-billy/v6
v6.0.0-beta.1`, `github.com/sergi/go-diff` (go-git dependency, used by the merge engine),
`github.com/ProtonMail/go-crypto` (go-git dependency, used for the instance signing key),
`github.com/go-enry/go-enry/v2` (language bar), `github.com/wneessen/go-mail` (SMTP),
`github.com/emersion/go-imap/v2` and `github.com/emersion/go-message` (incoming mail),
`github.com/docker/docker/client` for the Actions runner in dfcli only. `go get` bumps
`golang.org/x/crypto`, `x/net`, `x/sys`, `x/text` to the versions go-git pins.

UI: `marked` and `dompurify` for markdown, `shiki` for highlighting, `@codemirror/
language-data` for the file editor on top of the CodeMirror 6 already present. No diff
library, the diff viewer renders the structured hunks from 14.3.

## 3. Vocabulary, namespaces, and addresses

- Namespace. A user's username or an organization's name. They share one pool, org
  creation already refuses a name taken by a user. A namespace owns three independent
  catalogs: image repositories, artifact repositories, and git repositories. The same
  `ns/name` may exist in all three because each kind has its own table and unique index,
  and when it does, the three are one project (13).
- Git repository. `namespace/name`, name is one segment matching
  `^[a-z0-9][a-z0-9._-]{0,99}$`, never ending in `.git` or `.wiki`. Owned by the
  namespace. A user's repo and an org's repo differ only in who the namespace is.
- Reserved namespaces. Because the project lives at the root, the first path segment must
  never be a UI or data plane route. `utils.ReservedNamespaces` lists `admin, api,
  artifacts, images, orgs, settings, login, logout, register, change-password,
  forgot-password, reset-password, docs, notifications, explore, v2, auth, acme, assets,
  static, favicon.ico, robots.txt, git, new, actions, docker, github` (the last three so a
  `uses: actions/checkout` reference can never land on a local squat, the runner resolves
  those owners on github.com alone) and is enforced in `services/auth.go`
  Register, `services/user.go` AdminCreateUser, `auth/oidc.go` through
  `UsernameFromClaims` (a reserved slug gets a `-1` suffix), and
  `services/organization.go` CreateOrganization. Bootstrap accounts are operator declared,
  so `container/bootstrap.go` checks only the name syntax and the conventional `admin`
  operator keeps working. Existing accounts are never renamed. A reserved name that
  already exists only loses its root profile page to the UI route of the same name.

Addresses, one table so nothing is ambiguous:

| Thing | UI page | Data plane |
| --- | --- | --- |
| Home | `/` lists image repositories, `/git` lists git repositories when `git.enabled` |  |
| Git repository | `/ns/name` and sub pages `/ns/name/issues/3` | `https://host/ns/name.git` (`.git` optional), `git@host:ns/name.git` |
| Wiki | `/ns/name/wiki` | `https://host/ns/name.wiki.git`, `git@host:ns/name.wiki.git` |
| Image repository | `/images/ns/name` (moved from `/ns/name`, old URL redirects), nested names `/images/ns/name/web` | `docker pull host/ns/name` unchanged |
| Artifact repository | `/artifacts/ns/name` unchanged | `/api/v1/artifacts/...` unchanged |
| User or org | `/ns` shows the user profile, or redirects to `/orgs/ns` |  |
| Portal | `https://portal/app` page, `https://portal/app.git` clone, `git@portal:ci/app.git` always, `ssh://git@portal:<ssh_port>/app.git` when the portal has an SSH port | HTTP paths mapped to `ci/app` by the portal middleware, SSH paths canonical on the shared port |

The same string names the project everywhere: `git clone https://host/ns/name` and
`docker pull host/ns/name` refer to one project with one Packages tab, one set of
collaborators, and one deploy token.

## 4. Package layout

```
internal/gitrepo/            Engine wrapper, the registry/ analog
  engine.go                  Open, init, delete, rename, storer options, shared caches
  loader.go                  Row resolved storer lookup, never url bytes
  locks.go                   Per repo writer mutex and transfer semaphore
  http.go                    Smart http handler, port of go-git backend/http.go
  ssh.go                     SSH server, exec parsing, env, exit status
  sshmanager.go              One listener per primary and portal ssh port
  lfs.go                     Batch, basic transfer, verify, locks, lfs-transfer
  hooks.go                   PreReceive policy and PostReceive events
  access.go                  Units and levels, teams, collaborators, grants
  reads.go                   Refs, tree, blob, log, commit, compare, blame, grep, archive
  diff.go                    Structured diff with rename detection
  commit.go                  Tree editor primitive, create commits without a worktree
  merge.go                   Three way tree merge, diff3 text merge, conflict report
  signing.go                 GPG and SSH signature verification, instance signing key
  maintenance.go             Scheduled repack and prune, size accounting
  manager.go                 Business logic shared by rpc services and hooks
internal/forge/              Issues, pulls, reviews, labels, milestones, projects,
                             notifications, releases, wiki, redirects, templates, teams
internal/mailer/             SMTP queue, templates, incoming mail, password reset tokens
internal/actions/            Workflows, runs, jobs, runner protocol, logs, secrets
internal/mirror/git.go       Pull mirror syncer, push mirror syncer, ssh mirror keys
internal/mirror/import_*.go  Forge importers on the existing driver shape
internal/rpc/services/
  git.go gitfiles.go gitrefs.go issue.go pull.go review.go release.go wiki.go
  project.go notification.go keys.go team.go mail.go actions.go runner.go
proto/distroface/v1/
  git.proto issue.proto pull.proto release.proto wiki.proto project.proto
  notification.proto keys.proto mail.proto actions.proto plus edits listed in 12
web/distroface/src/routes/[namespace]/[name]/...   project pages, images move to routes/images
pkg/api/git*.go issue.go pull.go release.go keys.go team.go runner.go   dfcli commands
```

`gitrepo` depends on `db`, `stores`, `auth`, `rbac`, `settings`, `webhook`, `audit`,
`artifacts` (blob store for LFS and release assets). `forge` depends on `gitrepo` and
`mailer`. `actions` depends on `forge`. Services depend on all of them.

## 5. Storage and engine (`internal/gitrepo/engine.go`)

```
<data_dir>/git/repos/<namespace>/<name>.git/        bare repository
<data_dir>/git/repos/<namespace>/<name>.wiki.git/   wiki, created on first page
<artifacts.storage_path>/blobs/sha256/<xx>/<hex>    LFS objects, shared CAS with artifacts
<data_dir>/keys/ssh_host_ed25519_key(.pub)          generated once, like keys/token.key
<data_dir>/keys/ssh_host_rsa_key(.pub)              for clients without ed25519
<data_dir>/keys/signing.asc                         instance OpenPGP key, generated on first use
```

Boot config in `pkg/config/config.go` gains `git.storage_path` (default `<data_dir>/git`),
following `artifacts.storage_path`. Everything else is a runtime setting (22).

Engine rules:

- The root filesystem is `osfs.New(reposDir, osfs.WithBoundOS())`. BoundOS refuses paths
  that escape the base directory. Repo paths are built only from the stored `namespace`
  and `name` columns, both validated by the name pattern, never from URL bytes. The
  loader in `loader.go` resolves the row first, then chroots to `<ns>/<name>.git`.
- Storer per call: `filesystem.NewStorageWithOptions(fs, sharedCache, filesystem.Options{
  LargeObjectThreshold: 32 << 20, Pool: sharedPool})`. One `cache.NewObjectLRU(256 MiB)`
  and one `fdpool.Pool` are shared by every repo. Object hashes are globally unique so a
  shared cache is safe, and the pool bounds open pack file descriptors across repos.
  Storers that implement `io.Closer` are closed when the call ends.
- `git.Open(storer, nil)` gives the `*git.Repository` used by reads. Init is
  `git.PlainInit(path, true, git.WithDefaultBranch(refs/heads/<default>),
  git.WithObjectFormat(sha1|sha256))`.
- Writers serialize. `transport.ReceivePack` documents that callers must serialize
  concurrent ref writers. `locks.go` keeps one `sync.Mutex` per repo id, taken by
  receive-pack, web commits, merges, mirror fetches, maintenance, and rename. Reads never
  take it. Receive-pack takes the lock before opening the storer and re-reads the row
  under it, so a push that waited out a rename lands in the moved directory instead of
  recreating the old one. Rename additionally answers transfers 503 with `Retry-After: 2`
  while the directory moves.
- Transfer cap. One semaphore of `git.max_concurrent_transfers` (default 32) wraps every
  upload-pack, receive-pack, and upload-archive on HTTP and SSH. Pack generation in pure
  Go is CPU and memory bound, so the cap is the safety valve. A waiter gives up after ten
  seconds, HTTP answers 503 with `Retry-After: 5`, SSH exits 1 with `server busy`.
- Size accounting. `SizeBytes` is recomputed after every ref write by walking
  `objects/`, cached on the row, shown in the UI, and summed into the storage usage page.
- Maintenance (`maintenance.go`). A repack then `Repository.Prune` per repo on the
  schedule in `git.gc`, following `admin.Collector.Schedule`. The repack snapshots every
  ref, walks and writes the new pack without the writer lock, then takes the lock only to
  drop the old packs and packed loose objects, and skips that swap when any ref moved in
  between, so pushes never wait on a long repack. Prune runs unlocked with a one hour
  object grace. Rejected pushes leave their unpacked pack on disk because go-git unpacks
  before `PreReceive` runs, maintenance reclaims it. One run per repo at a time, repos
  are visited one after another. Repo settings expose "Run maintenance now".
- Object format. `CreateGitRepository.object_format` accepts sha1 (default) or sha256,
  go-git serves both and advertises `object-format`.

## 6. Transport over HTTP (`internal/gitrepo/http.go`)

### 6.1 Mount

The data plane shares the root with the SPA, so it cannot be a set of root level mux
patterns: `GET /{ns}/{name}/info/refs` and the existing `/v2/` prefix both match
`/v2/x/info/refs` and neither is more specific, which ServeMux refuses at registration.
Instead `gitrepo.HTTP` wraps the frontend handler that `setupFrontend` registers at `/`:

```go
mux.Handle("/", s.GitHTTP.Wrap(s.createFrontendHandler(fs)))
```

Inside, one regexp decides whether a request is git traffic:

```go
// Project path plus a smart http, lfs, or browse suffix
var gitPath = regexp.MustCompile(`^/([^/]+)/([^/]+?)(\.wiki)?(\.git)?/(info/refs|git-upload-pack|git-receive-pack|git-upload-archive|HEAD|objects/.+|info/lfs/.*|-/.*)$`)
```

Everything else falls through to the SPA, which is how `/ns/name` is the page and
`/ns/name.git` is the remote. Every other data plane (`/v2/`, `/api/v1/`, `/auth/`,
`/acme/`, `/.well-known/`, the Connect prefixes) is claimed by a more specific pattern and
never reaches the catch all, and the reserved namespace list in 3 keeps new users and orgs
from owning one of those first segments. The suffix set never overlaps a UI sub page
because UI sub pages use plain words (`tree`, `blob`, `commits`, `issues`, `pulls`,
`settings`) and the data plane uses `info/*`, `git-*`, `objects/*`, `HEAD`, and `-/*`.

```
GET  /{ns}/{name}[.wiki][.git]/info/refs            smart advertisement, service query
POST /{ns}/{name}[.wiki][.git]/git-upload-pack
POST /{ns}/{name}[.wiki][.git]/git-receive-pack
POST /{ns}/{name}[.wiki][.git]/git-upload-archive
GET  /{ns}/{name}[.git]/HEAD, objects/...           dumb paths, 403 like go-git
POST /{ns}/{name}[.git]/info/lfs/objects/batch
GET  /{ns}/{name}[.git]/info/lfs/objects/{oid}
PUT  /{ns}/{name}[.git]/info/lfs/objects/{oid}
POST /{ns}/{name}[.git]/info/lfs/verify
POST /{ns}/{name}[.git]/info/lfs/locks
GET  /{ns}/{name}[.git]/info/lfs/locks
POST /{ns}/{name}[.git]/info/lfs/locks/verify
POST /{ns}/{name}[.git]/info/lfs/locks/{id}/unlock
GET  /{ns}/{name}/-/raw/{ref}/{path...}             blob bytes, sniffed content type
GET  /{ns}/{name}/-/archive/{file}                  <ref>.zip, <ref>.tar.gz
GET  /{ns}/{name}/-/lfs/{oid}                       LFS blob download for the UI
GET  /{ns}/{name}/-/releases/{tag}/{asset}          301 to the artifact download
GET  /{ns}/{name}/-/actions/artifacts/{id}          301 to the artifact download
```

### 6.2 Request flow

Copied from `auth/token_handler.go` for identity and from go-git `backend/http.go` for the
wire details. go-git's `backend.Backend` is an `http.Handler` that would serve this in
twenty lines, but its `Serve` builds `ReceivePackRequest` without `Hooks`, so the route
table, content type checks, gzip request bodies, no-cache headers, and the chunk flushing
response writer are ported into `http.go` and the three `transport.*` functions are
called directly. The behavior is identical, including the 403 for dumb protocol paths and
the 500 only when no byte has been written yet.

1. Portal. Already applied by the middleware (24). `portal.FromContext` carries
   `AllowPush` and `AllowAnonymous`.
2. Repo. Resolve `(ns, name)`. A miss consults `git_redirects` (25) and, once the access
   check in step 4 has admitted the caller to the repository at its new address, answers
   301 to the new location for `GET info/refs`, which git follows, so a move never tells
   an outsider where a private repository went. Still a miss answers 401 with
   `WWW-Authenticate: Basic realm="distroface"` when no credentials came, 404 otherwise,
   so existence never leaks. The wiki shares no LFS store, `info/lfs` and `-/lfs` on a
   `.wiki` path answer 404.
3. Identity. `Authorization: Basic` with username plus local password, or any username
   plus a `df_` token (token wins, mirrors `TokenHandler`). `Authorization: Bearer df_...`
   for tooling. `Bearer <lfs jwt>` on `info/lfs/*` paths (9.5). No credentials runs as
   anonymous where `portal.AnonymousAllowed` says so. Failed credentials feed
   `authLimiter`, blocked IPs get 429 before any lookup.
4. Access. `gitrepo.Access.Can(user, repo, UnitCode, action)` (10). Reads are `info/refs?
   service=git-upload-pack`, `POST git-upload-pack`, `POST git-upload-archive`, LFS batch
   with `operation: download`, LFS GET, raw, archive. Pushes are `info/refs?service=
   git-receive-pack`, `POST git-receive-pack`, LFS batch `operation: upload`, LFS PUT,
   locks. Pushes need `AllowPush` on a portal and are refused on archived repos. The wiki
   uses `UnitWiki`.
5. Limits. The transfer semaphore (5). `POST git-upload-pack` applies `pullLimiter`
   keyed by user or `anonPullLimiter` keyed by client IP, same headers and 429 body as
   `registry.PullRateLimit`. A receive-pack body is capped at the quota headroom plus one
   MiB when `git.max_repo_size_mb` is set, 413 up front on a known length and an unpack
   error mid stream otherwise, so an oversized push never lands on disk. Both transports
   cut a session that moves no bytes for five minutes, HTTP by pushing read and write
   deadlines before each read and write, SSH by closing the channel.
6. Service. Content type must be `application/x-git-<svc>-request`, response is
   `application/x-git-<svc>-result` or `-advertisement`, `Git-Protocol` header becomes
   `GitProtocol`, gzip bodies are decoded. `transport.UploadPack(ctx, st, body, frw,
   &UploadPackRequest{GitProtocol, StatelessRPC: true})`, `transport.ReceivePack(...,
   &ReceivePackRequest{..., Hooks: manager.Hooks(ctx, repo, user)})`, or
   `transport.UploadArchive`. The receive-pack call holds the repo writer mutex.
7. After. upload-pack increments `pull_count`, receive-pack success sets `push_count`
   and `last_push`. Audit rows `Git/fetch` and `Git/push` with `ns/name` detail follow the
   `Registry/pull` and `Registry/push` shape in `registry/listener.go`.

### 6.3 Raw and archive

`-/raw/<ref>/<path>` resolves the ref with `ResolveRevision`, reads the blob, sniffs the
content type, sends `inline` for text and images and `attachment` otherwise, honors
`If-None-Match` on the blob hash, and streams. Every raw response and every artifact or
release asset download goes through `utils.HardenUserContent`: html, xhtml, and xml types
become `text/plain`, `nosniff` is set, and `Content-Security-Policy: default-src 'none';
style-src 'unsafe-inline'; sandbox` puts a directly opened file (svg included) in an
opaque sandboxed origin where no script runs and the app session is out of reach. An LFS pointer blob is detected and, when
`?lfs=1` is set, the object bytes are served instead. `-/archive/<ref>.zip|tar.gz` calls
`Repository.ArchiveContext` with `Prefix: <name>-<ref>/`, caps concurrency with the
transfer semaphore, and sets `Content-Disposition`. Both go through the read access check.

### 6.4 LFS (`internal/gitrepo/lfs.go`)

The git-lfs batch API over the paths in 6.1. Objects live in the artifact `BlobStore`
through the existing `InitiateUpload`, `AppendChunk`, `CompleteUpload` calls. The computed
sha256 must equal the requested oid or the upload is rejected and the staging file
removed. Rows: `lfs_objects(oid, size)` and `git_lfs_links(repo_id, oid)`. An upload
batch naming an oid the store already holds links it without a transfer only when the
caller can read a repository that links it, otherwise the bytes must be uploaded and
verified, so knowing a private blob's hash is never enough to obtain it. The write
classifier and the batch handler share one strict parser, and a body that fails to parse
counts as a write and is then rejected. Download is
`BlobStore.OpenBlob` streamed with `Content-Length`. `verify` checks row and size.
`artifacts.Manager.gcBlob` learns the second reference table so a blob shared by an
artifact and an LFS object is kept until both are gone. Deleting a repo removes its links
and reaps unreferenced oids. Locks are rows in `lfs_locks(id, repo_id, path, owner_id,
created_at)` with the create, list, verify, and unlock endpoints and `force` for admins.
Creating and releasing a lock needs push, over HTTP and over `git-lfs-transfer` alike.
Size limit `git.max_lfs_object_mb` on the batch, the basic upload, the SSH `put-object`
(refused 413 after draining the data packets) and import downloads alike, batch responses
echo the caller's own `Authorization` header into action headers so HTTP users never need a
second credential. `lfs_enabled`
false answers 404 on `info/lfs/*`.

## 7. Transport over SSH (`internal/gitrepo/ssh.go`, `sshmanager.go`)

### 7.1 Port

SSH is disabled by default (`git.ssh_enabled: false`). An admin turns it on under Admin,
Git, where the port field defaults to 22 and every change is probed on the server before it
is saved: `SettingsService.ProbeGitSSHPort` tries to bind the port (or reports that this
instance already serves it) and `UpdateSettings` refuses a patch that would enable ssh on a
port that cannot be bound. 22 is the only port the scp style URL `git@host:ns/name.git` can
express, so with the default nothing about the port ever appears in a git command. The
Docker image carries `cap_net_bind_service`, so the server binds 22 inside the container and
operators publish it with `-p 22:22` or `-p 2222:22`. When the published port is not 22,
`git.ssh_public_host` (default the host part of `public_hostname`) takes a `host:port` and
the clone box switches to the `ssh://git@host:2222/ns/name.git` form. While ssh is off the
clone box shows HTTPS only, deploy keys explain they are unusable, and no listener opens.

### 7.2 Namespaces, hostnames, and portals

Every user, org, and repo is addressed over SSH the way GitHub addresses millions of them
on one host and one port: the path carries the namespace. `git@github.com:torvalds/linux.git`
reaches the server as the exec command `git-upload-pack 'torvalds/linux.git'`, and ours
arrives as `git-upload-pack 'ci/app.git'`. The server never needs the hostname. A portal
hostname works the same way: `git@ci.example.com:ci/app.git` resolves to our IP, the key
authenticates the user, the path names the repo. The portal UI shows exactly that URL, the
canonical path on the portal hostname, with no port. The brute force limiter clears for a
client only once the handshake has verified a signature, offering a known public key
without proving it counts for nothing.

What SSH does not carry is the hostname the client typed. The protocol has no Host header
and no SNI, so nothing in the connection tells the server whether `host`,
`ci.example.com`, or `registry.example.com` was used. HTTP portals rely on that header for
two things only: turning an unqualified `app.git` into `ci/app.git`, and applying the
portal's own policy (read only, isolation, custom rules). On the shared SSH listener
neither can key off the hostname, so the shared listener serves fully qualified paths with
the caller's own permissions, which is the primary host behavior. A portal that wants the
full HTTP semantics over SSH as well, unqualified names and read only enforcement, takes a
dedicated `ssh_port`: `sshmanager.go` copies `portal.Manager.Reconcile`, keeps one listener
for `git.ssh_port` and one per enabled portal with a non zero `ssh_port` (new
`RegistryPortal.SSHPort` column and proto field), opens and closes them on settings and
portal writes, probes ports before storing them the way `ProbePort` does, and
`Resolver.ResolveSSH(port)` hands the session the portal so `MapName`, `InScope`, and
`AllowPush` apply exactly as on HTTP. Portal SSH ports are unique and never equal an HTTP
portal port. The clone box on such a portal shows `ssh://git@ci.example.com:<ssh_port>/
app.git` beside the canonical form.

### 7.3 Server

`golang.org/x/crypto/ssh` `ServerConfig` with `PublicKeyCallback` only, host keys loaded or
generated into `keys/` with the `loadOrGenerate` shape from `auth/token_service.go`. The
SSH username is ignored (`git@` by convention), identity is the key fingerprint
`SHA256:...` looked up in `ssh_keys`. No password auth, no pty, no shell, no forwarding,
no subsystems. `env` requests accept `GIT_PROTOCOL` only.

Exec commands, both `git-upload-pack 'path'` and `git upload-pack 'path'` forms, quotes
optional, leading slash and `.git` optional, path must be `ns/name` or `ns/name.wiki`
after portal mapping:

```
git-upload-pack        read    transport.UploadPack with channel reader and writer
git-receive-pack       push    transport.ReceivePack with hooks, writer mutex held
git-upload-archive     read    transport.UploadArchive
git-lfs-authenticate   read or push by the operation argument, answers the LFS token json
git-lfs-transfer       read or push, the pure SSH LFS protocol over the channel
```

A session with no command answers "Hi <user>! You've successfully authenticated, but
Distroface does not provide shell access." and exits 0. Exit status is sent with the
`exit-status` request. Idle timeout 5 minutes with activity resets, per key concurrent
session cap of 8, global connection cap from `max_concurrent_transfers`. Unknown keys
audit as `Git/auth` denied but never feed the password limiter shared with docker login,
an agent offering every key it holds is not an attack and `MaxAuthTries` bounds the probe.
Deploy keys (9.2) resolve to a repo scoped principal.

## 8. Hooks (`internal/gitrepo/hooks.go`)

`manager.Hooks(ctx, repo, user)` returns a `transport.ReceivePackHooks` bound to the
request. Both callbacks run inside the transport call, on HTTP and SSH alike, so there is
exactly one policy path.

### 8.1 PreReceive

Receives the unpacked storer, every `packp.Command` (`Name`, `Old`, `New`, `Action()`),
push options, and a progress writer that reaches the client's terminal. Returning an error
refuses every ref with that message. Checks, in order, each writing a one line reason:

1. Server owned refs. `refs/pull/*`, `refs/distroface/*`, and anything outside `refs/heads`,
   `refs/tags`, `refs/notes` is refused.
2. Archived repo. Refused.
3. Quota. `git.max_repo_size_mb` for the system or org tier, compared against the size of
   `objects/` after unpack. Zero disables.
4. Branch protection (`git_branch_protections`, pattern glob over `refs/heads/*`):
   `block_force_push` (the old commit must be an ancestor of the new, `IsAncestor`),
   `block_deletion`, `require_pull_request` (direct pushes refused, merges from 16 arrive
   through the same hook with a bypass flag in ctx), `require_signed_commits` (every new
   commit between old and new verifies against a stored GPG or SSH key of the committer,
   or the instance key), `require_linear_history` (no merge commits), `allowed_pushers`
   (users and teams), `require_status_checks` and `required_approvals` apply to merges
   only and are enforced in 16.5.
5. Tag protection (`git_tag_protections`, pattern over `refs/tags/*`, allowed pushers,
   block deletion).
6. Default branch deletion. Refused while it is the default branch.
7. Push options. `-o skip-ci` is recorded for 21, unknown options are ignored.

Deploy keys and deploy tokens marked read only never reach receive-pack, access refuses
them earlier with a clear 403.

### 8.2 PostReceive

Receives only the commands that applied. Each one becomes a `RefUpdate{Name, Old, New,
Kind: created|updated|deleted|forced}` and the manager fans out synchronously, errors
logged never returned:

- Unborn HEAD. First push to an empty repo sets HEAD to the configured default branch
  when it was pushed, otherwise to the first pushed branch, and stores `default_branch`.
- Row. `push_count`, `last_push`, `size_bytes`, cached branch and tag counts.
- Webhooks. `GIT_PUSH` per branch ref with the commit list (capped at 20 commits with
  `truncated`), `GIT_BRANCH` create and delete, `GIT_TAG` create and delete, through the
  dispatcher in 23.
- Pull requests. Every open PR whose head is the updated branch records a "pushed N
  commits" or "force pushed" timeline event, resets mergeability to unknown, dismisses
  stale approvals when the protection says so, and refreshes `refs/pull/<n>/head`. PRs
  whose base moved reset mergeability only.
- Issues. Commit messages on the default branch are scanned for `close|closes|closed|
  fix|fixes|fixed|resolve|resolves|resolved #N` and `ns/repo#N`, matching issues close
  with a "closed via <sha>" event when the pusher may write issues.
- Releases. An annotated tag creates a draft release when `release_on_tag` is on.
- Actions. Push and tag events enqueue workflow runs (21).
- Push mirrors. Repos with push mirrors marked `sync_on_push` are scheduled, debounced
  30 seconds.
- Notifications. Watchers of the repo receive a push notification when they opted in,
  with email per their preferences (19).
- Audit. `Git/push` detail `ns/name:<ref>@<new>` per ref.
- Hint. After a push to a non default branch with no open PR, the progress writer prints
  `Create a pull request for '<branch>' at <ui url>/pulls/new?head=<branch>` like GitHub.

## 9. Identity and credentials

### 9.1 HTTP credentials

Username plus local password, or any username plus a `df_` API token. OIDC accounts have
no password and use tokens, same as docker today. `git credential` helpers and `.netrc`
work unchanged. dfcli ships a credential helper (27).

### 9.2 SSH keys and deploy keys (`keys.proto`, `ssh_keys` table)

Users add keys under `/settings/keys`. Repo admins add deploy keys on the repo settings
page, read only by default, `read_only=false` allows push. One fingerprint maps to one
principal so a key cannot be registered twice anywhere. Expiry optional, `last_used_at`
set on every auth. A deploy key authenticates as a synthetic principal
`AuthenticatedUser{ID: "deploykey:<id>", Username: "deploy-key", Provider: "deploykey",
Scope: &CredentialScope{Type: GIT_REPO, ID: "ns/name", ReadOnly}}`.

### 9.3 Deploy tokens and credential scopes

`CreateAPIToken` gains `CredentialScope scope` with types `GIT_REPO` (id `ns/name`) and
`ORG` (id org name) plus `read_only`. A scoped token authenticates as the owning user with
`AuthenticatedUser.Scope` set. One function, `auth.ScopeAllows(user, action, objectIDs...)`,
runs at the top of `RepoAccess.HasRepoAccess`, `artifacts.Access.HasRepoAccess`,
`gitrepo.Access.Can`, `TokenHandler.canPull` and `canPush`, and the rpc `authorize`
interceptor. The interceptor lets a scoped caller reach only `GetCurrentUser`, the public
procedures, and procedures mapped to the git, repositories, and artifacts resources. Mapped
procedures with an object are checked against the scope there, with image and artifact
objects resolved to their linked source repo. Mapped procedures without an object pass for
reads only, since lists filter visibility in service and creation never fits a scope.
Everything else (tokens, keys, user and org management, settings, notifications,
projects, transfers, stars) is refused, and `CreateAPIToken` refuses every scoped caller
outright so a deploy or job token can never mint a wider one:

- `GIT_REPO ns/name`: the git repo and its linked packages (13), read always, push unless
  read only, nothing else. This is the `GITHUB_TOKEN` analog external CI uses to publish
  images and artifacts for one source repository.
- `ORG name`: the user's own rights inside that namespace, nothing outside.

Repo settings get a "Deploy tokens" panel that creates `GIT_REPO` scoped tokens owned by
the creating user and lists tokens scoped to the repo.

### 9.4 Signing keys

`gpg_keys(id, user_id, key_id, fingerprint, armored, emails json, expires_at, created_at)`
added under `/settings/keys` next to SSH keys. SSH keys gain `usage: auth|signing|both`
so one ed25519 key can also verify `gpg.format=ssh` commits. Verification
(`signing.go`): `object.Commit.Verify(armored)` for OpenPGP, and an `sshsig` parser
(armored `SSH SIGNATURE`, namespace `git`, sha512) for SSH signatures using
`ssh.ParsePublicKey` and `PublicKey.Verify`. A commit is "Verified" when the signature
checks and the signer's key belongs to a user whose email matches the committer,
"Unverified" otherwise, "Instance" when signed by the instance key. Web commits and merges
are signed with the instance key when `git.signing.enabled` is on, go-git's `Signer`
interface on `CommitOptions` and `CreateTagOptions` does the signing.

### 9.5 LFS tokens

`git-lfs-authenticate` over SSH answers `{"href": "https://<public_hostname>/ns/name.git/
info/lfs", "header": {"Authorization": "Bearer <jwt>"}, "expires_in": 3600}`. The jwt
comes from `TokenService.SignTokenAudience("distroface-git", subject, access)` with access
`[{Type: "git", Name: "ns/name", Actions: ["pull"|"push"]}]`, verified on the HTTP side
with the new `VerifyAccess(raw, audience)` next to `VerifyTokenSubject`. The subject is
the username for accounts and `deploykey:<id>` for deploy keys, a form no username can
take, and the verifier rebuilds the deploy key principal from that row, bound to the
repository the key belongs to. The same ECDSA key
pair signs registry and git tokens, audiences keep them apart. On a portal the href uses
the portal host and mapped name.

## 10. Authorization

Permissions follow the Gitea and Forgejo model: repository units, access levels per unit,
org teams that grant unit levels on repositories, per repo collaborators, and an owners
tier. The existing RBAC roles stay the capability layer, as they are for images and
artifacts.

### 10.1 Units and levels

Units: `code, issues, pulls, releases, wiki, projects, packages, actions`, plus the external
tracker and external wiki settings Gitea has (`external_issues_url`, `external_wiki_url`
replace the built in unit when set). Levels per unit: `none, read, write`. Repo wide
levels: `admin` (settings, collaborators, keys, protections, webhooks) and `owner` (delete,
transfer, archive). `Access.Unit(ctx, user, repo, unit)` returns the highest level that
any of these sources grants, and `Can(ctx, user, repo, unit, action)` maps levels to
actions:

| Source | What it grants |
| --- | --- |
| Repo owner (`OwnerID == user.ID`), namespace equal to the username, org role owner or admin | owner on every unit (the Gitea Owners team) |
| `git/manage` RBAC permission | owner on every unit |
| Collaborator row (`git_collaborators.permission` read, write, admin) | that level on every unit |
| Team membership (10.2) for a team that includes the repo | the team's level per unit, admin when `admin_access` |
| Scoped RBAC grant on `ns/name` or `ns` for an action | the level the action implies |
| Public repo and the `git/read` capability (seeded for `user` and `anonymous`) | read on every enabled unit |
| Anonymous on a private repo, or a disabled unit | none |

Level to action: read gives pull, browse, open issues and comment when signed in,
react, watch, star. Write on `code` gives push and branch and tag writes, on `issues`
labels, milestones, assignees, close and reopen any issue, on `pulls` merge when protection
passes and review, on `releases` create and edit releases and attach assets, on `wiki`
edit pages, on `projects` manage boards, on `packages` push to linked image and artifact
repos, on `actions` dispatch, rerun, cancel, and manage secrets. Admin adds every settings
page and `manage_topics`. Owner adds delete, transfer, archive. Anonymous never writes.
Locked issues accept comments from write and above only. Feature toggles on the repo set
a unit to none for everyone.

### 10.2 Org teams

Gitea organizations grant access through teams, and so do ours from this feature on.
`org_teams(id, org_id, name, description, is_default, can_create_repos,
includes_all_repos, admin_access, units json)`, `org_team_members(team_id, user_id)`,
`org_team_repos(team_id, git_repo_id)`.

- Every org gets a default team named `Members` with `is_default=true` at creation,
  bootstrap seeded orgs included, and a migration adds it to existing orgs. Every org member belongs to it implicitly, it cannot
  be deleted, and its permissions are editable. It starts with `can_create_repos=true`,
  `includes_all_repos=true`, and write on every unit, which is the behavior you asked for:
  members create repos in the org and push to them by default. An org that wants Gitea's
  stricter default lowers the default team to read or none and creates finer teams.
- Org owners and admins are the Owners team: full access to every repo and every setting,
  independent of team rows. This keeps today's owner, admin, member roles meaningful.
- Additional teams pick repos one by one or `includes_all_repos`, set a level per unit,
  may be `admin_access` (repo admin on included repos), and may `can_create_repos`
  (created repos are added to the team). Teams can be named in branch protection
  `allowed_pushers` and `allowed_mergers`, in `CODEOWNERS` as `@org/team`, and as review
  requests.
- Team rows apply to git repos and, through the `packages` unit, to the image and artifact
  repos linked to them. Standalone image and artifact repos keep today's rule where any
  member pushes, nothing about them changes in this plan.
- UI: `/orgs/[name]/teams` and `/orgs/[name]/teams/[team]` with members, repositories, and
  the unit permission grid. The members page shows each member's teams.

### 10.3 Collaborators

`git_collaborators(repo_id, user_id, permission, created_at)` managed from the repo
settings page by admins and above, invited users accept from their notifications.
Collaborators and team members see private repos in listings through the same
`ListOptions` path that handles RBAC grants today, with `OR repo.id IN (collaborator
repos) OR repo.id IN (team repos)` clauses.

### 10.4 RBAC resource (`internal/rbac`)

`resources.go` adds `ResourceGit = "git"` with actions read, create, update, delete,
push, pull, manage. Seeds in `rbac.go`, picked up by the existing backfill loop:

```
user:      git read, pull, push, create, update, delete
anonymous: git read, pull
```

`mapping.go` entries use object id `namespace+name` like artifacts. Every procedure
appears in exactly one of the public, identity, authenticated only, or permission tables,
and the interceptor denies a procedure it cannot find rather than letting it through.
Reads are `AnyGrant` with visibility filtered in service. Issue, pull, release, wiki, and project writes map to
`git/read` plus the unit check in service, because those are relationship gated, not
grant gated. Settings mutations map to `git/update`, deletion and transfer to
`git/delete`, branch and tag writes to `git/push`. `ListScopeableObjects` in
`services/role.go` gains a `git` case, `portalAllowsProcedure` adds `ResourceGit` to the
read only portal check, `auditableResource` works unchanged. `CanCreateInNamespace` is the
artifact one plus the team `can_create_repos` rule for org members.

## 11. Data model (`internal/db/models.go`)

Every table below joins the `AutoMigrate` list in `stores/store.go`. Ids are uuid strings
like `Repository`, foreign keys cascade through gorm constraints like `Webhook`. Core rows:

```go
type GitRepository struct {
	ID              string `gorm:"primaryKey"`
	Namespace       string `gorm:"uniqueIndex:idx_git_namespace_name;not null"`
	Name            string `gorm:"uniqueIndex:idx_git_namespace_name;not null"`
	Description     string
	Website         string
	OwnerID         string `gorm:"index"`
	IsPrivate       bool   `gorm:"not null;default:false"`
	IsOrgNamespace  bool   `gorm:"not null;default:false"`
	DefaultBranch   string `gorm:"not null;default:'main'"`
	ObjectFormat    string `gorm:"not null;default:'sha1'"`
	Type            v1.GitRepoType `gorm:"not null;default:1"` // standard, mirror, fork, template
	ForkOfID        *string `gorm:"index"`
	TemplateOfID    *string
	IsTemplate      bool `gorm:"not null;default:false"`
	IsArchived      bool `gorm:"not null;default:false"`
	Units           string `gorm:"type:text;not null;default:'{}'"` // enabled units and external urls
	LFSEnabled      bool `gorm:"not null;default:true"`
	MergeSettings   string `gorm:"type:text;not null;default:'{}'"` // allowed methods, default, delete branch
	IssueCounter    int64 `gorm:"not null;default:0"` // shared by issues and pulls
	SizeBytes       int64 `gorm:"not null;default:0"`
	LFSSizeBytes    int64 `gorm:"not null;default:0"`
	BranchCount     int32 `gorm:"not null;default:0"`
	TagCount        int32 `gorm:"not null;default:0"`
	PullCount       int64 `gorm:"not null;default:0"` // upload-pack posts
	PushCount       int64 `gorm:"not null;default:0"`
	LastPush        *time.Time
	MirrorConfig    string `gorm:"type:text;not null;default:''"` // protojson like images
	MirrorState     string `gorm:"type:text;not null;default:''"`
	MirrorLastSync  *time.Time
	MirrorLastError string
	CreatedAt, UpdatedAt time.Time
}

type OrgTeam struct {
	ID               string `gorm:"primaryKey"`
	OrgID            string `gorm:"not null;uniqueIndex:idx_team_org_name"`
	Name             string `gorm:"not null;uniqueIndex:idx_team_org_name"`
	Description      string
	IsDefault        bool   `gorm:"not null;default:false"` // every member belongs implicitly
	CanCreateRepos   bool   `gorm:"not null;default:false"`
	IncludesAllRepos bool   `gorm:"not null;default:false"`
	AdminAccess      bool   `gorm:"not null;default:false"`
	Units            string `gorm:"type:text;not null;default:'{}'"` // unit to none read write
	CreatedAt, UpdatedAt time.Time
}

type SSHKey struct {
	ID          string `gorm:"primaryKey"`
	OwnerType   string `gorm:"not null;index:idx_sshkey_owner"` // user or git_repo
	OwnerID     string `gorm:"not null;index:idx_sshkey_owner"`
	Title       string `gorm:"not null"`
	PublicKey   string `gorm:"type:text;not null"`
	Fingerprint string `gorm:"not null;uniqueIndex"`
	KeyType     string `gorm:"not null"`
	Usage       string `gorm:"not null;default:'auth'"` // auth, signing, both
	ReadOnly    bool   `gorm:"not null;default:true"`   // deploy keys only
	CreatedBy   string `gorm:"not null"`
	LastUsedAt  *time.Time
	ExpiresAt   *time.Time
	CreatedAt   time.Time
}

type GitBranchProtection struct {
	ID                     string `gorm:"primaryKey"`
	RepoID                 string `gorm:"not null;index"`
	Pattern                string `gorm:"not null"`
	Priority               int    `gorm:"not null;default:0"`
	BlockForcePush         bool
	BlockDeletion          bool
	RequirePullRequest     bool
	RequiredApprovals      int
	DismissStaleApprovals  bool
	RequireCodeOwnerReview bool
	RequireStatusChecks    bool
	StatusCheckContexts    string `gorm:"type:text;not null;default:'[]'"`
	RequireSignedCommits   bool
	RequireLinearHistory   bool
	RequireUpToDate        bool
	AllowedPushers         string `gorm:"type:text;not null;default:'[]'"` // user and team ids
	AllowedMergers         string `gorm:"type:text;not null;default:'[]'"`
	BypassIDs              string `gorm:"type:text;not null;default:'[]'"`
}

type GitIssue struct {
	ID              string `gorm:"primaryKey"`
	RepoID          string `gorm:"not null;uniqueIndex:idx_issue_repo_number"`
	Number          int64  `gorm:"not null;uniqueIndex:idx_issue_repo_number"`
	IsPull          bool   `gorm:"not null;index"`
	Title           string `gorm:"not null"`
	Body            string `gorm:"type:text"`
	State           string `gorm:"not null;default:'open';index"` // open, closed
	AuthorID        string `gorm:"not null;index"`
	MilestoneID     *string `gorm:"index"`
	Locked          bool
	LockReason      string
	Pinned          int `gorm:"not null;default:0"` // pin order, zero unpinned
	DueDate         *time.Time
	ClosedAt        *time.Time
	ClosedBy        string
	CommentCount    int `gorm:"not null;default:0"`
	CreatedAt, UpdatedAt time.Time
}

type GitPullRequest struct {
	IssueID              string `gorm:"primaryKey"`
	HeadRepoID           string `gorm:"not null;index"` // fork or same repo
	HeadBranch           string `gorm:"not null"`
	HeadSHA              string `gorm:"not null"`
	BaseBranch           string `gorm:"not null;index"`
	MergeBaseSHA         string
	Draft                bool
	MergeableState       string `gorm:"not null;default:'unknown'"` // unknown, clean, conflict, blocked, checking
	ConflictPaths        string `gorm:"type:text;not null;default:'[]'"`
	ChangedFiles         int
	Additions            int
	Deletions            int
	CommitCount          int
	AllowMaintainerEdits bool `gorm:"not null;default:true"`
	AutoMergeMethod      string // set when auto merge is armed
	AutoMergeBy          string
	MergedAt             *time.Time
	MergedBy             string
	MergeCommitSHA       string
	MergeMethod          string
	HeadDeleted          bool
}
```

Compact listing of the remaining tables, columns beyond the obvious ids and timestamps:

| Table | Columns | Notes |
| --- | --- | --- |
| `org_team_members`, `org_team_repos` | team_id, user_id or git_repo_id | unique pairs |
| `git_collaborators` | repo_id, user_id, permission, accepted | unique (repo_id, user_id) |
| `git_redirects` | namespace, name, repo_id | old addresses after rename or transfer |
| `git_transfers` | repo_id, to_namespace, requested_by, expires_at | pending transfers to users |
| `git_topics` | repo_id, topic | unique pair, topic `^[a-z0-9][a-z0-9-]{0,34}$` |
| `git_stars` | user_id, repo_id | same shape as `stars` |
| `git_watches` | user_id, repo_id, mode | all, participating, ignore |
| `git_tag_protections` | repo_id, pattern, block_deletion, allowed_pushers json |  |
| `git_commit_statuses` | repo_id, sha, context, state, target_url, description, creator_id | unique (repo_id, sha, context), latest wins |
| `git_releases` | repo_id, tag, name, body, draft, prerelease, author_id, target_sha, published_at |  |
| `git_release_assets` | release_id, name, label, size, digest, content_type, uploader_id, download_count | files of a release, unique (release_id, name), blob in the shared store, see 13 |
| `git_release_images` | release_id, image_repo_id, tag, digest | pinned image references on a release |
| `gpg_keys` | user_id, key_id, fingerprint, armored, emails, expires_at |  |
| `api_tokens` | plus scope_type, scope_id, read_only | null scope means today's behavior |
| `repositories`, `artifact_repositories` | plus git_repo_id nullable indexed | the package link |
| `registry_portals` | plus ssh_port | 0 means no SSH listener for the portal |
| `webhooks` | plus git_repo_id nullable, scope `git_repository` |  |
| `lfs_objects`, `git_lfs_links`, `lfs_locks` | see 6.4 |  |
| `git_labels` | repo_id or org_id (one set), name, color, description, exclusive_scope | org labels apply to every org repo |
| `git_issue_labels` | issue_id, label_id |  |
| `git_milestones` | repo_id, title, description, due_date, state, closed_at | counts computed |
| `git_issue_assignees` | issue_id, user_id |  |
| `git_comments` | issue_id, author_id, type, body, payload json, review_id nullable, edited_at | type comment or a timeline event kind |
| `git_reactions` | target_type, target_id, user_id, emoji | issue or comment, unique per user and emoji |
| `git_issue_dependencies` | issue_id, depends_on_id |  |
| `git_tracked_time` | issue_id, user_id, seconds, note | manual entries only, time open is derived from closed and reopened events |
| `git_review_requests` | issue_id, user_id or team_id, requested_by, fulfilled |  |
| `git_reviews` | issue_id, reviewer_id, state, commit_sha, body, submitted_at, dismissed, dismissed_by | pending, approved, changes_requested, commented |
| `git_review_comments` | review_id, issue_id, path, line, side, start_line, diff_hunk, body, resolved, resolved_by, outdated_sha | inline threads |
| `git_projects`, `git_project_columns`, `git_project_issues` | repo_id or org_id, title, public, columns with position, issue placement | private boards show only to collaborators with write on `projects` or org members |
| `git_notifications` | user_id, repo_id, issue_id nullable, sha nullable, kind, status, updated_by, updated_at | status unread, read, pinned |
| `user_notification_prefs` | user_id, email_mode, email_mentions, email_reviews, email_assignments, web_mode | see 19 |
| `mail_messages` | scope_type, scope_id, to, subject, text, html, headers json, status, attempts, last_error, sent_at | the outbound queue |
| `mail_inbound_tokens` | token_hash, user_id, issue_id, expires_at | reply by email addresses |
| `password_resets`, `email_verifications` | user_id, token_hash, expires_at, used_at |  |
| `git_push_mirrors` | repo_id, remote_url, auth json, interval_minutes, sync_on_push, last_sync, last_error, ssh_key_id |  |
| `git_imports` | repo_id, source, upstream, state json, phase, last_error | resumable importer cursor |
| `actions_runners` | owner scope (system, org, repo), name, labels json, token_hash, version, last_seen, status |  |
| `actions_runs` | repo_id, number, workflow_path, event, ref, sha, trigger_user_id, status, conclusion, started_at, finished_at, skip_ci |  |
| `actions_jobs` | run_id, name, runs_on json, needs json, status, conclusion, runner_id, attempt, started_at, finished_at, steps json, outputs json |  |
| `actions_job_logs` | job_id, chunk_index, bytes | appended by the runner, compacted on completion |
| `actions_secrets`, `actions_variables` | owner scope, name, encrypted value or plain value | secrets sealed under the sealing key |
| `actions_artifacts` | run_id, job_id, number, name, digest, size, content_type, status, expires_at | run owned uploads of the results api, blob in the shared store, unique (run_id, name) |

`SettingsRow` gains no columns, the new `SETTINGS_SCOPE_TYPE_REPO = 4` uses the git repo
id as `scope_id` (22).

Migrations in `internal/db/migrations/`: `AutoMigrate` creates the tables, then
`202610MM0002_default_teams.go` creates the `Members` default team for every existing org,
and `AutoMigrate` adds the token scope columns. Packages link to their source repository
when either side is created, by the naming rules in 13, so no backfill runs at upgrade
time, there are no git repositories yet to link.
`Migrate` runs on one pinned connection with `foreign_keys` off for the schema phase,
because the sqlite driver adds a constraint by copying the table and dropping the old one,
and a drop with foreign keys on cascades deletes through every child table.

## 12. Protos (`proto/distroface/v1/*.proto`)

Every rpc gets its own request and response message, buf lint from `buf.yaml` applies.
Request messages are capped at 64 MiB on the connect mount, bulk bytes always travel over
the plain http data planes.
Every list takes `PageRequest` and answers `PageInfo` through `pkg/pages` with a
`stores.*Query` spec. Repo scoped requests carry `namespace` and `name` so
`ObjectIDField: "namespace+name"` works in the interceptor. Commit paging uses the opaque
`page_token` to carry the last commit sha instead of an offset, and `total_count` stays
zero, because go-git's log is an iterator.

Edits to existing files: `types.proto` gains `GitRepoType {STANDARD, MIRROR, FORK,
TEMPLATE}`, `WebhookScope GIT_REPOSITORY = 3`, webhook events `GIT_PUSH, GIT_BRANCH,
GIT_TAG, GIT_ISSUE, GIT_ISSUE_COMMENT, GIT_PULL_REQUEST, GIT_PULL_REQUEST_REVIEW,
GIT_PULL_REQUEST_COMMENT, GIT_RELEASE, GIT_WIKI, GIT_FORK, GIT_REPOSITORY,
GIT_WORKFLOW_RUN`, and `Organization` gains `teams_count`. `token.proto` gains
`CredentialScope`. `settings.proto` gains `GitSettings git = 13`, `MailSettings mail = 14`,
and `SETTINGS_SCOPE_TYPE_REPO = 4` (22). `portal.proto` gains `ssh_port` on the portal
and its create and update requests. `mirror.proto` documents `SyncEvent.kind` values
`image`, `artifact`, `git`, `git_push`, `import`. `organization.proto` gains the team
rpcs. `auth.proto` gains password reset and email verification. `repository.proto` and
`artifact.proto` gain `MoveRepository`, `MoveArtifactRepository`, and a `source_repository`
field on the repo messages with `release_tag` on tags and versions when a release matches.

Services and their rpcs (request fields in parentheses where not obvious):

`GitService` (`git.proto`): CreateGitRepository (namespace, name, description, visibility,
default_branch, object_format, type, mirror, auto_init with readme, gitignore template,
license), GetGitRepository (answers `viewer_units` with the caller's level per unit),
ListGitRepositories (namespace, type, topic, query), SearchGitRepositories,
UpdateGitRepository (description, website, visibility, default_branch, units and external
urls, merge settings, lfs_enabled, mirror), DeleteGitRepository (delete_linked),
MoveGitRepository (new_namespace, new_name), AcceptTransfer, RejectTransfer,
ArchiveGitRepository, UnarchiveGitRepository, ForkGitRepository (target namespace, name),
SyncFork, ListForks, UseTemplate, SetTopics, StarGitRepository, UnstarGitRepository,
ListStarredGitRepositories, WatchGitRepository (mode), SyncGitRepository,
StopGitRepositorySync, ListLinkedRepositories, LinkRepository (kind, namespace, name),
UnlinkRepository, ListCollaborators, AddCollaborator, UpdateCollaborator,
RemoveCollaborator, GetRepositoryActivity, GetRepositoryLanguages, ListContributors,
RunMaintenance.

`GitRefService` (`git.proto`): ListBranches (with last commit, ahead and behind the default
branch, protection flag), GetBranch, CreateBranch (from ref), DeleteBranch, RenameBranch
(admin when the branch is the default or either name is protected, rules naming the old
branch exactly follow it, open pull requests follow before the old name is deleted),
ListTags (natsort desc), GetTag, CreateTag (annotated with message, target), DeleteTag,
SetBranchProtection, ListBranchProtections, DeleteBranchProtection, SetTagProtection,
ListTagProtections, DeleteTagProtection.

`GitFileService` (`git.proto`): GetTree (ref, path, recursive flag, with last commit per
entry on demand), GetBlob (inline up to 1 MiB, binary flag, lfs pointer resolution, raw
url), GetReadme (ref, path), ListCommits (ref, path, author, since, until, cursor),
GetCommit (stats, parents, verification, statuses), CompareRefs (base, head, commits plus
structured diff, a head in another repository is fetched through a staging ref that is
dropped right after and refused while the repository is over its size quota), GetCommitDiff, GetDiffFile (one file, full), Blame (ref, path), SearchCode
(ref, pattern, path glob, paged), UpdateFiles (branch, new_branch, message, author
override, operations create, update, delete, rename with base64 or text content of at most
25 MiB per file, expected head sha), RevertCommit (to branch), CherryPickCommit (to branch), GetArchiveURL,
GetTemplates (issue and pull templates, CODEOWNERS presence).

`ReleaseService` (`release.proto`): CreateRelease (tag, target, name, body, draft,
prerelease), GetRelease (assets, images, source commit), ListReleases, UpdateRelease,
DeleteRelease (assets go with it), PublishRelease, UpdateReleaseAsset (name, label),
DeleteReleaseAsset, AttachReleaseImage (image repo, tag, pins the digest),
DetachReleaseImage, GetLatestRelease, GenerateReleaseNotes (commits since previous tag,
merged PR titles). Asset bytes never ride the RPC: a client streams one request to
`POST /<ns>/<name>/-/releases/<tag>/assets?name=<file>&label=<text>` with the file as the
body and gets the `ReleaseAsset` json back, the same shape octokit uploads to
`uploads.github.com`.

`IssueService` (`issue.proto`): CreateIssue, GetIssue, ListIssues (state, labels, milestone,
assignee, author, mentioned, type issue or pull, sort), SearchIssues (across visible repos),
UpdateIssue (title, body, state, milestone, due date, assignees, labels), LockIssue,
UnlockIssue, PinIssue, UnpinIssue, DeleteIssue, CreateComment, UpdateComment, DeleteComment,
ListComments (timeline with events), AddReaction, RemoveReaction, ListLabels, CreateLabel,
UpdateLabel, DeleteLabel, ListMilestones, CreateMilestone, UpdateMilestone, DeleteMilestone,
AddDependency, RemoveDependency, ListDependencies, AddTrackedTime, DeleteTrackedTime,
ListTrackedTime, SubscribeIssue, UnsubscribeIssue. ListDependencies and ListComments drop
issues, references, and board events the viewer cannot see.

`PullRequestService` (`pull.proto`): CreatePullRequest (base, head as branch or
`ns/name:branch`, title, body, draft), GetPullRequest, ListPullRequests, UpdatePullRequest
(title, body, base, draft toggle, maintainer edits), GetPullRequestDiff (paged files),
GetPullRequestFile, ListPullRequestCommits, GetMergeability (recomputes when unknown),
MergePullRequest (method, message, delete branch, expected head sha), ScheduleAutoMerge,
CancelAutoMerge, ClosePullRequest, ReopenPullRequest, UpdateBranch (merge base into head
or rebase head), RequestReviewers (users and teams), RemoveReviewers, CreateReview (state,
body, inline comments batch), SubmitReview, DismissReview, ListReviews, CreateReviewComment,
UpdateReviewComment, DeleteReviewComment, ResolveReviewThread, ApplySuggestion (commits
the suggestion block through UpdateFiles), CreateCommitStatus, ListCommitStatuses,
GetCombinedStatus.

`WikiService` (`wiki.proto`): ListWikiPages, GetWikiPage (rendered source and raw),
CreateWikiPage, UpdateWikiPage, DeleteWikiPage, ListWikiRevisions. Pages are files in
the wiki repo, every write is a commit through the primitive in 14.4.

`ProjectService` (`project.proto`): CreateProject (repo or org), GetProject (cards show
only issues the viewer may read on their own repository), ListProjects,
UpdateProject, DeleteProject, CloseProject, CreateColumn, UpdateColumn, DeleteColumn,
MoveColumn, AddIssueToProject, MoveIssue (column, position), RemoveIssueFromProject.

`NotificationService` (`notification.proto`): ListNotifications (status filter),
GetUnreadCount, MarkRead, MarkAllRead, PinNotification, WatchNotifications (server stream
of unread count changes, keepalive like `MirrorService.WatchSyncs`), GetNotificationPrefs,
UpdateNotificationPrefs.

`OrganizationService` additions: ListTeams, GetTeam, CreateTeam, UpdateTeam (units,
flags), DeleteTeam, ListTeamMembers, AddTeamMember, RemoveTeamMember, ListTeamRepos,
AddTeamRepo, RemoveTeamRepo. Every team read answers organization members and instance
organization managers only, since a team names private repositories.

`MailService` (`mail.proto`): SendTestEmail (scope), ListMailDeliveries (scope, status),
RetryMailDelivery. `AuthService` additions: RequestPasswordReset (identifier), ResetPassword
(token, new password), RequestEmailVerification, VerifyEmail (token). `CreateInvite` gains
`emails` to send invitations.

`KeyService` (`keys.proto`): AddSSHKey (owner self or repo deploy key, title, public key,
read only, usage, expiry), ListSSHKeys, DeleteSSHKey, AddGPGKey, ListGPGKeys, DeleteGPGKey,
VerifyGPGKey (sign a challenge token).

`ActionsService` (`actions.proto`): ListWorkflows, GetWorkflow, EnableWorkflow,
DisableWorkflow, DispatchWorkflow (inputs), ListWorkflowRuns, GetWorkflowRun, CancelRun,
RerunRun (failed jobs or all), ApproveRun (fork PRs), GetJob, StreamJobLog (server stream),
ListRunArtifacts, ListSecrets, SetSecret, DeleteSecret, ListVariables, SetVariable,
DeleteVariable, ListRunners (scope), CreateRunnerToken, DeleteRunner, UpdateRunner (labels).

`RunnerService` (`actions.proto`, authenticated by runner token in a header):
RegisterRunner, Heartbeat, FetchTask (long poll up to 30 seconds), UpdateTask (step
states, outputs), AppendTaskLog (chunks of at most 1 MiB, 64 MiB per job, past that the
server answers resource exhausted and the runner stops streaming while the job runs on),
CompleteTask. Artifacts never ride this service, the runner speaks the results api with
the runtime token. Finished logs are compacted into one blob a batch of chunks at a time.

## 13. One project: linked packages, releases, and images

Images and artifacts stay first class in their own catalogs, and a git repository can be
their source repository. The link is the nullable `git_repo_id` column. Together with the
same `ns/name` address they form one project.

Linking rules:

1. By name. Image repo `ns/name` and `ns/name/<anything>` and artifact repo `ns/name` link
   to git repo `ns/name`. Applied when the child is created (`RepositoryService.
   CreateRepository`, the registry listener auto create path, `ArtifactService.
   CreateArtifactRepository`) and when the git repo is created, where existing matches
   are adopted only when the creator already holds manage rights on them. The backfill
   migration applies it once to current rows.
2. By label. A manifest push whose config carries `org.opencontainers.image.source`
   pointing at this instance's own clone URL links the image repo to that git repo if it
   is in the same namespace, the GitHub ghcr behavior. Done in the registry observer for
   the image repo the push creates, an existing package is linked by hand.
3. By hand. `LinkRepository` on the git repo (admin level) links an image or artifact repo
   in the same namespace that the caller may manage, since a link shares the package with
   the repository's collaborators. `UnlinkRepository` removes manual and name links alike.

Effects of a link:

- Packages tab on the project page lists linked image repos with their latest tags and
  pull counts and linked artifact repos with their latest versions, every row linking to
  `/images/ns/name` or `/artifacts/ns/name`. The image and artifact pages show a "Source
  repository" line linking back to `/ns/name`.
- Access is the union: `RepoAccess.HasRepoAccess` and `artifacts.Access.HasRepoAccess`
  grant an action when the linked git repo's `packages` unit grants the same action to the
  user through a relationship (owner, org role, team, collaborator, grant, or `GIT_REPO`
  scope), so collaborators, teams, and deploy tokens publish packages without extra
  grants. `LinkedRepoAllows` never counts the read a public git repo hands to everyone, so
  a private package stays private behind a public source. Visibility stays per repo, new
  children default to the source's visibility.
- `DeleteGitRepository` refuses while links exist unless `delete_linked=true`, then deletes
  children through their managers so blobs and registry paths are reclaimed.
- `MoveGitRepository` moves name linked children to the new `ns/name` prefix (25).
- Nested image names (`ns/name/web`) with no matching git repo stay allowed, nothing about
  today's registry behavior changes.

Releases are the hub where a version of the project comes together. A `git_releases` row
carries the tag, the target commit, notes, and draft and prerelease flags. A release page
shows three things, each linking to its home:

- Source. The tag and commit, linking to `/ns/name/commit/<sha>` and the archive downloads.
- Assets. `git_release_assets` rows that belong to the release and nothing else. A row
  carries the file name, an optional label, size, sha256 digest, content type, uploader,
  and a download count, and the bytes live in the shared blob store next to LFS objects and
  run artifacts, reaped when no table references the digest. Uploads stream in one request
  to `POST /ns/name/-/releases/<tag>/assets?name=<file>` (session cookie, bearer or basic
  token, `releases` unit at write), capped by `git.max_release_asset_mb` (2048 by default,
  org scope may lower it), and a name that already exists is refused unless `replace=1`.
  Downloads are `GET /ns/name/releases/download/<tag>/<file>`, the GitHub address, served
  straight from the blob with hardened headers, drafts visible to writers alone. Deleting
  a release deletes its files, deleting the repository reaps them. `dfcli release upload
  ns/name v1.2.3 ./dist/*` streams files, `dfcli release download ns/name v1.2.3 --asset
  '*.tar.gz' -o out` fetches them. The Packages tab lists only real packages, release
  files never appear there.
- Images. Every linked image repo that carries a tag equal to the release tag is listed
  automatically with its digest, platforms, and a `docker pull host/ns/name:v1.2.3` line,
  linking to `/images/ns/name`. `AttachReleaseImage` pins an explicit reference (`ns/name/
  web:v1.2.3`) to the release with its digest at attach time, so a retagged image still
  points at the bytes the release shipped, and lets a release list images whose tags do not
  match the release tag.

The reverse links exist too. On the image page a tag equal to a release tag, or pinned by
a release, shows a Release badge linking to `/ns/name/releases/<tag>`. On the artifact page
a version equal to a release tag shows the same badge. The project sidebar shows the
latest release with its asset and image counts.

A mirrored git repo created with "also mirror releases" (`mirror_releases` on its
`MirrorConfig`, editable on the mirror settings page) copies the upstream release list on
every sync: `git_releases` rows are created or refreshed with notes and flags, every asset
file is streamed onto its release, files and releases the upstream dropped are removed, and
the host must be a GitHub, GitLab, Gitea, or Forgejo instance because the listing comes
from its release api. Release package mirrors that used to hang off git mirrors are folded
into this on the first sweep after upgrade. "Also mirror packages" creates image
mirrors for the upstream's container registry under `ns/name` and `ns/name/<child>`.

Workflow run artifacts (21) are `actions_artifacts` rows owned by their run, uploaded and
downloaded through the GitHub Actions results api, never package contents.

## 14. Code browsing and web editing (`internal/gitrepo/reads.go`, `diff.go`, `commit.go`)

### 14.1 Reads

All reads open a `*git.Repository` and use go-git objects, the same way `RegistryAccess`
walks Distribution storage:

- Refs: `Branches()`, `Tags()` with annotated tag peeling, `Head()`. Branch ahead and
  behind counts against the default branch come from `MergeBase` plus a bounded log walk,
  computed on demand and cached by `(branch sha, default sha)`.
- Tree and blob: a ref resolves as a branch first, then a tag, then a full hash, and only
  then through `ResolveRevision` for expressions and abbreviated hashes, so a branch named
  `beef` or `2024` is never mistaken for a commit prefix. Then `CommitObject`,
  `Tree.Tree(path)`, `Tree.File`.
  Last commit per entry is a second call because it is a log walk per path and the UI
  loads it lazily. Blob answers include size, mode, binary detection, LFS pointer parsing
  (`version https://git-lfs.github.com/spec/v1`, `oid sha256:`, `size`), and the raw URL.
- README: first of `README.md`, `README`, `README.txt`, `readme.*` in the root or a
  `.distroface/` or `docs/` folder, rendered client side.
- Commits: `Log(&LogOptions{From, Order: LogOrderCommitterTime, PathFilter, Since,
  Until})`. Author filter matches email or name. Cursor paging as in 12.
- Commit detail: parents, `Stats()`, verification (9.4), statuses, the diff from 14.3.
- Blame: `git.Blame(commit, path)` grouped into runs of consecutive lines by commit.
  Binary files are refused, files over 1 MiB and walks over thirty seconds answer
  resource exhausted. Recursive tree listings stop at 100000 entries and set
  `truncated`.
- Code search: `Repository.Grep(&GrepOptions{Patterns, ReferenceName, PathSpecs})`,
  repo scoped on one ref, results capped at 500 lines. Instance wide code search is not
  offered because go-git has no index, Gitea without its indexer has the same limit.
- Languages: go-enry over the tree at the default branch, bytes per language, cached by
  tree hash.
- Contributors and activity: one log walk per head sha, cached, aggregates by author
  email and by ISO week.

### 14.2 Reserved paths in the tree

`.distroface/` is the repo's configuration folder. `ISSUE_TEMPLATE.md`,
`ISSUE_TEMPLATE/*.md|yaml` (Gitea and GitHub form syntax), `PULL_REQUEST_TEMPLATE.md`,
`CODEOWNERS`, `workflows/*.yaml`. The reader falls back to `.gitea/` and `.github/` so
imported repos work unchanged. Root `CODEOWNERS` and `docs/CODEOWNERS` are also honored.

### 14.3 Structured diff

`object.DiffTreeWithOptions` with rename detection, then `Changes.PatchContext`. The proto
is `DiffFile{old_path, new_path, status, mode changes, is_binary, is_submodule, is_lfs,
additions, deletions, truncated, hunks[]}` with `DiffHunk{old_start, old_lines, new_start,
new_lines, header, lines[]}` and `DiffLine{kind context|add|del, old_no, new_no, text}`.

Diffs are served in two shapes. File listings (`GetCommitDiff`, `GetCompareDiff`,
`GetPullRequestDiff`) take a `PageRequest`, sort changes by path, filter them by
`page.query.text` substring, and return one page of `DiffFile` rows without hunks plus
`total_files`, `additions`, and `deletions` over every matching file. Single file reads
(`GetDiffFile`, `GetPullRequestFile`) return the hunks of one path. Per file changed lines
cap at 20000, above that `truncated` is set and hunks are withheld on every read. Whole
file diffs over 2 MiB are reported as binary. Past the first 1000 changed files a listing
carries the remaining paths without stats or hunks, each marked `truncated`, and the diff
sets its own `truncated` so the totals are known to be partial. Line counts and binary
detection per blob pair are memoized process wide, so paging and totals cost one patch per
pair. Commit
ranges (`ListCompareCommits`, `ListPullRequestCommits`) page an oldest first hash list
memoized per `(merge base, head)` pair with an exact total.

The UI renders a file tree beside a single file pane: the tree is one server page of the
listing, the pane fetches the selected file on demand, lines soft wrap in both unified and
split modes so neither side scrolls sideways, and rows mount in chunks as the pane scrolls.

### 14.4 Commit primitive

`commit.go` creates commits in a bare repository without a worktree, which is what web
editing, wiki, merges, reverts, cherry picks, suggestions, template instantiation, and
`auto_init` all need:

```go
type TreeEdit struct {
	Path    string
	Mode    filemode.FileMode
	Content io.Reader // nil deletes the path
	Rename  string    // old path moved here
}

// Writes blobs, rebuilds the touched trees, commits, updates the ref
func (e *Engine) Commit(ctx, repo, ref, expectedHead plumbing.Hash, edits []TreeEdit, msg string, author, committer object.Signature, signer git.Signer) (plumbing.Hash, error)
```

Blobs are written as `plumbing.MemoryObject` through `storer.SetEncodedObject`, trees
are rebuilt bottom up from the parent commit's tree with `object.Tree.Encode`, the commit
is encoded with `object.Commit.Encode`, and the ref update is compare and set against
`expectedHead` so two editors never silently overwrite each other. The repo writer mutex
is held. The ref update then runs the same `PostReceive` fan out as a push, so web edits
trigger webhooks, PR updates, issue closing, and Actions exactly like a push, with
`PreReceive` protection applied first.

Web editing in the UI: new file, edit, upload, delete, rename, with a CodeMirror editor,
commit to the branch or to a new branch with "open a pull request" like GitHub. Author
identity is the user's display name and email, or `<username>@noreply.<public_hostname>`
when the account has no email.

## 15. Issues (`internal/forge`)

Issues and pull requests share `git_issues` and one per repo number sequence
(`IssueCounter` incremented in the create transaction, safe under the store's
`_txlock=immediate`). Features, each a small manager method and rpc:

- Markdown body and comments, edited with history (`edited_at`, previous bodies kept in
  `payload`), rendered client side with `#N`, `ns/name#N`, `@user`, `@org/team`, and sha
  autolinks, task lists that write back through `UpdateIssue`.
- Labels per repo and per org (org labels appear on every org repo), colors, exclusive
  scopes (`kind/bug` and `kind/feature` cannot coexist), label search in lists.
- Milestones with due dates, open and closed counts, progress.
- Assignees, multiple.
- Timeline events stored as typed comments: labeled, unlabeled, assigned, unassigned,
  milestoned, demilestoned, closed, reopened, referenced (from a commit or another issue
  or PR), renamed, locked, unlocked, pinned, due date changed, dependency added,
  dependency removed, blocker closed, blocker reopened, time tracked, review requested,
  head pushed, force pushed, merged, auto merge armed, converted to draft, ready for
  review, project added, moved, removed.
- Reactions on issues and comments from the fixed set `+1 -1 laugh hooray confused heart
  rocket eyes`.
- Cross references: writing `#N` or `ns/name#N` in a body or commit message records a
  "referenced" event on the target when the author can read it.
- Dependencies: an issue blocked by another cannot be closed until the blocker closes,
  toggle `issues.block_on_dependencies` per repo. Closing or reopening a blocker writes
  a `blocker_closed` or `blocker_reopened` event on every issue it blocks, and the sidebar
  keeps resolved blockers in a muted "n of m resolved" list under the open ones.
- Time tracking, off by default and switched on per repo with `units.time_tracking`: the
  sidebar shows how long the issue has been open, summed from the closed and reopened
  events on the server and advanced once a minute on the client, plus optional manual
  "time spent" entries. There is no stopwatch and nothing ticks in the backend.
- Pins (max 3 shown first), locks with reason, due dates.
- Templates from 14.2, including YAML forms rendered as a form that produces the body.
- Bulk edit in lists: labels, milestone, assignees, state.
- Search across visible repos with `pages.Spec` on title and body and the same filters as
  the per repo list.
- Notifications, email, and watching (18, 19), webhooks `GIT_ISSUE` and
  `GIT_ISSUE_COMMENT` (23).

Permission: read on the `issues` unit opens issues and comments when signed in, write
edits others' issues and manages labels and milestones, authors edit their own. Locked
issues accept comments from write and above only. Anonymous never writes.

## 16. Pull requests and reviews

### 16.1 Lifecycle

`CreatePullRequest` resolves base `refs/heads/<base>` in the repo and head either as a
branch in the same repo or `ns/fork:branch`. A fork reaches this form from its own pages:
the code tab of a fork shows how far a branch is ahead of or behind the upstream with an
"Open pull request" shortcut, and the compare and new pull request pages offer the upstream
as a base repository, which moves the flow to the upstream with the fork branch as head. For a fork head the engine fetches the head
commits into the base repo with `Repository.FetchContext(RemoteURL: file://<fork
path>, RefSpecs: <branch>:refs/pull/<n>/head)`, go-git's file transport, in process. For a
same repo head it writes `refs/pull/<n>/head` directly. `refs/pull/<n>/head` is refreshed
on every head push (8.2) so `git fetch origin pull/<n>/head` works for reviewers.
`MergeBaseSHA` comes from `object.Commit.MergeBase`. Commits, changed files, additions and
deletions are cached on the row and recomputed when either side moves. Drafts, ready for
review, close, reopen, retarget base, and `UpdateBranch` (merge base into head, or rebase
head onto base and force update, each through 16.3) complete the lifecycle. Closing a
merged PR with "delete branch" or the repo's `delete_branch_on_merge` setting removes the
head branch when it is not protected and not the default branch, and `HeadDeleted` lets the
UI offer "restore branch" while the ref is still reachable from `refs/pull/<n>/head`.

### 16.2 Mergeability

`GetMergeability` runs the merge engine in memory (no object writes) between base tip and
head tip with the merge base as ancestor, stores `clean` or `conflict` with the conflict
paths, and `blocked` when protection requirements from 16.5 are not met. The state is
`unknown` after either side moves and recomputed on view, so stale answers never show.

### 16.3 Merge engine (`internal/gitrepo/merge.go`)

go-git does not ship a three way merge, so this file is ours. It operates on trees and
blobs only, never a worktree:

1. `DiffTree(base, ours)` and `DiffTree(base, theirs)` with rename detection give two change
   sets keyed by path. A rename is a linked delete plus insert, and before paths are
   merged the other side's edit to the old path is carried to the new one and merged there
   against the old base content, both sides renaming to one place merge at that place,
   renaming to two places, renaming against a delete, and renaming onto a path the other
   side added all conflict with kinds `rename`, `rename_delete`, and `rename_add`.
0. The base is every merge base of the two commits: one base is its tree, a criss-cross
   history merges its bases first through this same engine into a virtual base tree, eight
   levels deep, and falls back to the first base when the bases themselves conflict.
2. Paths changed on one side only take that side. Paths deleted on one side and
   unchanged on the other delete. Paths with identical results on both sides take either.
3. Paths changed on both sides: both deleted deletes, one deleted and one modified
   conflicts (modify delete), both modified with the same mode run a line level diff3
   over the three blobs using `sergi/go-diff` line diffs against the base, non
   overlapping hunks apply cleanly, overlapping hunks conflict. Binary blobs conflict
   unless identical. Mode conflicts conflict. Submodule pointers conflict unless equal.
   Directory and file collisions at the same path conflict.
4. The result is a new tree built with the primitive from 14.4, plus the list of
   conflicted paths each with the three blob hashes and, for text, a merged buffer with
   `<<<<<<<`, `=======`, `>>>>>>>` markers that the UI shows read only.

Merge methods built on it: merge commit (two parents, message `Merge pull request #n from
ns/branch` plus title and body), squash (one commit on base with the merged tree, message
from title and commit list, author the PR author, committer the merger), rebase (each PR
commit re-applied on base in order through the engine with the commit's parent as the
ancestor, conflict aborts), rebase and merge (rebase then a merge commit), fast forward
only (refused when not an ancestor). Every method ends with a compare and set ref update
of the base branch through the same `PreReceive` and `PostReceive` path as a push, with a
context flag that satisfies `require_pull_request`. Merge commits are signed with the
instance key when enabled. Revert and cherry pick (12) are the same engine with the
commit's parent as the ancestor.

### 16.4 Reviews

Reviews follow the GitHub model. A review is a batch of inline comments plus a state,
pending until submitted. Inline comments anchor to `path`, `line`, `side` (old or new),
optional `start_line` for ranges, and keep the `diff_hunk` text so they render after the
head moves, marked `outdated` when the anchored line no longer exists in the new head
(detected by re-diffing against the commit the comment was made on). Threads resolve and
unresolve. Suggestion blocks (```` ```suggestion ````) get an Apply button that calls
`ApplySuggestion`, which commits the replacement to the head branch through 14.4 when the
caller may push there or when `AllowMaintainerEdits` admits a base repo writer. The
suggested lines are located in the current head by diffing the file against the commit the
comment was made on, and the apply is refused when any of those lines changed.
Reviewers are requested by hand, as users or teams, or by `CODEOWNERS` patterns on the
changed paths, owners are users, `@org/team`, or emails. Approvals count toward
`required_approvals`, authors cannot approve their own PR, dismissed reviews keep their
comments and lose their weight, and `dismiss_stale_approvals` drops approvals when the
head moves.

### 16.5 Protection checks on merge

`MergePullRequest` evaluates the base branch protection: required approvals from
reviewers with write level on `pulls` (the newest review per reviewer decides, so a
dismissed newest review leaves no approval behind, and imported reviews never count), code
owner review when required (an owner entry that resolves to nobody blocks rather than
passing), required status check contexts all `success` on the head sha (statuses from
Actions or from `CreateCommitStatus`, the Gitea commit status API external CI uses, and
with no contexts named every reported check must pass and at least one must have
reported), no changes requested reviews outstanding, signed commits and linear history
when required (a history beyond 1000 commits blocks instead of going unchecked), head not
behind base when `require_up_to_date` is set, and the merger in `allowed_mergers` when the
list is non empty. Failures answer FailedPrecondition with the list of unmet rules the UI
shows. `ScheduleAutoMerge` arms a merge that `PostReceive` and status updates retry when
the state flips to clean and all rules pass, performed as the scheduling user. A head push
by anyone else disarms it, so nobody rides a maintainer's approval with new commits. In
pre receive the signed commit and linear history rules check exactly the commits the push
introduces (`rev-list new --not old`, or `--not --all` for a new branch), refuse more than
10000 at once rather than skipping any, and treat an unverifiable force push as blocked.

## 17. Wiki

A second bare repo `<name>.wiki.git` created on the first page write, clonable over HTTP
and SSH with the `wiki` unit's levels (read for pull, write for push). Pages are markdown
files named from the title (`Home.md` is the landing page), `_Sidebar.md` and `_Footer.md`
render around every page. The UI edits through `WikiService`, every write is a commit
through 14.4 by the editing user, history comes from `Log` on the page path. The wiki
appears on the repo nav when the unit is enabled, and `DeleteGitRepository` removes it too.

Editing uses `markdown-editor.svelte`, a plain textarea with a formatting toolbar, Enter
continuing list markers, Ctrl+B, Ctrl+I, and Ctrl+K, a page picker that inserts
`[[Page Title]]`, an issue picker that inserts `#12`, and a formatting help popover. No
line numbers and no CodeMirror on wiki pages, that editor stays for source files. The
renderer (`$lib/markdown.ts`) resolves `[[Page]]` and `[[Text|Page]]` against the wiki
directory, autolinks `#12`, `ns/name#12`, and `@user` against the current repository on
every markdown surface, and loads relative images from
`/ns/name.wiki/-/raw/HEAD/<path>` so files committed to the wiki repo render inline.

## 18. Notifications and watching

`git_watches` holds explicit modes. Default watching is `participating`: a user receives
notifications for issues and PRs they authored, are assigned to, were mentioned in,
commented on, or review. `all` adds every new issue, PR, release, and push on the repo,
`ignore` silences it. Creating or forking a repo watches it with `all`. Notification rows
are created by the forge managers and the post receive fan out, deduplicated per
`(user, repo, issue)` with `updated_at` bumped and status reset to unread. The fan out runs
in the background after the request returns, loads preferences and subscriptions in bulk,
and drops recipients who are inactive or cannot see the repository, so removed
collaborators and mentioned teams never receive private content. Invitations and transfers
reach their explicit recipient without that check. Each event also asks the mailer (19) to
send according to the recipient's `user_notification_prefs`. The UI
shows a bell with the unread count fed by `WatchNotifications`, and `/notifications` lists
them with mark read, mark all read, pin, and filters by repo. `/settings/notifications`
edits the web and email preferences.

## 19. Mail (`internal/mailer`)

The app has no mail subsystem today, so this feature adds one, with SMTP settings at the
instance, organization, and repository tiers and a queue backed sender every other feature
calls.

### 19.1 Settings

`MailSettings mail = 14` in `Settings`:

```proto
message MailSettings {
  optional bool enabled = 1;
  optional string smtp_host = 2;
  optional int32 smtp_port = 3;
  optional MailSecurity security = 4;          // NONE, STARTTLS, TLS
  optional string username = 5;
  optional string password = 6;                // write only, reads redact
  bool password_set = 7;                       // output only
  optional string from_address = 8;
  optional string from_name = 9;
  optional string reply_to = 10;
  optional bool allow_private_networks = 11;   // smtp relays are usually internal, default true
  optional bool skip_tls_verify = 12;
  optional int32 max_per_minute = 13;          // zero unlimited
  MailIncomingSettings incoming = 14;          // enabled, imap_host, imap_port, tls, username, password, mailbox, reply_address
}
```

Tiers: system sets the instance mailer. Org scope and a new `SETTINGS_SCOPE_TYPE_REPO = 4`
with the git repo id as `scope_id` may bring their own relay. `settings/resolver.go`
`Effective` builds the chain system, then org when the repo's namespace is an org, then
repo, and `sealMail` keeps the relay and sender atomic below system: a tier that sets
`smtp_host` supplies host, port, security, credentials, tls policy and sender itself and
inherits none of them, a tier that does not set `smtp_host` contributes only `enabled`.
`max_per_minute`, `allow_private_networks` and `incoming.*` are system only, so a lower
tier can neither lift the throttle, point the server at private hosts, nor spoof the
instance sender through the instance relay. `mailer.ScopeFor` picks the tier owning the
relay (or the nearest tier that turned mail off) so throttle windows follow the relay.
`SettingsService.requireScopeAdmin` treats REPO as the repo's admin level and
`requireScopeRead` as its read level. `Redact` strips the password the way it strips the
OIDC secret. Settings UI: `/admin/mail`, an Email card in `org-settings-manager.svelte`,
and an Email card in repo settings, all through `patchSettings` with `repoScope(repoId)`
added to `settings-utils.ts`. "Send test email" lives on each.

### 19.2 Sending

`mailer.Service` resolves the effective settings for the scope of the event (repo, then org,
then system, first tier with `enabled` and a host wins), renders a template, and enqueues a
`mail_messages` row. A worker drains the queue with the webhook dispatcher's retry shape
(five attempts, exponential backoff, `last_error`, per message log admins can inspect
through `ListMailDeliveries` and `RetryMailDelivery`), rate limited by `max_per_minute` per
scope, connecting through `wneessen/go-mail` with the dial guarded by `checkMirrorIP` unless
`allow_private_networks`. A message the worker gives up on after the fifth attempt is
audited as `Mail/send` with the recipient and the last error. `mailer.Render` produces a
text and an html body for every templated mail from one layout with the instance name,
and the footer carries the reason the mail was sent and an unsubscribe link with a signed
token into `UpdateNotificationPrefs`. The token is an HMAC under a key derived from the
live signing secret (`auth.Manager.DerivedKey("notifications")`), names the user and an
expiry thirty days out, and is neither issued nor accepted when no key is installed. Every message sets `List-Id`, `Message-ID`, and
threading headers so an issue's mails thread in clients.

Consumers:

- Notifications (18): issue and PR opened, commented, closed, merged, review requested,
  review submitted, assigned, mentioned, release published, push to watched repo, per the
  recipient's `email_mode` (all, participating, none) and the per kind toggles.
- Collaborator invitations and team additions.
- Registration invites: `CreateInvite` gains `emails`, each receives the invite link.
- Password reset: `RequestPasswordReset` always answers success, sends when the account is
  local with an email, `password_resets` tokens expire in one hour, `ResetPassword` rotates
  the password and clears sessions. UI `/forgot-password` and `/reset-password/[token]`.
- Email verification: `auth.require_email_verification` (default false) blocks login for
  local accounts until `VerifyEmail` runs, `users.email_verified_at` records it.
- Admin test email.

### 19.3 Incoming mail

When `mail.incoming.enabled` is set, a poller using `emersion/go-imap/v2` with IDLE watches
the configured mailbox. Outbound notification mails about an issue or PR set `Reply-To` to
`reply_address` with a `+<token>` extension, the token is a `mail_inbound_tokens` row bound
to the recipient and the issue. A reply whose address carries a valid token is parsed with
`emersion/go-message`, the text part is stripped of quoted history and signatures, and the
remainder posts as a comment by that user through `IssueService.CreateComment` with a
"via email" marker, but only when the sender address is the account's own email and the
account is active, so a leaked token is useless from any other mailbox. Mail carrying
`Auto-Submitted`, a bulk or list `Precedence`, or an auto reply marker is marked seen and
dropped, which with the outbound `Auto-Submitted: auto-generated` and
`X-Auto-Response-Suppress: All` headers keeps out of office responders from looping.
IMAP uses implicit TLS or STARTTLS and never falls back to a cleartext login. Unknown
tokens and unparsable mails are logged and left in the mailbox folder `Failed`.

## 20. Mirrors, push mirrors, and imports (`internal/mirror`)

### 20.1 Pull mirrors

A fourth kind in `Monitor` beside image and artifact, key `git:<id>`, same `try`,
`armCancel`, `nextState`, cooldown, and `SyncEvent` plumbing. `MirrorConfig` is reused:
`upstream` is an https or ssh git URL (or `owner/repo` shorthand for github, gitlab,
gitea when the type says so), `auth_token` becomes basic auth on https (`x-access-token`
for github, `oauth2` for gitlab, the literal `username` when set, otherwise the token as
password with username `token`), `pattern` filters ref short names, `sync_interval_minutes`
and `paused` apply, `sync_depth` and `include_prereleases` are ignored for git. Every
stored `auth_token`, on pull and push mirrors of all three kinds, is sealed under the
sealing key with an `enc:v1:` envelope, plain rows from before are sealed on boot, and an
update that moves a mirror to another host drops the stored token unless a new one comes
with it. An ssh upstream whose repository key is missing or unreadable fails outright, the
server's own ssh agent is never consulted.

Fetch is `Repository.FetchContext(ctx, &FetchOptions{RemoteURL, RefSpecs: {"+refs/heads/
*:refs/heads/*", "+refs/tags/*:refs/tags/*"}, Prune: true, Force: true, Tags: AllTags,
ClientOptions: {client.WithHTTPClient(m.client), client.WithDialer(m.dial)}})` under the
repo writer mutex. The `http.Client` is the monitor's existing one, so `safeTransport`
vets every dial against `checkMirrorIP`, and the same `safeDialer` is handed to the ssh
and git transports for pull and push mirrors, so an ssh or scp style upstream on a private
address fails at dial time like an https one unless `mirror.allow_private_networks` is on.
`pacedTransport` spaces requests per host, redirects are capped, and
`allow_private_networks` applies. Rate limit answers (HTTP 429 or go-git's
`ErrAuthorizationFailed` with a `Retry-After`) map to `rateLimitedError`. The resulting ref
changes run the `PostReceive` fan out with the mirror as actor so webhooks, PR updates, and
Actions fire for mirrors too (Actions only when `actions.run_on_mirror` is on). Mirror repos
refuse client pushes with "repository is a mirror".

SSH upstreams: `GenerateMirrorKey` creates an ed25519 key pair per repo stored encrypted
in `MirrorState`, the public half is shown for the user to install upstream as a deploy
key, fetch uses `client.WithSSHAuth` with that key and a host key callback pinned on first
contact (stored in `MirrorState`). The same key serves push mirrors.

Validation probes with `transport` `Handshake` plus `GetRemoteRefs` under a 20 second
timeout. `ValidateGitMirror` sits beside the other two validators. Creating a git mirror
offers "also mirror releases", which turns on the release sync described in 13, and "also
mirror packages", which creates the linked image mirrors described there, each image
mirror an independent row with its own sync.

### 20.2 Push mirrors

`git_push_mirrors` rows, several per repo. `Repository.PushContext(ctx, &PushOptions{
RemoteURL, RefSpecs: {"+refs/heads/*:refs/heads/*", "+refs/tags/*:refs/tags/*"}, Force:
true, Prune: true, ClientOptions})` on the interval and on push when `sync_on_push` is
set, key `git_push:<id>`, same status and cooldown bookkeeping. That is `git push
--mirror` semantics, diverged remote branches are overwritten and branches that exist only
on the remote are deleted, and the form says so before a mirror is added. Credentials are
a token or the generated ssh key.

### 20.3 Imports

"Import a repository" creates a standard repository and fills it once. It is never a
mirror: nothing syncs afterwards, pushes land like on any other repository, and no mirror
settings or badge exist for it. The source is any https or ssh clone url (owner/repo
shorthand works for GitHub, GitLab, and Gitea), its credentials stay sealed for the
duration of the import and are dropped with the upstream url when it ends. Branches and
tags always come over. Optionally the importer fetches the upstream wiki repository,
downloads LFS objects through the upstream batch api into the blob store, and, when the
host is a GitHub, GitLab, Gitea, or Forgejo instance, imports metadata through a
`forgeDriver` interface beside `releaseDriver`: labels, milestones, issues with comments,
pull requests (as closed or open PRs whose head refs are fetched from the upstream
`refs/pull/<n>/head` or `refs/merge-requests/<n>/head`), reviews where the API exposes
them, and releases. A host without a recognised forge api still imports, the metadata
step is recorded as a note. The importer is resumable through `git_imports.state` and
runs as a monitor job with `SyncEvent.kind=import` so the UI shows progress. While a phase
runs, pushes and web edits answer "repository import is in progress" so the fetch cannot
clobber them. A failed import leaves the repository writable with what arrived and the
header offers retry (`SyncGitRepository` resumes the recorded import) and dismiss
(`DismissGitImport` stops it and forgets the record). `GitRepository.import_status`
carries upstream, phase, error, and notes until the import finished without notes.
Upstream authors are never mapped to local accounts: every imported issue,
comment, review, and release carries the upstream login in its "imported" marker and no
local author, so a hostile upstream cannot speak as a local user, and imported reviews are
flagged `imported` and never count toward required reviews. Comments and reviews are
fetched before the issue row is written, so a cut off import resumes without gaps.

## 21. Actions (`internal/actions`)

The largest phase and the last one. Workflow files live in `.distroface/workflows/*.yaml`
with `.gitea/workflows` and `.github/workflows` as fallbacks, written in the GitHub Actions
syntax. Nothing about the syntax, the expression language, the action formats, or the job
execution is homegrown: the server plans with the parser Gitea ships (a copy of its
`jobparser` and `workflowpattern` packages under `internal/actions/`, built on the act
model from `gitea.com/gitea/runner`), and the runner executes with act itself, the same
library behind Gitea's and Forgejo's runners. Every action on github.com runs as is.

Server side:

- Workflow loading (`workflows.go`) reads a file with `model.ReadWorkflow`, decode errors
  surface as the run's error instead of act's default fatal exit, and `on` is parsed with
  `jobparser.ParseRawOn`.
- Triggers (`triggers.go`) from `PostReceive` (push, tag), the forge (pull_request and
  pull_request_target on opened, synchronize, reopened, closed, issues, issue_comment,
  release), `DispatchWorkflow` (workflow_dispatch with typed inputs validated against the
  declaration, the payload keeps strings like GitHub and the `inputs` context is typed),
  and a scheduler for `schedule` crons. Event matching is the Gitea matcher ported onto
  `jobparser.Event`: branches, tags, paths and their ignore forms (paths diff the push
  range, or the merge base against the head for pull requests), types, and dispatch
  inputs. Fork PR runs wait for `ApproveRun` by a user with write on `actions` unless the
  pull request author already has it. `pull_request` runs whose head lives in a fork are
  marked `from_fork` for life: no secrets and a read only job token. `pull_request_target`
  reads the base workflow, runs on the base sha, and keeps secrets. `[skip ci]` in the head
  commit message and the `-o skip-ci` push option skip. The `github` context for a pull
  request points `ref` at `refs/pull/N/head`, the ref the server maintains.
- Planning (`plan.go`) is `jobparser.Parse` with the git context, variables, and inputs:
  matrix expansion with include and exclude (capped at 256 jobs, an oversized or invalid
  matrix becomes one failed job carrying the reason), `runs-on`, `needs`, `strategy`,
  `concurrency` at both levels, `timeout-minutes`, `continue-on-error`, `permissions`
  (neither `contents: write` nor `packages: write` nor `write-all` means a read only job
  token), and `run-name`. Each job row stores its own single job workflow
  (`WorkflowPayload`) the way Gitea does, so the runner never sees another job. A matrix
  or a job setting written over `needs` outputs stays one deferred row until its needs
  finish and expands in place. Reusable workflows expand server side (`expandCaller`):
  `./path` at the caller's commit or `owner/repo/path@ref` on this instance, and a host
  reference fetched raw from the default actions url, typed `inputs`, `secrets` as a map or
  `inherit` narrowed caller by caller, called jobs inlined as `<caller>/<called>` rows
  under a `ParentJobID`, `needs.<caller>.outputs.<name>` resolved through the called
  workflow's `outputs`, nesting capped at four levels, cycles and empty called workflows
  refused with the reason. Where each called workflow came from is kept on the row so a
  `./` reference inside a called workflow resolves against that workflow's repository.
- The emitter (`emit.go`) is Gitea's `job_emitter` ported: when a job finishes, the run is
  advanced under a per run lock, `needs` results and merged outputs are built, `if` is
  evaluated with `jobparser.EvaluateJobIfExpression` (an empty `if` means every need
  succeeded, `continue-on-error` jobs count as success downstream), `fail-fast` cancels
  the queued and running siblings of a failed matrix leg, concurrency groups cancel in
  progress at the workflow and at the job level, and expansion of callers and deferred
  rows is a conditional update so two runners finishing needs at once expand once. A job
  whose `runs-on` labels match no runner registered for the repository, its organization,
  or the instance fails at queue time with the labels in its annotation, and a queued job
  no runner took within 24 hours fails the same way.
- Runs and jobs are rows, jobs become tasks a runner claims through `FetchTask`: the
  runner's scope is filtered in sql, queued jobs are examined in pages with their runs and
  repositories loaded in bulk, labels are matched in memory, `max-parallel` is enforced in
  the atomic claim. The task carries the single job workflow, the `github` context json
  (`run_id` and `repository_id` are the numeric ids the REST shim accepts, so
  `parseInt(GITHUB_RUN_ID)` in `@actions/github` works), the event payload, secrets for the
  scope (system, org, repo), variables, the `needs` results, a `GITHUB_TOKEN` analog (a
  short lived `GIT_REPO` scoped API token revoked when the job ends), the runtime token (an
  HS256 JWT with `scp: Actions.Results:<run>:<job>` the artifact and cache apis check), the
  timeout, and the default actions url. The registry token handler signs registry tokens
  minted with a job token with the subject `actions-job/<job>/<user>`, so every tag pushed
  with that token is attributed to the run (`actions_run_images`), listed on the run page,
  and pinned on the release of a tag ref.
- Artifacts are the GitHub Actions results api (`artifactsv4.go`): the twirp
  `ArtifactService` at `/twirp/github.actions.results.api.v1.ArtifactService/` with
  CreateArtifact, UploadArtifact (block upload with `comp=block`, `appendBlock`,
  `blocklist`), FinalizeArtifact (size and sha256 verified), ListArtifacts,
  GetSignedArtifactURL (HMAC signed, one hour), and DeleteArtifact, authenticated by the
  runtime token and bound to the run in it, so `actions/upload-artifact@v4` and
  `actions/download-artifact@v4` run unmodified. The legacy pipeline api at
  `/api/actions_pipeline/` serves v1 to v3 of the same actions and the `setup-*` caches.
  Rows are `actions_artifacts` owned by the run with the blob in the shared store, listed
  on the run page and downloadable at `/ns/name/-/actions/artifacts/<id>/<name>.zip` with
  the session, exposed on the REST shim at `/repos/{o}/{r}/actions/runs/{id}/artifacts`
  and `/actions/artifacts/{id}/zip` for cross run downloads, expired after
  `git.actions.artifact_retention_days` (410 once expired), and reaped with the repository.
  `ACTIONS_CACHE_URL` serves the artifactcache protocol backed by the same blob store with a
  10 GiB per repository cap. `GITHUB_API_URL` is `<base>/api/v1`, the address act hands
  every job, where the GitHub REST compatibility layer (`internal/ghapi`) answers the
  calls octokit based actions make with `GITHUB_TOKEN`.
- Logs stream from the runner in chunks into `actions_job_logs`, served to the UI by
  `StreamJobLog` (the stream tracks bytes sent so the tail of a fast job is never lost),
  compacted into one blob on completion, pruned after `actions.log_retention_days`.
  Server side failures (no runner, planning errors) write an error line into the transcript
  so a red job always explains itself.
- Job conclusions write `git_commit_statuses` with context `<workflow> / <job>` so branch
  protection required checks and the PR checks panel work with no special casing.
- Secrets are sealed with an AES-GCM key derived from the sealing key, never from the
  session secret. Variables are plain, both scoped system, org, or repo.
- Runners register with a one time token per scope, carry labels, heartbeat, and show
  online or offline. Admin pages at `/admin/runners`, org settings, and repo settings.
- Starter workflows come from `github.com/actions/starter-workflows` (or
  `git.actions.starter_workflows_url`), cloned into `<data_dir>/starter-workflows` and
  refreshed daily in the background, served by `ListWorkflowTemplates` and
  `GetWorkflowTemplate` with GitHub's placeholders filled (`$default-branch`,
  `$protected-branches` from the repository's protection rules, `$cron-*`,
  `$codeql-languages-matrix` from the detected languages) and grouped the way GitHub's
  "New workflow" page groups them. No template catalog is maintained in this repository.

Runner (`dfcli runner register --server ... --token ...` and `dfcli runner start`, the scope rides on the token):

- Job execution is act (`pkg/runner/job.go` builds an `act/runner.Config` the way
  act_runner does): `runs-on` labels map to images in the runner config (`ubuntu-latest`
  is `ghcr.io/catthehacker/ubuntu:act-latest`), `-self-hosted` runs on the host when the
  runner allows it, `services` and `container` with `credentials`, `pre`, `main`, and
  `post` phases, composite, docker, and node20 or node24 actions, `$GITHUB_OUTPUT`,
  `$GITHUB_ENV`, `$GITHUB_PATH`, `$GITHUB_STEP_SUMMARY`, workflow commands, masking, and
  problem matchers all behave as they do under act_runner because the same code runs
  them. Images on the instance registry are pulled with the job token before act starts
  so private `container:` and service images work, and every job gets `DOCKER_CONFIG`
  logged in to the instance registry with the job token plus `DISTROFACE_REGISTRY`. Jobs
  never get the docker socket or `--privileged` unless the runner was registered with
  `--privileged-jobs`. `GITEA_ACTIONS=true` and `DISTROFACE_ACTIONS=true` are set so
  actions that branch on the host forge pick the Gitea shaped api.
- The log hook (`report.go`) turns act's entries into step rows and transcript lines the
  way act_runner's reporter does: one row per step and phase (`Pre` and `Post` rows only
  for actions that really have the phase), `Run <first line>` for unnamed run steps,
  `##[group]` markers, annotations from `::error::` and friends with secrets masked in the
  message, the job conclusion from act's `jobResult`, and a final flush before the task
  completes.
- Interactive agent sessions reuse the job container: the injected final step waits for a
  done file while the runner executes commands through docker exec with the step
  environment, and the container, volumes, and network are removed at the end, bounded
  waits on every path.
- Concurrency per runner, graceful shutdown finishes running jobs, a second signal cancels
  them as `cancelled`, logs stream in near real time, cancellation from the UI stops the
  running step and runs the post phases, and on start the runner removes containers,
  networks, and job directories a crashed run of the same runner name left behind
  (container names are normalized the way act normalizes them).

## 22. Settings and config

Boot config (`pkg/config/config.go`): `git.storage_path` only. Everything else is
`GitSettings` and `MailSettings` in the resolver. Org overrides: `git.lfs_enabled`,
`git.private_by_default`, `git.default_branch`, `git.max_repo_size_mb`,
`git.actions.enabled`, `mail.*`. Repo overrides: `mail.*`. Added to `scopeAllowed` in
`settings/merge.go`.

```proto
message GitSettings {
  optional bool enabled = 1;
  optional bool ssh_enabled = 2;
  optional int32 ssh_port = 3;                  // system only, default 22, listener rebinds live
  optional string ssh_public_host = 4;          // host or host:port in clone urls
  optional bool lfs_enabled = 5;
  optional int64 max_lfs_object_mb = 6;
  optional int64 max_repo_size_mb = 7;          // zero unlimited, an org may only lower it
  optional bool private_by_default = 8;
  optional string default_branch = 9;
  optional bool release_on_tag = 10;
  optional int64 max_release_asset_mb = 17; // Per file cap on release assets, zero unlimited
  optional int32 max_concurrent_transfers = 11; // system only
  optional bool allow_forks = 12;
  GitGCSettings gc = 13;                        // enabled, interval_hours, system only
  GitSigningSettings signing = 14;              // enabled, instance key signs web commits and merges
  GitActionsSettings actions = 15;              // enabled, artifact_retention_days, log_retention_days, run_on_mirror, default_runs_on
}
```

Org rows may tighten `lfs_enabled` and `max_repo_size_mb` but never loosen them, the
update refuses a looser value and the resolver clamps any stored one back to the instance
limit. Members of a scope read its effective settings with the mail subtree reduced to
`enabled`, the relay host and account are for scope admins.
`defaults.go` seeds `enabled=true, ssh_enabled=false, ssh_port=22, lfs_enabled=true,
max_lfs_object_mb=2048, max_repo_size_mb=0, max_release_asset_mb=2048, default_branch=main, release_on_tag=false,
max_concurrent_transfers=32, allow_forks=true, gc.enabled=true, gc.interval_hours=168,
signing.enabled=false, actions.enabled=false, actions.artifact_retention_days=90,
actions.log_retention_days=90`, and `mail.enabled=false, smtp_port=587, security=STARTTLS,
allow_private_networks=true`. `publicSubset` in `services/settings.go` exposes
`git.enabled`, `ssh_enabled`, `ssh_port`, `ssh_public_host` so the UI builds clone URLs
before login and knows whether `/` lists repositories or images.

Startup in `container.go` after the mirror monitor:

```go
gitEngine, err := gitrepo.NewEngine(cfg.Git.StoragePath, cfg.Storage.DataDir)
mailService := mailer.NewService(store, resolver, log)
gitManager := gitrepo.NewManager(store, gitEngine, blobStore, resolver, dispatcher, auditRecorder, log)
forgeManager := forge.NewManager(store, gitManager, mailService, dispatcher, auditRecorder, resolver, log)
actionsManager := actions.NewManager(store, gitManager, forgeManager, artifactManager, authManager, resolver, log)
gitHTTP := gitrepo.NewHTTP(gitManager, authManager, enforcer, portalResolver, tokenService, authLimiter, pullLimiter, anonPullLimiter, auditRecorder, log)
gitSSH := gitrepo.NewSSHManager(gitManager, store, enforcer, tokenService, resolver, portalResolver, cfg.Server.Host, authLimiter, auditRecorder, log)
resolver.Subscribe(gitSSH.Reconcile)
portalService.OnChange(gitSSH.Reconcile)
mirrorMonitor.SetGit(gitManager)
gitManager.Hooks().Subscribe(forgeManager.OnRefUpdate, actionsManager.OnRefUpdate)
mailService.Start(ctx)
```

`ServerDeps` gains `GitHTTP`, `GitManager`, `ForgeManager`, `ActionsManager`,
`MailService`. `App` gains `GitSSH` for shutdown and the scheduler stops.

## 23. Webhooks and audit

`Dispatcher` gains `DispatchGit(ctx, event, repo *db.GitRepository, payload any)` beside
`Dispatch`, and `GetActiveWebhooksForGitRepo(repoID, orgName)` matches repo scope rows
with `git_repo_id` and org scope rows. Payloads are JSON with `event`, `action`,
`timestamp`, `repository {namespace, name, full_name, clone_url, ssh_url, html_url,
private, default_branch}`, `sender {username, display_name}`, then one of `push {ref,
before, after, forced, commits[], pusher, compare_url}`, `branch` or `tag {ref, sha}`,
`issue {number, title, body, state, labels, assignees, milestone, url}` with `comment`
when present, `pull_request {number, title, base, head, draft, merged, url}` with `review`
or `comment`, `release {tag, name, draft, prerelease, assets[], images[], url}`, `wiki
{page, action}`, `fork {forkee}`, `repository` for created, deleted, renamed, transferred,
archived, or `workflow_run {name, status, conclusion, url}`. `RenderTemplate` and
`ValidateTemplate` accept the new payload struct, the sample payload in validation gains a
git case. The webhook form lists git events for repo and org scopes, `WebhookService`
accepts `scope=GIT_REPOSITORY` with `git_repo_id` and checks the repo admin level.

Audit rows: rpc mutations through the interceptor for free (`Git/CreateGitRepository`,
`Issue/CreateIssue`, and so on), transport events `Git/push`, `Git/fetch`, `Git/auth`
denied, `Git/lfs-upload`, `SSHKey/add`, `SSHKey/delete`, `GPGKey/add`, `Token/create`
detail gains the scope, `Team/*` through the interceptor, `Mail/send` failures,
`Actions/run` with the run id, `Git/merge` with the method and PR.

## 24. Portals

HTTP git honors the portal the way `/v2/` does, as a third case in `portal/middleware.go`
that runs for every path not already claimed by the OCI and artifact cases:

```go
// Opaque repo name ahead of a smart http, lfs, or browse suffix
var gitRoutePattern = regexp.MustCompile(`^/(.+?)(\.wiki)?(\.git)?/(info/refs|git-upload-pack|git-receive-pack|git-upload-archive|HEAD|objects/.+|info/lfs/.*|-/.*)$`)
```

The first group is the opaque name exactly as `apiRoutePattern` captures it for OCI, one
segment on a `map_unqualified` portal, two on the primary host. `MapName`, then `InScope`,
then rewrite to the canonical `/<ns>/<name>[.wiki].git/<suffix>` so the handler sees two
segment paths only. Differences from the OCI case: `allowMethod` does not run on git paths
because `POST git-upload-pack` is a read, the handler checks `AllowPush` at the push intents
listed in 6.2 and answers 403 there, and foreign repos on isolated portals get a plain 404.
`AnonymousAllowed` and `ForeignRef` apply as for images. `http_clone_url` in responses uses
the portal host and the mapped name when the call arrived on a portal, `ssh_clone_url` is
the canonical path on the portal hostname, plus the unqualified form on the portal SSH port
when one is set (7.2). `ListGitRepositories` scopes to the org through
`portal.ScopeNamespace`. The SPA on a portal host resolves `/app` through the same
`ScopeRepoRef` mapping the artifact pages use.

## 25. Rename, transfer, forks, templates, deletion

Rename and transfer. `MoveGitRepository(new_namespace, new_name)` needs owner level on the
source plus `CanCreateInNamespace(new_namespace)`. The store moves `namespace`, `name`,
`owner_id`, `is_org_namespace` in one transaction, writes a `git_redirects` row for the old
address, moves the bare directory and the wiki with `os.Rename` while the writer mutex is
held, renames the casbin object through the new `Enforcer.RenameObject(resource, old,
new)` (the `UPDATE casbin_rule SET v3` the artifact backfill already runs), and moves name
linked packages: image repos through `RegistryAccess.MoveRepository`, an `os.Rename` of
`docker/registry/v2/repositories/<ns>/<name>` which holds only link files into the global
blob tree, refused while `_uploads` has entries, and artifact repos by row update. The
packages move first and any failure puts back what already moved, so a move either
completes or changes nothing. Team repository rows of the previous org are dropped, the
settings resolver forgets the repo's org, and removing an org member also drops their
team seats.
`RepositoryService.MoveRepository` and `ArtifactService.MoveArtifactRepository` expose the
same primitive for standalone repos, which closes the existing gap where no kind could be
renamed. Old HTTP clone URLs answer 301, old SSH paths resolve transparently, and the UI
redirects, until the old address is reused by a new repo. Transfers to another user create
a `git_transfers` row the recipient accepts from their notifications within seven days,
transfers into an org where the caller may create repos apply immediately.

Forks. `ForkGitRepository` creates the row with `fork_of_id`, inits the bare repo, and
fetches every ref from the parent with the file transport in process. Forks are independent
object stores, no alternates, so deleting a parent never breaks a fork. `SyncFork` fast
forwards the fork's default branch from the parent, or opens a PR when it cannot or when
that branch is protected, so a fork's own rules are never bypassed. Private
repos fork only within the namespaces the user can create in. `ListForks` shows the
network. `allow_forks` on the system tier and the repo setting gate it.

Templates. `IsTemplate` repos offer "Use this template", which creates a repo with one
commit containing the template's default branch tree (14.4), optionally with
`git_labels` copied, recording `template_of_id`.

Archive. Archived repos are read only for pushes, issues, PRs, wiki, and settings except
unarchive and delete. Listings show the badge.

Deletion. `DeleteGitRepository` removes rows by cascade, the bare and wiki directories,
LFS links, release asset files, and run artifacts with blob reaping, linked packages when
`delete_linked`, redirects pointing at it,
and audits. Rows no cascade reaches go in the same transaction: tokens scoped to the
repo, deploy keys, repo scoped actions secrets, variables, runners and runner tokens, and
the settings row, and the casbin grants on the object are removed after. `DeleteOrganization` gains the git loop beside its image loop and drops the
org's teams. `DeleteUser` deletes the git repos in the user's namespace, which fixes the
orphan behavior images have today in passing (noted, not changed for images here).

## 26. UI (`web/distroface/src`)

### 26.1 Route moves

The project takes the root, so the image repo page moves. `routes/[namespace]/[name]/
+page.svelte` (the image page) becomes `routes/images/[namespace]/[...name]/+page.svelte`,
which also gives nested image names a page for the first time. `routes/+page.svelte` (the
image explore) becomes `routes/images/+page.svelte`. The new `routes/+page.svelte` renders
the repositories explore when `configStore.gitEnabled` and the images explore otherwise.
`repo-card.svelte` and every `resolve('/[namespace]/[name]', ...)` call point at
`/images/[namespace]/[...name]`. The new `routes/[namespace]/[name]/+page.svelte` is the
project page: it calls `GetGitRepository`, and on NotFound it tries `GetRepository` and
redirects to `/images/ns/name`, then `GetArtifactRepository` and redirects to
`/artifacts/ns/name`, so every old bookmark keeps working. `routes/[username]/+page.svelte`
becomes `routes/[namespace]/+page.svelte` and redirects to `/orgs/[name]` when the
namespace is an org. The SvelteKit router prefers static segments, so `/images`,
`/artifacts`, `/orgs`, `/admin`, `/settings`, `/docs`, `/login`, and `/notifications` keep
winning over `[namespace]`, and the reserved namespace list in 3 keeps new accounts from
shadowing them.

### 26.2 Navigation and shared state

`navLinks` in `routes/+layout.svelte` becomes Repositories (GitBranch, `/`), Images
(Package, `/images`), Artifacts (Archive, `/artifacts`), Organizations, Admin, API, with
Repositories hidden when git is disabled, plus a `notification-bell` when signed in. The
org layout gains Repositories beside Images and Artifacts, Teams beside Members, and under
`canAdmin` Labels, Projects, Runners, Secrets, and Email. The user settings layout gains
Keys and Notifications. The admin layout gains Git, Mail, and Runners. `rpc-client.ts`
gains `git`, `gitRefs`, `gitFiles`, `release`, `issue`, `pull`, `wiki`, `project`,
`notification`, `key`, `mail`, `actions` clients. `configStore` gains `gitEnabled`,
`sshPublicHost`, `sshPort`. `vite.config.ts` adds a root proxy entry whose `bypass`
forwards only paths matching the data plane suffix set from 6.1, so SPA pages at the root
keep rendering in dev. `portalStore` gains `sshPort` so the clone box builds portal SSH
URLs.

### 26.3 Routes

| Route | Content |
| --- | --- |
| `/`, `/git/new` | repositories explore, `QueryFilterBar`, kind and topic filters, create page with type select (standard, mirror, import, from template), `MirrorConfigFields kind="git"` for mirrors and plain source plus what to import fields for imports |
| `/[ns]/[name]` | code tab: header with badges and topics, description, clone box (HTTP and SSH with copy, port aware), branch and tag picker, latest commit bar, tree table, README, sidebar with latest release, packages, languages, contributors |
| `.../tree/[ref]/[...path]`, `.../blob/[ref]/[...path]`, `.../blame/[ref]/[...path]` | browser, viewer with shiki, line anchors, LFS notice, blame runs, edit and delete buttons for writers |
| `.../edit/[ref]/[...path]`, `.../new/[ref]/[...path]`, `.../upload/[ref]/[...path]` | CodeMirror editor, commit form with "commit to new branch and open a PR" |
| `.../commits/[ref]`, `.../commit/[sha]` | commit list with author filter and path, commit page with the paged file tree and diff pane, statuses, verification badge, revert and cherry pick |
| `.../branches`, `.../tags`, `.../compare/[range]` | branch table with ahead and behind and protection, tag table, compare with base repository picker on forks, paged commits, paged file tree and diff pane, "create pull request" |
| `.../issues`, `.../issues/new`, `.../issues/[number]`, `.../labels`, `.../milestones` | lists with filters and bulk bar, template picker, issue page with timeline, reactions, sidebar for labels, assignees, milestone, project, due date, dependencies, time tracking |
| `.../pulls`, `.../pulls/new`, `.../pulls/[number]` | PR list, compare form with base and head repository pickers, paged commits and file tree, PR page with conversation, paged commits, and files tabs, file tree beside a single file diff pane with inline review comments, review submit panel, merge box with method select and protection status, checks panel, sidebar shared with issues (lock, delete for admins, no pins), maintainer edits toggle for fork authors |
| `.../releases`, `.../releases/new`, `.../releases/[tag]` | release list, editor with tag picker, asset upload through the artifact data plane, image attach picker, generate notes, release page with source, assets, and images each linking home |
| `.../wiki`, `.../wiki/[page]`, `.../wiki/[page]/edit`, `.../wiki/[page]/history` | wiki pages, sidebar and footer, editor |
| `.../projects`, `.../projects/[id]` | kanban board with drag and drop between columns |
| `.../actions`, `.../actions/runs/[id]`, `.../actions/runs/[id]/jobs/[jobId]` | workflow list, runs with status, job log viewer streaming, artifacts, rerun and cancel |
| `.../packages`, `.../packages/link` | linked image and artifact repos linking to their pages, link page and unlink for admins |
| `.../activity`, `.../forks` | activity graph and contributor list, fork network |
| `.../settings` | general (description, website, topics, visibility, default branch, units and external urls, merge methods), branches (protections), tags, collaborators, deploy keys, deploy tokens, webhooks (`WebhookManager scope=GIT_REPOSITORY`), LFS, mirror, push mirrors, email, actions (runners plus repository overrides for default runs-on, the mirror trigger, and artifact and log retention, inherited from the organization or instance and capped by the instance), secrets (secrets and variables), danger zone (archive, transfer, rename, delete with linked) |
| `/images`, `/images/[ns]/[...name]` | moved image pages, plus a Source repository line and Release badges on tags |
| `/artifacts/[ns]/[repo]` | gains a Source repository line and Release badges on versions |
| `/orgs/[name]/repos`, `/orgs/[name]/teams`, `/orgs/[name]/teams/[team]`, `/orgs/[name]/labels`, `/orgs/[name]/projects`, `/orgs/[name]/runners` | org pages reusing the repo components, team page with members, repositories, unit grid |
| `/settings/keys`, `/settings/notifications`, `/notifications` | SSH and GPG keys, web and email preferences, inbox |
| `/forgot-password`, `/reset-password/[token]`, `/verify-email/[token]` | password reset and verification |
| `/admin/git`, `/admin/mail`, `/admin/runners` | settings cards in the `FormCard` and `FormField` pattern of `admin/mirroring`, an instance card with the ssh host key fingerprints, the signing public key, the storage path and usage from `GetGitServerInfo`, mail deliveries table with retry, runner list |

Shared components added: `clone-box`, `ref-picker`, `tree-table`, `file-view`,
`markdown` (marked plus dompurify, autolinks, task lists, relative link rewriting, fenced
code through shiki, mermaid fences render as plain code per project rules), `diff-viewer`
(paged `file-tree` beside a `diff-file` pane, unified and split with soft wrap, chunked
rows, inline comment anchors), `paged-commit-list`, `fork-contribute-bar`,
`base-repo-picker`, `commit-list`,
`commit-badge`, `verification-badge`, `label-chip`, `label-picker`, `assignee-picker`,
`milestone-picker`, `reaction-bar`, `timeline`, `review-thread`, `merge-box`,
`checks-panel`, `release-list`, `asset-uploader`, `image-picker`, `kanban-board`,
`job-log`, `key-manager`, `protection-form`, `collaborator-table`, `team-permission-grid`,
`notification-bell`, `source-link`. `MirrorConfigFields` gains `kind="git"` copy and the
ssh key panel. `mirror-sync.svelte.ts` accepts kinds `git`, `git_push`, `import`.
`cli-setup-prompt.svelte` adds the `git config credential.helper` line. Permission gating
follows the existing idiom: pages read `viewer_units` from `GetGitRepository` and gate
buttons with `PermissionGate allowed={...}`.

## 27. dfcli (`pkg/api`)

Git itself is the client for clone, push, and fetch. dfcli adds:

- `dfcli git credential` a git credential helper (`get`, `store`, `erase` on stdin) that
  answers the stored session or `df_` token for the configured server, so `git config
  --global credential.helper "dfcli git credential"` makes HTTP pushes passwordless.
- `dfcli git clone [ns/]name [dir]` resolves the repo like artifacts do and execs the local
  `git` with the right URL, `--ssh` picks the SSH URL.
- `dfcli repo create|list|view|delete|fork|transfer|archive|topics` with `--namespace`,
  `--private`, `--mirror <url>`, `--template`, JSON output through `printProtoJSON`.
- `dfcli release create|list|view|upload|download|attach-image|delete` where `upload`
  streams each file to the release asset endpoint (`--label`, `--replace`) and `download`
  fetches the assets of a release into a directory (`--asset <glob>`, `-o <dir>`).
- `dfcli issue list|create|view|close|reopen|comment|label|assign` and `dfcli pr
  list|create|view|checkout|merge|review|close` with `--table` and JSON, `checkout` runs
  `git fetch origin pull/<n>/head` and creates the branch.
- `dfcli org team list|view|create|add|remove|add-repo|remove-repo`.
- `dfcli key add|list|rm` for SSH keys, `dfcli gpg add|list|rm`.
- `dfcli runner register|start|status` from 21.
- `dfcli token create --scope git:ns/name --read-only` for deploy tokens.

`client.go` gains `Git()`, `GitRefs()`, `GitFiles()`, `Releases()`, `Issues()`, `Pulls()`,
`Keys()`, `Teams()`, `Actions()`, `Runner()` accessors in the existing shape.

## 28. Build, image, dev, verification

- `go.mod`: dependencies in 2.3. `go mod tidy` after the first import.
- `docker/Dockerfile.distroface`: `EXPOSE 8080 22`. No `apk add git`, the server never
  execs git. The runner image for dfcli is a separate small Dockerfile in `docker/`: it
  runs as root because it drives the host docker socket, reads `DFCLI_RUNNER_CONFIG` from
  the `/home/runner` volume so registration survives recreation, and keeps job trees under
  `DFCLI_RUNNER_WORK_DIR=/var/lib/dfcli-runner`, which must be bind mounted from the same
  host path because sibling job containers mount it through the host daemon.
- `docker-compose.yaml` keeps host networking for dev, and `config.yaml` seeds
  `git.ssh_port: 2222` for that reason only. The README documents `-p 22:22` for
  production and `-p 2222:22` with `ssh_public_host: host:2222` when 22 is taken.
- `Makefile`: `proto` targets unchanged, buf generates Go, TS, and OpenAPI for the new
  files automatically, `/docs/api` picks them up through `import.meta.glob`.
- `.claude/skills/verify/SKILL.md` gains a git section: the dev machine's `git` and
  `git-lfs` binaries are test clients only. Scenarios: clone with and without `.git`,
  protocol v2 negotiated (`GIT_TRACE_PACKET`), shallow clone, push over 1 MiB (gzip and
  chunked bodies), private repo 401 then 404, anonymous clone when allowed, read only
  portal clone and push refusal, `map_unqualified` portal clone over HTTP and over the
  portal SSH port, SSH clone and push with a generated key, deploy key read only refusal,
  protected branch rejection message shown by the client, LFS push and pull with `git lfs
  install`, wiki clone, web edit then pull, PR create and merge through the RPC then pull
  shows the merge commit, mirror from a scratch http server, release asset upload over the
  streaming endpoint and download through `releases/download/<tag>/<file>`, release image
  listing after a docker push, team permission grid
  denying a push, password reset mail captured by a local SMTP sink, runner registering
  and executing a workflow with a `run` step.
- `scripts/seeder`: phases for git repos (pushed through the HTTP transport with go-git
  as the client in the seeder), teams, issues, pulls, labels, milestones, releases, stars.

## 29. Phased checklist

Each phase ships on its own and leaves the app consistent. Nothing below is optional.

### Phase 1: core transport, browsing, addresses
- [ ] `git.proto` (GitService, GitRefService, GitFileService), `types.proto` enums,
      `GitSettings`, `portal.proto` ssh_port, buf generate
- [ ] `GitRepository`, `GitRedirect`, `GitTopic`, `GitStar`, `GitWatch` models,
      `stores/git.go` with `GitReposQuery`, link migration
- [ ] `utils.ReservedNamespaces` enforced at every self service account and org creation site
- [ ] `rbac/resources.go`, seeds, `mapping.go` entries, `role.go` scopeable case,
      `portalAllowsProcedure`
- [ ] `internal/gitrepo`: engine, loader, locks, access (owner, public, grants), reads,
      diff, http wrapped around the frontend handler (upload-pack, receive-pack,
      upload-archive, raw, archive), hooks with the ref update fan out (counters, webhooks,
      audit, unborn HEAD), maintenance scheduler
- [ ] `services/git.go`, `gitrefs.go`, `gitfiles.go`, `server.go` wrap, portal middleware
      git case, `container.go` wiring, config key, Dockerfile expose
- [ ] Linked packages: `git_repo_id` columns, name and label linking, access union,
      Packages tab, `ListLinkedRepositories`, link and unlink, source lines on image and
      artifact pages
- [ ] Stars, watch rows, topics, archive flag, units and external urls
- [ ] Transfer cap, pull limiters, `Git/push` and `Git/fetch` audit
- [ ] UI: route moves for images and the profile page, home switch, nav, explore list,
      create panel, project page with tree, blob, blame, commits, commit, branches, tags,
      compare, raw, archive, clone box, markdown and shiki, settings general and danger
      zone, org repos tab, admin git settings, vite proxy
- [ ] dfcli `git credential`, `git clone`, `repo` commands
- [ ] Verify skill scenarios for clone, push, protocol v2, portals

### Phase 2: identity, SSH, keys, LFS
- [ ] `keys.proto`, `SSHKey`, `GPGKey` models and store, `KeyService`, `/settings/keys`
- [ ] `ssh.go` and `sshmanager.go`: primary and portal listeners, host keys, exec parsing,
      env, exit status, reconcile on settings and portal writes, deploy keys, banner
- [ ] `CredentialScope` on API tokens, `auth.ScopeAllows`, wired into all three access
      helpers and the registry token endpoint, deploy tokens panel
- [ ] `signing.go` GPG and SSH verification, instance signing key, verified badges
- [ ] `lfs.go` batch, basic transfer, verify, locks, `git-lfs-authenticate`,
      `git-lfs-transfer`, `VerifyAccess` with the git audience, shared blob reaping
- [ ] Verify skill scenarios for SSH on primary and portal ports, deploy keys, LFS

### Phase 3: permissions and protection
- [ ] `OrgTeam`, members, repos, default team migration, team rpcs on
      `OrganizationService`, `Access.Unit` with teams and collaborators, `can_create_repos`
- [ ] `GitCollaborator` with invitations, collaborator rpcs and table
- [ ] `GitBranchProtection`, `GitTagProtection`, pre-receive rules with client messages,
      protection forms, `GitCommitStatus` and the status rpcs
- [ ] UI teams pages, permission grid, collaborators, protections, `viewer_units` gating

### Phase 4: editing, releases, moves, forks
- [ ] `commit.go` primitive, `UpdateFiles`, revert, cherry pick, web editor routes,
      `auto_init`, templates and "use this template"
- [ ] `release.proto`, `GitRelease`, `GitReleaseImage`, assets in the artifact package,
      image association and pinning, release pages with home links, badges on image and
      artifact pages, `release_on_tag`, generate notes, dfcli `release`
- [ ] `MoveGitRepository`, `MoveRepository`, `MoveArtifactRepository`, `Enforcer.
      RenameObject`, `RegistryAccess.MoveRepository`, redirects, pending transfers
- [ ] Forks, sync fork, fork network, deletion cascades for org and user

### Phase 5: mail
- [ ] `mail.proto`, `MailSettings`, REPO settings scope in resolver, merge, service, and UI
      helpers, admin, org, and repo email cards, test email
- [ ] `internal/mailer` queue, worker, templates, deliveries log and retry rpc
- [ ] Password reset, email verification, invite emails, UI routes
- [ ] Incoming mail poller and reply by email tokens

### Phase 6: issues, projects, notifications
- [ ] `issue.proto`, `project.proto`, models for issues, labels, milestones, assignees,
      comments, reactions, dependencies, tracked time, projects
- [ ] `internal/forge` managers, timeline events, cross references, auto close from
      commits, templates and YAML forms, bulk edit, search
- [ ] `notification.proto`, notifications and watching, preferences, bell, inbox page,
      email delivery through the mailer
- [ ] Webhooks `GIT_ISSUE`, `GIT_ISSUE_COMMENT`, org labels, org projects
- [ ] UI issue and project routes and components, dfcli `issue`

### Phase 7: pull requests and reviews
- [ ] `pull.proto`, `GitPullRequest`, reviews, review comments, review requests for users
      and teams
- [ ] `merge.go` three way engine with diff3 and conflict report, merge methods, revert
      and cherry pick switched onto it
- [ ] PR lifecycle, `refs/pull/<n>/head`, cross fork fetch, mergeability cache, update
      branch, auto merge, protection checks, CODEOWNERS with teams, suggestions, outdated
      detection
- [ ] Webhooks `GIT_PULL_REQUEST`, `GIT_PULL_REQUEST_REVIEW`, `GIT_PULL_REQUEST_COMMENT`,
      post receive hint, PR notifications and mails
- [ ] UI pull routes, diff view with inline comments, merge box, checks panel, dfcli `pr`

### Phase 8: wiki
- [ ] `wiki.proto`, wiki repo lifecycle, pages, history, sidebar and footer, clone over
      both transports, wiki webhooks, UI routes

### Phase 9: mirrors and imports
- [ ] `mirror/git.go` pull mirrors with the shared http client, ssh mirror keys, validate,
      monitor kind `git`, refuse pushes to mirrors, composite create with releases and
      packages
- [ ] Push mirrors, `git_push_mirrors`, sync on push, kind `git_push`
- [ ] `forgeDriver` importers for github, gitlab, gitea, resumable `git_imports`, kind
      `import`, UI import flow and progress
- [ ] `MirrorConfigFields kind="git"`, admin mirroring page shows the new kinds

### Phase 10: actions
- [x] `actions.proto`, models, Gitea's jobparser and workflowpattern for parsing and
      planning, triggers, scheduler, concurrency groups, run and job state machine with the
      ported job emitter, reusable workflows, logs, secrets, variables, runners and tokens,
      statuses into protection
- [x] GitHub Actions results api (upload-artifact and download-artifact v4), the legacy
      pipeline artifact api, run owned artifact rows, artifact cache
- [x] `RunnerService`, dfcli `runner` executing jobs with act the way act_runner does,
      interactive agent sessions on the job container
- [x] Starter workflows from `actions/starter-workflows`, no catalog in this repository
- [x] UI actions routes, log streaming, admin and org runner pages, webhook
      `GIT_WORKFLOW_RUN`
- [ ] Verify skill scenario with a registered runner and a `run` step workflow

### Phase 11: polish
- [x] Insights cache: contributors, weekly activity, and languages are memoized per
      `repo@head` in `GitService` (`gitrepo.Memo`, one hour ttl, concurrent misses share
      one walk, a push simply misses on the new head). The repo home requests them with
      `backgroundCallOptions` so they never drive the progress bar.
- [ ] Language bar, activity graph, contributors, seeder phases, docs page check,
      `README.md` feature list and port documentation, `config.example.yaml` comments,
      release notes for the route move, the SSH port, and the new dependencies

## 30. Decisions needed from you

These are the places where I chose a default the doc now depends on. Say so if any should
flip, otherwise the defaults stand.

1. Image pages move from `/ns/name` to `/images/ns/name`, with redirects from the old
   address, so the project can own the root like Forgejo. This is the one UI change outside
   git. The alternative is keeping images at the root and giving git a prefix, which you
   rejected.
2. The default team starts with write on every unit and `can_create_repos`, so org members
   create and push by default as you asked. Gitea starts a new org with an Owners team
   only. Both are reachable by editing the default team.
3. Reserved namespaces are enforced for new self service accounts and orgs only. Existing
   accounts and bootstrap names such as `admin` are left alone, no migration renames
   anything.
4. Release assets are rows of their release and workflow artifacts are rows of their run,
   both with bytes in the shared blob store. Neither is a package. A standalone artifact
   repo named after a git repo is still adopted as a linked package at git repo creation,
   it just holds package files.
5. Repository tier settings exist only for `mail.*`. If you want other paths at the repo
   tier, name them and I will add them to `scopeAllowed`.
6. Artifact repositories and image repositories do not get stars or collaborators in this
   plan, git does. Both are the same shape and one small phase each if wanted.
7. No avatars for git repos because nothing in the app has avatars today.
8. The beta.1 protocol gaps in 2.2 (no partial clone filter, no atomic push, no thin
   pack) are accepted as is. Each one disappears with a go-git upgrade, none needs code
   from us.
9. Mirror upstreams accept https and ssh. The `git://` scheme is refused.
10. User and org renames stay out. The `Move*` primitives and redirects make them a
    follow on.

## 31. Sources

- go-git v6.0.0-beta.1 module source read from the module cache, in particular
  `plumbing/transport/serve.go`, `upload_pack.go`, `receive_pack.go`, `upload_archive.go`,
  `loader.go`, `backend/backend.go`, `backend/http.go`, `backend/writer.go`,
  `storage/filesystem/storage.go`, `repository.go`, `options.go`, `blame.go`,
  `worktree.go` (Grep), `plumbing/object/{patch,difftree,merge_base,rename}.go`,
  `plumbing/client/client.go`, `internal/archive/archive.go`, `COMPATIBILITY.md`,
  `_examples/http-server/main.go`
- go-git releases: https://github.com/go-git/go-git/releases/tag/v6.0.0-beta.1
- go-git examples: https://github.com/go-git/go-git/tree/v6.0.0-beta.1/_examples
- go-billy v6.0.0-beta.1 `osfs` BoundOS: https://github.com/go-git/go-billy
- Git smart HTTP protocol: https://git-scm.com/docs/http-protocol
- Git protocol v2: https://git-scm.com/docs/protocol-v2
- Git pack protocol and capabilities: https://git-scm.com/docs/pack-protocol
- git-upload-archive: https://git-scm.com/docs/git-upload-archive
- Git LFS batch API, basic transfer, locking API, ssh authenticate, pure ssh transfer:
  https://github.com/git-lfs/git-lfs/tree/main/docs/api and
  https://github.com/git-lfs/git-lfs/blob/main/docs/proposals/ssh_adapter.md
- SSH signature format: https://github.com/openssh/openssh-portable/blob/master/PROTOCOL.sshsig
- Gitea reference: https://docs.gitea.com/ (teams and units, permissions, branch
  protection, actions, mirrors, migrations, commit status API, CODEOWNERS, reserved
  usernames, SSH_PORT and SSH_LISTEN_PORT)
- Forgejo reference: https://forgejo.org/docs/latest/user/
- GitHub packages linking rules: https://docs.github.com/en/packages/learn-github-packages/connecting-a-repository-to-a-package
- GitHub Actions workflow syntax: https://docs.github.com/en/actions/writing-workflows/workflow-syntax-for-github-actions
- diff3 algorithm: Khanna, Kunal, Pierce, "A Formal Investigation of Diff3",
  https://www.cis.upenn.edu/~bcpierce/papers/diff3-short.pdf
- go-mail: https://github.com/wneessen/go-mail
- go-imap v2 and go-message: https://github.com/emersion/go-imap and https://github.com/emersion/go-message
- Existing in repo patterns: `internal/registry/*`, `internal/artifacts/*`,
  `internal/auth/token_handler.go`, `internal/portal/{middleware,proxy,resolver}.go`,
  `internal/mirror/*`, `internal/rbac/*`, `internal/rpc/services/artifact.go`,
  `internal/settings/*`, `internal/db/migrations/202607220001_rename_invalid_oidc_usernames.go`

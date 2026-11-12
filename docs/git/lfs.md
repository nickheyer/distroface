# Git LFS

The server implements the git-lfs batch api with the basic transfer adapter, the verify endpoint, file locks, and the pure ssh transfer protocol. LFS objects are content addressed blobs shared across repositories.

## Enabling

| Setting | Default | Description |
| --- | --- | --- |
| `git.lfs_enabled` | `true` | Instance switch. An org tier may turn it off, never on while the instance has it off |
| `git.max_lfs_object_mb` | `2048` | Largest single object accepted, zero is unlimited |

Each repository also has its own switch, seeded from the effective setting at creation and changed under Repo > Settings > LFS or with `GitService.UpdateGitRepository` `lfs_enabled`. When either switch is off every LFS request answers 404 `lfs is disabled for this repository`.

```bash
git lfs install
git lfs track "*.psd"
git add .gitattributes
git push
```

## Batch api

The endpoint is `POST /namespace/name.git/info/lfs/objects/batch` with `application/vnd.git-lfs+json`. Authentication is the same as for [cloning](cloning.md), a `download` batch needs read on the code unit and an `upload` batch needs write.

| Rule | Result |
| --- | --- |
| `transfer` | Always `basic` |
| `hash_algo` | `sha256` only, anything else answers 409 |
| Download of an unknown object | Per object error 404 |
| Upload above `git.max_lfs_object_mb` | Per object error 422 |
| Upload of an object already stored for a repository you can read | Linked to this repository without a transfer |
| Upload of a new object | `upload` action at `.../info/lfs/objects/<oid>` and `verify` at `.../info/lfs/verify`, both valid 3600 seconds, your `Authorization` header echoed |

Object transfer uses `PUT` and `GET` on `/namespace/name.git/info/lfs/objects/<oid>`. An upload is hashed while it streams, a body whose sha256 differs from the oid answers 400, one above the cap answers 413. A `GET` honors `Range`, so an interrupted download resumes where it stopped. `POST .../info/lfs/verify` checks that the oid exists with the given size.

## Transfer over ssh

`git-lfs-authenticate namespace/name.git download|upload` prints the json git-lfs expects: `href` pointing at `<http clone url>/info/lfs`, a `Bearer` token in `header`, and `expires_in` 3600. The token is signed for that one repository with `pull`, plus `push` for `upload`, and is only accepted on `info/lfs` paths.

Clients that speak the pure ssh protocol run `git-lfs-transfer namespace/name.git download|upload` instead and never touch http. Version 1 is supported with `batch`, `get-object`, `put-object`, `verify-object`, `lock`, `list-lock`, `unlock`, and `quit`. Size caps and content checks match the http path.

## Locks

| Endpoint | Behavior |
| --- | --- |
| `POST .../info/lfs/locks` | Creates a lock on `path`, 409 with the existing lock when one exists |
| `GET .../info/lfs/locks` | Lists locks, filters `path` and `id`, pages with `limit` and `cursor` |
| `POST .../info/lfs/locks/verify` | Splits locks into `ours` and `theirs` |
| `POST .../info/lfs/locks/<id>/unlock` | Releases a lock, `force` lets a repository admin release another user's lock |

Creating and releasing locks needs write on the code unit. The ssh transfer protocol offers the same four operations.

## Where objects live

Objects are stored once per sha256 in the shared blob store under `<artifacts.storage_path>/blobs/sha256`, the same store release assets use, not under `git.storage_path`. A link row ties each oid to every repository that references it and records its size. Deleting a repository drops its links and removes blobs nothing references anymore. Repositories that reference the same oid share one copy of the bytes.

## Usage

Repo > Settings > LFS shows the repository's LFS bytes beside its git object bytes, the values are `lfs_size_bytes` and `size_bytes` on `GitRepository`. Admin > Git shows the instance total under the Instance card, returned as `lfs_bytes` by `SettingsService.GetGitServerInfo`.

When the web reads a file that is an LFS pointer, `GitFileService.GetBlob` sets `is_lfs` and fills `lfs` with the oid, size, whether the bytes are present on this server, and a download url that serves the object instead of the pointer. See [Code](code.md).

# Artifacts and cache

Jobs upload artifacts, restore and save caches, push images and stream logs through the server. The endpoints speak the protocols the GitHub toolkit actions expect, so `actions/upload-artifact`, `actions/download-artifact`, `actions/cache` and the `setup-*` actions work unchanged.

## Authentication inside a job

| Token | Set by the runner as | Accepted by |
| --- | --- | --- |
| Runtime token, a JWT bound to the job and run | `ACTIONS_RUNTIME_TOKEN` | The results API and the pipeline API |
| Job token, see [Actions overview](overview.md#github_token) | `GITHUB_TOKEN` secret | The cache API, the GitHub API and the registry |

Both stop working once the job is no longer `running`. The runtime token expires one hour after the job's timeout.

## Artifacts

An artifact is a named zip stored once per run. Names may not contain `\ / " : < > | * ?` or line breaks, and a name cannot be uploaded twice in one run unless the first upload is deleted. Every artifact also has a number that GitHub style clients use as its id.

### upload-artifact and download-artifact v4

Version 4 of the toolkit talks to the results API at `/twirp/github.actions.results.api.v1.ArtifactService/<Method>` with `ACTIONS_RESULTS_URL` pointing at the instance. Requests are JSON bodies authenticated by the runtime token, and `workflow_run_backend_id` must be the job's own run.

| Method | Purpose |
| --- | --- |
| `POST .../CreateArtifact` | Claim the name, answer a signed upload link |
| `PUT .../UploadArtifact?comp=block`, `appendBlock` or `blocklist` | Receive blocks and the block list on the signed link |
| `POST .../FinalizeArtifact` | Merge the blocks, check the announced size and sha256, confirm the artifact |
| `POST .../ListArtifacts` | Confirmed, unexpired artifacts of the run, filtered by name or id |
| `POST .../GetSignedArtifactURL` | Signed download link for one name |
| `GET .../DownloadArtifact` | Stream the zip on the signed link |
| `POST .../DeleteArtifact` | Remove an artifact by name |

Signed links are valid for one hour and are bound to the job that asked for them.

### upload-artifact v1 to v3

Older toolkit versions use the pipeline API under `/api/actions_pipeline/_apis/pipelines/workflows/{run_id}/artifacts`, reached through `ACTIONS_RUNTIME_URL`, with the same runtime token. `{run_id}` is the run's id or number.

| Route | Purpose |
| --- | --- |
| `POST .../artifacts` | Create a container for a name, `retentionDays` optional |
| `PUT .../artifacts/{hash}/upload?itemPath=<name>/<file>` | Upload one file in `Content-Range` chunks, gzip and `x-actions-results-md5` honoured |
| `PATCH .../artifacts?artifactName=<name>` | Finalize, folding the staged files into the zip |
| `GET .../artifacts` | List artifacts of the run |
| `GET .../artifacts/{hash}/path`, `.../download_url` | List the files inside an artifact |
| `GET .../artifacts/{hash}/download?itemPath=<name>/<file>` | Stream one file |
| `GET .../artifacts/{id}/zip` | The whole zip by number or id |

`{hash}` is the md5 of the artifact name, the way the toolkit computes it. Uploading a file that already exists in the artifact replaces that entry.

### Downloading artifacts

Repo > Actions shows the artifacts of a run with their size and expiry. `ActionsService.ListRunArtifacts` returns a session authenticated link per artifact under the repository page. The GitHub API route `/repos/{owner}/{repo}/actions/artifacts/{id}/zip` redirects to a signed link under `/api/actions/artifacts/{id}/{name}.zip?exp=...&sig=...` that is valid for one hour and needs no credentials. Missing artifacts answer 404, expired ones 410.

### Retention

An artifact expires after `git.actions.artifact_retention_days`, 90 by default, or sooner when the upload asked for a shorter `retention-days`. Zero keeps artifacts forever unless the upload sets a limit. A sweep two minutes after start and every hour after that deletes expired artifacts, drops uploads that never finalized within 24 hours, and removes blobs nothing references any more. Deleting a repository removes its artifacts, logs and caches.

## Cache

`actions/cache` and the `setup-*` actions use the artifactcache protocol under `/api/actions/cache/_apis/artifactcache`, reached through `ACTIONS_CACHE_URL` and authenticated with the job token as a bearer.

| Route | Purpose |
| --- | --- |
| `GET /cache?keys=<k1>,<k2>&version=<v>` | Look up an entry. Exact key matches across every key come first, then prefix matches. Entries saved on the run's own ref are preferred, then the default branch. 204 on a miss. |
| `POST /caches` | Reserve `key` and `version`, refused when the pair already exists |
| `PATCH /caches/{id}` | Upload the archive in `Content-Range` chunks |
| `POST /caches/{id}` | Commit with the final `size` |
| `GET /artifacts/{id}?job=...&exp=...&sig=...` | Download on the signed link the lookup answered |

Keys are at most 512 characters and hold no commas. An archive may be up to 10 GB and a repository keeps up to 10 GB of caches, the least recently used entries are evicted past that. Reservations that never commit expire after one hour, entries unused for 7 days are pruned, and fork pull request runs may restore but not save. Caches are never shared between repositories.

## Images

Pushes to the instance registry made with the job token are attributed to the job and listed on the run page and by `ActionsService.ListRunImages` with tag, digest, size, platforms and a pull command. When the run was triggered by a tag that has a release, the images are pinned to that release, see [Releases](../git/releases.md). Registry tokens minted from a job token are refused after the job finishes.

## Logs

The runner uploads log chunks of up to 1 MiB with `RunnerService.AppendTaskLog`, a job log holds at most 64 MiB. Secrets and tokens are masked before upload.

`ActionsService.StreamJobLog` streams the chunks of a job from `from_index` on and keeps polling every second until the job completes, then sends a final chunk with `done`. When a job finishes its chunks are compacted into one blob, a stream that started late receives the compacted log in one chunk with `replace` set. The run page under Repo > Actions shows the log live while the job runs.

Logs are kept for `git.actions.log_retention_days`, 90 by default, zero keeps them forever. The hourly sweep drops the logs of jobs older than that, 500 jobs per repository per pass. Rerunning a run drops the logs of the jobs it resets.

# Runners

A runner is a process that claims queued jobs from the server and executes them through act in docker containers or directly on its host. `dfcli runner` is the standalone runner, and the server can run one inside its own process.

## Registering a runner

Create a registration token for a scope under Admin > Actions > Runners, Org > Actions > Runners or Repo > Settings > Actions > Runners, or with `ActionsService.CreateRunnerToken`. The token starts with `dfr_`, is valid for one hour, works once, and carries the scope: instance runners serve every repository, organization runners the organization's repositories, repository runners one repository.

```bash
dfcli runner register --server https://df.example.com --token dfr_... --name build-1 --labels ubuntu-latest,linux
dfcli runner start
```

Registration exchanges the token for a long lived runner token starting with `dfrt_`, sent in the `X-Runner-Token` header of every call, and writes `runner.json`. `--server` is the global dfcli flag, see [CLI reference](../cli/reference.md).

| Flag | Description |
| --- | --- |
| `--token` | Registration token, required. The scope comes from the token. |
| `--name` | Runner name, default the hostname. |
| `--labels` | Comma separated labels the runner serves, default `ubuntu-latest`. |
| `--runner-config` | Config path, default `DFCLI_RUNNER_CONFIG` or `~/.dfcli/runner.json`. |
| `--concurrency` | Jobs to run at once, default 1. |
| `--host` | Enable the host executor for labels mapped to `host`. |
| `--work-dir` | Parent of job work trees. |
| `--privileged-jobs` | Hand jobs the docker socket and let them run `--privileged`. |
| `--docker-host` | Daemon address, `unix://`, `tcp://` or `ssh://`, default the environment. |
| `--container-options` | Extra `docker run` flags for every job container, shell quoted. |
| `--valid-volumes` | Comma separated host paths and named volumes jobs may mount, glob syntax. |
| `--container-network` | Docker network job containers join, default a private network per job. |
| `--insecure-skip-tls` | Skip certificate checks when cloning actions and workflows. |

`dfcli runner start --runner-config <path>` polls until interrupted. The first `SIGINT` or `SIGTERM` stops polling and lets running jobs finish, a second one cancels them. `dfcli runner status` prints the config and checks the docker daemon, `--check` also sends a heartbeat, which marks the runner online.

## runner.json

| Field | Description |
| --- | --- |
| `server` | Instance URL. |
| `token` | Runner token. |
| `runner_id`, `name`, `scope` | Identity the server assigned. `scope` is `system`, `org:<name>` or `repo:<namespace/name>`. |
| `labels` | Labels at registration. Matching uses the labels stored on the server, change them under the runner's scope page or with `ActionsService.UpdateRunner`. |
| `images` | Label to image map. The value `host` selects the host executor. |
| `concurrency` | Jobs at once. |
| `host_enabled` | Whether `host` mappings are honoured. |
| `privileged_jobs` | Docker socket and `--privileged` for jobs. |
| `work_dir` | Parent of job directories, falls back to `DFCLI_RUNNER_WORK_DIR`, then `$TMPDIR/dfcli-runner`. |
| `docker_host` | Daemon address, exported as `DOCKER_HOST` at start. |
| `container_network` | Network job and service containers join. |
| `container_options` | Extra `docker run` flags. |
| `valid_volumes` | Mount allow list for container options and workflow volumes. |
| `insecure_skip_tls` | Skip certificate checks for clones from the instance. |

### Labels and images

Registration maps every label to `ghcr.io/catthehacker/ubuntu:act-latest`, the image act, Gitea and Forgejo default to, with node, git, the docker cli, curl and build tools, and keeps `host` mapped to the host executor. Edit `images` to change that, for example `docker.io/library/ubuntu:24.04` for a slim image. A job's first `runs-on` label with a mapping decides the image. Unmapped labels fall back to the `ubuntu-latest` image. A job `container:` overrides the label image.

Labels mapped to `host` run the job on the runner machine itself, without a container, and only when `host_enabled` is set. Jobs that build images need docker inside the job, register with `--privileged-jobs` to hand them the socket.

## Job execution

A job gets its own directory `job-<runner>-<id>` under the work dir with the checkout, a docker config, and session files. Remote actions are cloned once into `_actions` under the work dir and shared by every job. Before act starts, the runner pulls the job image and every service image, using the workflow's `credentials` first, the job token as user `x-access-token` for images on the instance registry, and the runner process's docker login for everything else. The `DOCKER_USERNAME` and `DOCKER_PASSWORD` secrets apply to the job image when set.

Every step sees `ACTIONS_CACHE_URL`, `ACTIONS_RUNTIME_URL`, `ACTIONS_RESULTS_URL`, `ACTIONS_RUNTIME_TOKEN`, `DISTROFACE_ACTIONS=true`, `DISTROFACE_ACTIONS_RUNNER_VERSION`, `DISTROFACE_REGISTRY` with the instance host, and `DOCKER_CONFIG` pointing at a config that already logs `docker` into the instance registry with the job token. `GITHUB_SERVER_URL` is the instance and `GITHUB_API_URL` is `<instance>/api/v1`.

The runner reports step states and outputs every 3 seconds and learns about cancellation from the same call, streams log chunks, and reports the conclusion when act finishes. Jobs run at most `timeout-minutes`, 360 when unset. Secrets and tokens are masked in the log. After a job its directory and containers are removed, and at start the runner removes anything a crashed run left behind.

## Running the runner in docker

`docker/Dockerfile.runner` builds the `distroface-runner` image with `dfcli runner` as entrypoint and `start` as the default command. Jobs run as sibling containers through the host daemon, so the work dir must sit at the same path inside the container and on the host, and the registration lives on a volume.

```bash
docker build -f docker/Dockerfile.runner -t distroface-runner .
docker run --rm -v dfcli-runner-home:/home/runner \
  distroface-runner register --server https://df.example.com --token dfr_... --name ci
docker run -d --name dfcli-runner \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /var/lib/dfcli-runner:/var/lib/dfcli-runner \
  -v dfcli-runner-home:/home/runner \
  distroface-runner
```

The image sets `DFCLI_RUNNER_CONFIG=/home/runner/runner.json` and `DFCLI_RUNNER_WORK_DIR=/var/lib/dfcli-runner`. Act keeps the tool cache in the `act-toolcache` volume mounted at `/opt/hostedtoolcache`.

## The built in runner

The server can run jobs itself. Turn it on under Admin > Actions. A runner named `builtin` at the instance scope then serves the configured labels and restarts whenever these settings change, draining running jobs first. It cannot be removed with `ActionsService.DeleteRunner`, disable it in the settings instead.

| Setting | Default | Description |
| --- | --- | --- |
| `git.actions.builtin_runner.enabled` | `false` | Run the embedded runner. |
| `git.actions.builtin_runner.labels` | `ubuntu-latest` | Comma separated labels it serves. |
| `git.actions.builtin_runner.concurrency` | `2` | Jobs at once. |
| `git.actions.builtin_runner.image` | empty | Image behind every label, the stock act image when empty. |

Jobs are sibling containers on the server's docker socket, and the daemon mounts job directories by host path. The process therefore needs the socket, and the data dir must be bound at the same path on the host and inside the container, the work dir is `<data dir>/runner`. The socket makes the server root on its host, keep it to development and small instances and use `dfcli runner` on a separate machine for anything shared. The compose file in the repository shows both bindings, see [Installation](../getting-started/installation.md).

## Runner status

A runner heartbeats every 30 seconds with the ids of the jobs it is running. It shows as `online` when the last heartbeat is under 2 minutes old, `busy` while it runs jobs, and `offline` otherwise. A running job the heartbeat stops listing fails after 2 minutes with `runner stopped executing the job`, and the server fails every running job of a runner silent for 10 minutes. Deleting a runner fails what it was executing.

## Job matching

A runner long polls `RunnerService.FetchTask` for up to 30 seconds, dfcli asks for 20. The server hands it the oldest queued job in its scope whose `runs-on` labels all appear on the runner, case insensitive, skipping jobs whose concurrency group has a running member in the repository and interactive agent jobs when the runner lacks the `exec` capability. Jobs with no matching registered runner never queue, they fail at planning, see [Workflows](workflows.md).

## RPCs

`RunnerService` is spoken by runners at `/distroface.v1.RunnerService/<Method>`, authenticated by the `X-Runner-Token` header.

| Method | Purpose |
| --- | --- |
| `RegisterRunner` | Exchange a `dfr_` token for a runner and its `dfrt_` token, with name, labels, version and capabilities |
| `Heartbeat` | Mark the runner online and report running job ids |
| `FetchTask` | Long poll for a job, answers the workflow payload, github context, secrets, variables, needs, tokens, clone url and timeout |
| `UpdateTask` | Report step states, outputs and annotations, learns whether the job was cancelled |
| `AppendTaskLog` | Append a log chunk at an index |
| `CompleteTask` | Report the conclusion |
| `FetchExec`, `CompleteExec` | Commands of interactive agent jobs, see [Sessions](../agents/sessions.md) |

Management goes through `ActionsService`: `ListRunners` (with `include_inherited` a repository also sees the organization and instance runners serving it), `CreateRunnerToken`, `UpdateRunner` for name and labels, and `DeleteRunner`.

```bash
curl -X POST https://df.example.com/distroface.v1.ActionsService/CreateRunnerToken \
  -H 'Authorization: Bearer <token>' -H 'Content-Type: application/json' \
  -d '{"scope":{"kind":"repo","id":"myteam/app"}}'
```

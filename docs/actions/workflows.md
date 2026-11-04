# Workflows

A workflow is a YAML file in the repository that names the events it listens to and the jobs to run. The syntax is the GitHub Actions syntax, read with the act workflow model.

## Workflow files

The server reads the first of these directories that holds `.yml` or `.yaml` files, in this order:

1. `.distroface/workflows`
2. `.gitea/workflows`
3. `.github/workflows`

Files in the other directories are ignored. A file may be at most 1 MiB. The workflow name is `name:` when set and the file name without extension otherwise. A file needs at least one job, and every job needs `runs-on` and `steps` unless it calls a reusable workflow with `uses:`. Files that fail to parse are listed with their error under Repo > Actions and never run.

`push` and `pull_request` events read the files at the pushed or head commit. `pull_request_target` reads them at the base branch. Every other event and the schedules read the default branch.

## Triggers

`on:` takes an event name, a list of names, or a mapping of names to filters.

| Event | Filters | Activity types |
| --- | --- | --- |
| `push` | `branches`, `branches-ignore`, `tags`, `tags-ignore`, `paths`, `paths-ignore` | |
| `pull_request` | `types`, `branches`, `branches-ignore`, `paths`, `paths-ignore` | `opened`, `synchronize`, `reopened`, `closed`, `edited`, `labeled`, `assigned`, `review_requested`, `ready_for_review`, `converted_to_draft`. Without `types`: `opened`, `synchronize`, `reopened` |
| `pull_request_target` | as `pull_request` | as `pull_request`, raised beside every `pull_request` event and run on the base branch |
| `issues` | `types` | `opened`, `edited`, `closed`, `reopened`, `labeled`, `assigned` |
| `issue_comment` | `types` | `created`, `edited`, `deleted` |
| `pull_request_review` | `types` | `submitted` |
| `pull_request_review_comment` | `types` | `created` |
| `release` | `types` | `created`, `edited`, `deleted`, `published` |
| `schedule` | list of `cron` entries | |
| `workflow_dispatch` | `inputs` | |
| `workflow_call` | `inputs`, `secrets`, `outputs` | Only through another workflow's `uses:` |

Events without `types` match every activity. Branch, tag and path filters use the GitHub glob syntax, `!` negates. Path filters diff the pushed range, or the pull request head against its merge base with the base branch. When no diff can be taken, for a new ref or more than 5000 changed paths, the workflow runs. Tags never apply path filters. A `push` trigger with only tag filters ignores branches, one with only branch filters ignores tags.

Mirror repositories trigger on fetch only when `git.actions.run_on_mirror` is on. A head commit whose message contains `[skip ci]`, `[ci skip]`, `[no ci]`, `[skip actions]` or `[actions skip]` triggers no `push` or `pull_request` run.

### Schedules

Cron entries use five fields in UTC with ranges, steps, lists, month and weekday names, and the aliases `@yearly`, `@annually`, `@monthly`, `@weekly`, `@daily`, `@midnight` and `@hourly`. The scheduler ticks every minute, reads the default branch of every non archived repository with Actions enabled, and starts one run per workflow whose entry matches. `github.event.schedule` holds the matching cron string.

### Dispatching a workflow

`workflow_dispatch` runs start from Repo > Actions, pick the workflow, Run workflow, or through `ActionsService.DispatchWorkflow`. `ref` names a branch or tag and defaults to the default branch. Inputs are checked against the declarations: unknown names are refused, `required` inputs without a `default` must be given, `boolean` takes `true` or `false`, `number` must parse, `choice` must be one of `options`. `github.event.inputs` keeps strings, the `inputs` context carries typed values.

```bash
curl -X POST https://df.example.com/distroface.v1.ActionsService/DispatchWorkflow \
  -H 'Authorization: Bearer <token>' -H 'Content-Type: application/json' \
  -d '{"namespace":"myteam","name":"app","path":".distroface/workflows/release.yml","ref":"main","inputs":{"dry_run":"true"}}'
```

### Enabling and disabling

`ActionsService.DisableWorkflow` stops a file from running for every event, schedule and dispatch until `ActionsService.EnableWorkflow`. The switch is keyed by path and survives commits. Repo > Actions shows the state per workflow.

## Event payload and github context

`github.event` carries the payload. Every payload has `repository` with `name`, `full_name`, `owner.login`, `default_branch`, `private`, `clone_url` and `html_url`, and `sender.login`. Forge events add `action`.

| Event | Payload |
| --- | --- |
| `push` | `ref`, `before`, `after`, `created`, `deleted`, `forced`, `head_commit`, `commits` (at most 20, each with `id`, `message`, `url`, `timestamp`, `author`, `committer`), `pusher.name` |
| `pull_request`, `pull_request_target` | `pull_request` as the webhooks send it, see [Webhooks](../webhooks.md) |
| `issues` | `issue` as the webhooks send it |
| `issue_comment` | `issue` or `pull_request`, and `comment` with `id`, `body`, `author_id`, `created_at` |
| `release` | `release` with `tag_name`, `name`, `body`, `draft`, `prerelease`, `html_url` |
| `schedule` | `schedule` |
| `workflow_dispatch` | `inputs`, `ref`, `workflow` |

The `github` context has `actor`, `triggering_actor`, `event_name`, `event`, `ref`, `ref_name`, `ref_type`, `sha`, `base_ref`, `head_ref`, `repository`, `repository_owner`, `repository_id`, `run_id`, `run_number`, `run_attempt`, `job`, `workflow`, `workflow_ref`, `workflow_sha`, `server_url`, `api_url` (`<server>/api/v1`), `retention_days` and `token`. `graphql_url` is empty. For `pull_request` runs `ref` is `refs/pull/<n>/head`. `run_id` and `repository_id` are integer forms of the row ids, the GitHub API accepts both.

## Expressions and contexts

The server evaluates expressions while planning with the `github`, `inputs`, `vars`, `needs`, `strategy` and `matrix` contexts. `env`, `secrets`, `steps`, `job` and `runner` are evaluated by act on the runner. Server side evaluation covers `run-name`, job `name`, `runs-on`, `strategy.matrix`, `fail-fast`, `max-parallel`, `continue-on-error`, `timeout-minutes`, `concurrency` and the job level `if`. An empty job `if` means every needed job succeeded.

A job whose `name`, `runs-on`, `timeout-minutes`, `continue-on-error` or matrix reads `needs.` is planned once its needs finished. A matrix expands to at most 256 legs. Legs are named `<job> (<values>)` unless the name interpolates `matrix`.

## Jobs

- `runs-on` takes one label or a list. Empty `runs-on` is filled from `git.actions.default_runs_on`. Every label must appear, case insensitive, on a runner registered for the repository, its organization or the instance, else the job fails with `no registered runner matches runs-on`. Which image a label selects is decided by the runner, see [Runners](runners.md).
- `needs` hands `needs.<id>.result` and `needs.<id>.outputs` to the job. `continue-on-error` jobs and `neutral` conclusions count as `success` downstream, `timed_out` counts as `failure`.
- `strategy.fail-fast`, on by default, cancels the other legs of a matrix once one leg fails.
- `concurrency` at workflow level puts the run in a group within the repository, `cancel-in-progress: true` cancels the group's other live runs when the run is created. Job level groups are evaluated at plan time, or at queue time when they read `needs`. Runners never claim a job while another run or job of the same group is running in the repository.
- `permissions` affects the job token only, see [Actions overview](overview.md#github_token).
- `container`, `services` and their `credentials` are executed by act. The runner pulls every image before the job starts.
- `timeout-minutes` defaults to 360.

## Reusable workflows

A job with `uses:` instead of `steps` calls another workflow file.

| Form | Source |
| --- | --- |
| `./.distroface/workflows/build.yml` | The calling repository at the run's commit |
| `owner/repo/.distroface/workflows/build.yml@ref` | A repository on this instance when `owner/repo` exists here and the run's actor may read its code, otherwise the raw file from `git.actions.default_actions_url` |

For `github.com` the raw file comes from `raw.githubusercontent.com`, other hosts are tried at `/{owner}/{repo}/raw/{branch,tag,commit}/{ref}/{path}`. Calls nest at most 4 levels and cycles are refused. `with` is checked against `on.workflow_call.inputs`, `secrets: inherit` passes everything the caller sees, an explicit mapping of `${{ secrets.NAME }}` values is checked against `on.workflow_call.secrets`, and `on.workflow_call.outputs` become the caller's outputs. Called jobs appear as `<caller> / <job>`, see `github.event_name` as `workflow_call`, and read the caller's `with` values in `inputs`.

Step level `uses:` is resolved by act on the runner. References without a host are cloned from `git.actions.default_actions_url`, full URLs are used as written, and `./path` actions come from the checkout. Docker, JavaScript and composite actions run as act runs them.

## Starter workflows

Repo > Actions > New workflow shows cards from the repository named by `git.actions.starter_workflows_url`, shallow cloned into `<data dir>/starter-workflows` once a day. Cards are grouped the way the source groups them (Deployment, Security, Continuous integration, Automation, Pages), and starters that need a registry this instance lacks are hidden. `ActionsService.GetWorkflowTemplate` renders one for a repository, filling `$default-branch`, `$protected-branches`, `$cron-daily`, `$cron-weekly`, `$supported-codeql-languages`, `$codeql-languages-matrix` from the repository's languages, and `$registry-url(container)` with the instance host.

## Example

```yaml
name: Build and publish

on:
  push:
    branches: [main]
    tags: ['v*']
  pull_request:
  workflow_dispatch:
    inputs:
      dry_run:
        type: boolean
        default: false

permissions:
  contents: read

jobs:
  test:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        go: ['1.25', '1.26']
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: ${{ matrix.go }}
      - run: go test -coverprofile=coverage.out ./...
      - uses: actions/upload-artifact@v4
        with:
          name: coverage-${{ matrix.go }}
          path: coverage.out

  image:
    needs: test
    if: ${{ github.ref_type == 'tag' && !inputs.dry_run }}
    runs-on: ubuntu-latest
    permissions:
      contents: read
      packages: write
    steps:
      - uses: actions/checkout@v4
      - run: echo "REGISTRY=${GITHUB_SERVER_URL#*://}" >> "$GITHUB_ENV"
      - run: |
          docker build -t "$REGISTRY/${{ github.repository }}:${{ github.ref_name }}" .
          docker push "$REGISTRY/${{ github.repository }}:${{ github.ref_name }}"
```

The image job needs a runner started with `--privileged-jobs` for docker, see [Runners](runners.md). The job's `DOCKER_CONFIG` already carries the job token for the instance registry, so no login step is needed. The pushed tag shows under the run and is pinned to the release of the tag, see [Releases](../git/releases.md). The repository's own `.distroface/workflows/release.yml` is a longer example of the same pattern.

# Actions

Actions runs GitHub compatible workflows from a repository on registered runners. The server plans runs and jobs, runners execute them through act, the engine behind the Gitea and Forgejo runners, so published actions run as they do on GitHub.

## How a run happens

1. A push, tag, pull request, forge event, schedule or manual dispatch is matched against the workflow files of the repository, see [Workflows](workflows.md).
2. Every matching file becomes a run with a per repository number. The server expands matrices, resolves `needs`, evaluates job level `if`, and queues the jobs whose dependencies finished.
3. A runner whose labels cover the job's `runs-on` claims it, executes it, streams logs and step states back, and reports a conclusion, see [Runners](runners.md).
4. Each job writes a commit status named `<workflow> / <job>` on the run's commit. A finished run fires the `git_workflow_run` webhook and notifies the user who triggered it, see [Webhooks](../webhooks.md) and [Notifications](../git/notifications.md).

Runs and jobs move through `waiting`, `queued`, `running` and `completed`. Conclusions are `success`, `failure`, `cancelled`, `skipped`, `timed_out` and `neutral`. A run whose jobs all skipped concludes `skipped`.

Agent command sessions open one job per session on the same machinery, see [Sessions](../agents/sessions.md).

## Enabling Actions

Workflows are off until an admin turns on `git.actions.enabled` under Admin > Actions. Git hosting must be on as well. An organization may set `git.actions.enabled` for its repositories, and every repository has an Actions unit that can be switched off in its settings. A workflow runs only when the effective setting is on and the unit is enabled.

| Setting | Default | Description |
| --- | --- | --- |
| `git.actions.enabled` | `false` | Allow workflows to run. Instance and organization scope. |
| `git.actions.artifact_retention_days` | `90` | Days an uploaded artifact is kept. Zero keeps forever. Organizations and repositories may only shorten the instance value. |
| `git.actions.log_retention_days` | `90` | Days job logs are kept. Zero keeps forever. Organizations and repositories may only shorten the instance value. |
| `git.actions.run_on_mirror` | `false` | Let mirror fetches trigger push and schedule runs on mirror repositories. |
| `git.actions.default_runs_on` | `ubuntu-latest` | Comma separated labels given to jobs that name no `runs-on`. |
| `git.actions.default_actions_url` | `https://github.com` | Host that `uses:` references without a host resolve against. Instance scope only. |
| `git.actions.starter_workflows_url` | `https://github.com/actions/starter-workflows` | Repository the starter workflow cards come from. Instance scope only. |
| `git.actions.builtin_runner.*` | | The runner inside the server process, see [Runners](runners.md). Instance scope only. |

Organization values are set in the organization's settings, repository values under Repo > Settings > Actions. See [Settings](../administration/settings.md) for how the tiers merge.

## Scopes

Secrets, variables and runners belong to one of three scopes. A repository sees its own entries, those of its organization, and those of the instance.

| Scope | `kind` | `id` | Managed by |
| --- | --- | --- | --- |
| Instance | `system` | empty | Users with the git manage permission, under Admin > Actions |
| Organization | `org` | Organization name or id | Organization owners and admins, under Org > Actions |
| Repository | `repo` | `namespace/name` | Repository admins, under Repo > Settings > Actions |

## Permissions

Access is decided per repository on its Actions unit, see [Users and roles](../administration/users-and-roles.md).

| Level on the Actions unit | Allows |
| --- | --- |
| Read | List workflows and runs, read jobs, stream logs, list artifacts and images, browse starter workflows |
| Write | Dispatch, cancel, rerun and approve runs, enable and disable workflows |
| Admin | Manage repository scoped secrets, variables and runners |

Anonymous callers never mutate. Archived repositories refuse every mutation. Read only portals refuse mutations too, see [Portals](../administration/portals.md).

## Watching, cancelling, rerunning and approving runs

Repo > Actions lists the workflow files of the default branch with their run counts and the runs of the repository, filterable by workflow, status, conclusion, event, ref and actor. A run page shows its jobs, steps, live log, artifacts and the images it pushed.

- Cancel finishes queued jobs as `cancelled` at once and asks the runner to stop running ones. The runner notices on its next progress report, within a few seconds.
- Rerun queues a completed run again under the same number with a higher attempt. With `failed_only` the failed, cancelled and timed out jobs and everything depending on them return to the queue, other jobs keep their result. Without it the run is planned again from its stored workflow source and the old logs are dropped. Agent session runs cannot be rerun.
- Approve releases a run that waits because its pull request came from a fork whose author lacks write access on the Actions unit. Such runs start in `waiting`, get no secrets and a read only token.

A reaper runs every minute. Running jobs whose runner has not reported for 10 minutes fail, jobs past their `timeout-minutes` (360 when unset) time out, and queued jobs no runner claimed within 24 hours fail.

## RPCs

`ActionsService` is a ConnectRPC service, every method is a `POST` to `/distroface.v1.ActionsService/<Method>` with a JSON body, see [API overview](../api/overview.md). Repository methods take `namespace` and `name`, scope methods take a `scope` object with `kind` and `id`.

| Method | Purpose |
| --- | --- |
| `ListWorkflows`, `GetWorkflow` | Workflow files of the default branch with parse errors, triggers, dispatch inputs and run counts |
| `EnableWorkflow`, `DisableWorkflow` | Switch one file on or off |
| `DispatchWorkflow` | Start a `workflow_dispatch` run |
| `ListWorkflowTemplates`, `GetWorkflowTemplate` | Starter cards, one rendered for the repository |
| `ListWorkflowRuns`, `GetWorkflowRun`, `GetJob` | Runs newest first, one run with its jobs, one job with its steps |
| `CancelRun`, `RerunRun`, `ApproveRun` | Run controls |
| `StreamJobLog` | Server stream of log chunks until the job finishes |
| `ListRunArtifacts`, `ListRunImages` | Uploads and registry pushes of a run |
| `ListSecrets`, `SetSecret`, `DeleteSecret` | Secrets of a scope |
| `ListVariables`, `SetVariable`, `DeleteVariable` | Variables of a scope |
| `ListRunners`, `CreateRunnerToken`, `UpdateRunner`, `DeleteRunner` | Runners of a scope |

```bash
curl -X POST https://df.example.com/distroface.v1.ActionsService/ListWorkflowRuns \
  -H 'Authorization: Bearer <token>' -H 'Content-Type: application/json' \
  -d '{"namespace":"myteam","name":"app","status":"ACTIONS_STATUS_RUNNING"}'
```

`dfcli` manages runners with `dfcli runner`, see [CLI reference](../cli/reference.md). It has no commands for runs.

## GITHUB_TOKEN

Every job receives an API token in the secrets `GITHUB_TOKEN` and `DISTROFACE_TOKEN` and in `github.token`. It is a scoped token owned by the user who triggered the run, or by the repository owner for scheduled runs, limited to the git repository `namespace/name`, valid for one day and revoked when the job completes. Registry tokens minted from it stop working the moment the job finishes.

The token is read only unless the workflow or job `permissions` block grants `contents: write` or `packages: write`, or reads `write-all`. A job level block replaces the workflow level block. Fork runs always get a read only token.

| Reach | Read only token | Write token |
| --- | --- | --- |
| Git over http for the repository | clone and fetch | push |
| The GitHub flavored API, see [GitHub API](github-api.md) | reads | writes on the repository's units |
| The instance registry, images named after the repository or linked to it, see [Linked repositories](../git/linked-repositories.md) | pull | push, recorded on the run and pinned to the release of the run's tag |
| The actions cache, see [Artifacts and cache](artifacts-and-cache.md) | restore and save | restore and save |

The scope never reaches other repositories and never allows creating, deleting or managing repositories and packages. Secrets are covered in [Secrets and variables](secrets-and-variables.md).

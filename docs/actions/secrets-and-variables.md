# Secrets and variables

Secrets are sealed values handed to jobs by name, variables are plain strings. Both live at the instance, organization or repository scope.

## Scopes and precedence

A job sees the union of the three scopes. When the same name exists at several scopes the repository value wins over the organization value, which wins over the instance value. Names are identifiers, a letter or underscore followed by up to 199 letters, digits or underscores.

| Scope | Where | Who |
| --- | --- | --- |
| Instance | Admin > Actions | Users with the git manage permission |
| Organization | Org > Actions | Organization owners and admins |
| Repository | Repo > Settings > Actions | Repository admins |

Fork pull request runs and agent session jobs receive no secrets at all. Variables reach them.

## Managing secrets

A secret value is encrypted with AES-GCM under a key derived from `auth.sealing_key` before it is stored. The server never returns a value, `ListSecrets` answers names and update times only, and setting an existing name replaces it.

| Method | Body |
| --- | --- |
| `ActionsService.ListSecrets` | `scope`, `page` |
| `ActionsService.SetSecret` | `scope`, `secret_name`, `value` (required) |
| `ActionsService.DeleteSecret` | `scope`, `secret_name` |

```bash
curl -X POST https://df.example.com/distroface.v1.ActionsService/SetSecret \
  -H 'Authorization: Bearer <token>' -H 'Content-Type: application/json' \
  -d '{"scope":{"kind":"org","id":"myteam"},"secret_name":"NPM_TOKEN","value":"npm_..."}'
```

`scope.kind` is `system`, `org` or `repo`, `scope.id` is empty, the organization name, or `namespace/name`.

## Managing variables

| Method | Body |
| --- | --- |
| `ActionsService.ListVariables` | `scope`, `page` |
| `ActionsService.SetVariable` | `scope`, `variable_name`, `value` |
| `ActionsService.DeleteVariable` | `scope`, `variable_name` |

```bash
curl -X POST https://df.example.com/distroface.v1.ActionsService/SetVariable \
  -H 'Authorization: Bearer <token>' -H 'Content-Type: application/json' \
  -d '{"scope":{"kind":"repo","id":"myteam/app"},"variable_name":"GO_VERSION","value":"1.26"}'
```

Variables are visible in listings and are not masked in logs.

## Using them in a workflow

```yaml
jobs:
  publish:
    runs-on: ${{ vars.RUNNER_LABEL }}
    steps:
      - run: npm publish
        env:
          NODE_AUTH_TOKEN: ${{ secrets.NPM_TOKEN }}
          GO_VERSION: ${{ vars.GO_VERSION }}
```

- `secrets.<NAME>` holds the merged secrets of the three scopes plus `GITHUB_TOKEN` and `DISTROFACE_TOKEN`. The runner masks every secret value and the job tokens in the log.
- `vars.<NAME>` holds the merged variables. They are available while the server plans the run, so `runs-on`, matrix values and job level `if` may read them.
- The `secrets` context is evaluated on the runner only. Job level `if` and the other planned fields cannot read it, see [Workflows](workflows.md#expressions-and-contexts).

## Reusable workflows

A caller passes secrets to a called workflow with `secrets: inherit`, which forwards everything the caller sees, or with an explicit mapping whose values are `${{ secrets.NAME }}` and whose keys match the called workflow's `on.workflow_call.secrets`. A called job without a `secrets` block sees none. Each level of nesting narrows the set further, see [Workflows](workflows.md#reusable-workflows).

## GITHUB_TOKEN

`GITHUB_TOKEN` and its alias `DISTROFACE_TOKEN` are minted per job, scoped to the repository, read only unless `permissions` grants `contents: write` or `packages: write`, and revoked when the job completes. Fork runs always get a read only token. What the token reaches is listed in the [Actions overview](overview.md#github_token).

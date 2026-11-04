# Settings

Runtime settings are stored per scope in the database and applied live. The Admin pages, the org and repository settings pages, and `SettingsService` all edit the same rows.

## Tiers

A field resolves from the most specific tier that stores it:

1. Repository
2. Portal
3. Organization
4. System
5. Config file (`overrides:` block, pinned, not editable)
6. Built in default

`SettingsService.GetEffectiveSettings` returns every field resolved for a scope with a `provenance` entry naming the tier each value came from. `SettingsService.GetSettings` returns only the fields stored at one scope plus `locked_paths`, the fields pinned by the config file. See [Configuration](../getting-started/configuration.md) for seeding and pinning.

## Scopes

| Scope | `scope_id` | Who may write | Who may read |
| --- | --- | --- | --- |
| `SETTINGS_SCOPE_TYPE_SYSTEM` | empty | Users with `settings:manage`, the admin role by default | Everyone, anonymous callers get the public subset |
| `SETTINGS_SCOPE_TYPE_ORG` | org id | Org owners and admins, or `organizations:manage` on the org | Org members |
| `SETTINGS_SCOPE_TYPE_PORTAL` | portal id | Owners and admins of the org that owns the portal | Members of that org |
| `SETTINGS_SCOPE_TYPE_REPO` | git repo id | Repository admins and owners | Anyone who can see the repository |

System scope accepts every field. Lower scopes accept only these paths:

| Scope | Paths |
| --- | --- |
| Organization | `acme.email`, `acme.directory_url`, `artifacts.max_file_size_mb`, `artifacts.stale_upload_cleanup_hours`, `artifacts.private_by_default`, `artifacts.retention.*`, `portals.isolated`, `auth.anonymous_access`, `git.lfs_enabled`, `git.private_by_default`, `git.default_branch`, `git.max_repo_size_mb`, `git.max_release_asset_mb`, `git.actions.enabled`, `git.agents.enabled`, `git.agents.allow_repo_providers`, `git.agents.allow_repo_agents`, plus the mail, actions and agents paths below |
| Portal | `acme.email`, `acme.directory_url`, `tls.mtls_mode`, `auth.anonymous_access` |
| Repository | The mail, actions and agents paths below |

Mail paths an org or repository may store: `mail.enabled`, `smtp_host`, `smtp_port`, `security`, `username`, `password`, `from_address`, `from_name`, `reply_to`, `skip_tls_verify`. Throttle, private network and inbound settings stay at system scope.

Actions paths: `git.actions.default_runs_on`, `run_on_mirror`, `artifact_retention_days`, `log_retention_days`.

Agents paths: `git.agents.allow_commands`, `commands_runs_on`, `max_turns`, `max_session_minutes`, `max_sessions_per_day`, `max_concurrent_sessions`, `token_budget_per_session`, `log_retention_days`, `allow_file_agents`, `command_timeout_minutes`.

An org may tighten `git.max_repo_size_mb` below the instance limit but not raise it or set it to unlimited, and may not enable LFS when the instance has it off.

## Updating a scope

`SettingsService.UpdateSettings` takes a `Settings` message and a field mask. Masked paths present in the message are set, masked paths absent from it are cleared so the field inherits again. Unmasked paths are untouched.

```bash
curl -X POST http://localhost:8080/distroface.v1.SettingsService/UpdateSettings \
  -H 'Authorization: Bearer df_...' -H 'Content-Type: application/json' \
  -d '{
    "scope": {"type": "SETTINGS_SCOPE_TYPE_SYSTEM"},
    "settings": {"git": {"sshEnabled": true, "sshPort": 2222}},
    "updateMask": "git.ssh_enabled,git.ssh_port"
  }'
```

The response carries the stored rows and the new effective settings. Secrets such as `auth.oidc.client_secret` and `mail.password` are write only, reads return `client_secret_set` and `password_set` instead.

## Validation

Updates are refused with the fix in the message when the effective result cannot work. Examples: a session timeout under 300 seconds, a token expiry under 60 seconds, an ssh port outside 1 to 65535 or one this host cannot bind, a built in runner concurrency outside 1 to 64, ACME enabled without an account email, ACME as the certificate source while issuance is off or the hostname is `localhost` or an ip, a challenge port equal to the main or ssh port, an ACME directory url pointing at this instance. See [TLS](tls.md) and [ACME](acme.md).

## Groups

| Group | Admin page | Documented in |
| --- | --- | --- |
| `server` | Network security | [TLS](tls.md) |
| `auth` | Authentication | [Users and roles](users-and-roles.md), [OIDC](oidc.md) |
| `tls`, `acme` | Network security | [TLS](tls.md), [ACME](acme.md) |
| `ca` | Certificate authority | [Certificate authority](certificate-authority.md) |
| `portals` | Portals | [Portals](portals.md) |
| `artifacts` | Org > Settings for org overrides, API only at system scope | [Artifact repositories](../artifacts/repositories.md), [Retention](../artifacts/retention.md) |
| `gc` | Storage | [Garbage collection](../registry/garbage-collection.md) |
| `mirror` | Mirroring | [Mirrors and imports](../mirrors.md) |
| `git`, `git.actions`, `git.agents` | Git, Actions, Agents | [Git repositories](../git/repositories.md), [Actions](../actions/overview.md), [Agents](../agents/overview.md) |
| `mail` | Mail | [Mail](mail.md) |
| `rate_limit`, `webhooks`, `security` | API and config only | [Users and roles](users-and-roles.md), [Webhooks](../webhooks.md), [Audit](audit.md) |

Groups without an admin page are set through `SettingsService.UpdateSettings` or the `settings` and `overrides` blocks.

## Git server info

`SettingsService.GetGitServerInfo` returns the ssh host keys with fingerprints, the instance signing key, the git storage path, and repository, repository bytes and LFS bytes counts. `SettingsService.ProbeGitSSHPort` checks that a port binds on this host before the ssh setting is saved. Both back the Admin > Git page.

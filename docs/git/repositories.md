# Repositories

A git repository is a bare repository served in process by go-git and addressed as `namespace/name`, where the namespace is a user or an organization. Images and artifact repositories with the same name link to it, see [Linked repositories](linked-repositories.md).

## Creating a repository

Call `GitService.CreateGitRepository` or use `dfcli repo create [namespace/]name` from the [CLI](../cli/reference.md). The web form is Repo > New.

```bash
curl -X POST https://df.example.com/distroface.v1.GitService/CreateGitRepository \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"namespace":"myteam","name":"app","visibility":"VISIBILITY_PRIVATE","autoInit":true,"gitignoreTemplate":"Go","license":"MIT"}'
```

| Field | Description |
| --- | --- |
| `namespace` | Empty means your own username. You may create in your namespace, in an org where you are owner, admin, or on a team that may create repositories, or anywhere with the `git` `manage` permission |
| `name` | One path segment matching `^[a-z0-9][a-z0-9._-]{0,99}$`, never ending in `.git` or `.wiki` |
| `visibility` | `VISIBILITY_PUBLIC` or `VISIBILITY_PRIVATE`, unset follows `git.private_by_default` |
| `default_branch` | Unset takes `git.default_branch` |
| `object_format` | `sha1` or `sha256`, empty means `sha1` |
| `type` | `GIT_REPO_TYPE_STANDARD`, `GIT_REPO_TYPE_MIRROR`, or `GIT_REPO_TYPE_TEMPLATE`. Forks come from `ForkGitRepository` |
| `auto_init` | Standard repositories only. Writes an `Initial commit` with `README.md` holding the name and description |
| `gitignore_template` | Needs `auto_init`. One of `C`, `Docker`, `Global`, `Go`, `Java`, `Node`, `Python`, `Ruby`, `Rust`, `Svelte` |
| `license` | Needs `auto_init`. One of `MIT`, `Apache-2.0`, `BSD-3-Clause`, `GPL-3.0`, `Unlicense`, your username is the holder |
| `description`, `website`, `topics` | Metadata, the website must be an http or https url |
| `mirror`, `import_source` | Pull mirrors and one time imports, see [Mirrors](../mirrors.md) |

The creator watches the new repository, and in an org namespace every non default team the creator belongs to that may create repositories gains the repository. Same named images and artifact repositories the creator manages are linked at this point.

`dfcli repo create` takes `--namespace`, `-d`, `-p`, `--mirror url`, `--mirror-username`, `--mirror-token`, `--template namespace/name`, `--default-branch`, and `--auto-init`.

## Visibility

Public repositories grant read to every role holding `git` `read`, which includes the anonymous role by default. Private repositories require a relationship described under [Access](#access). Change visibility with `GitService.UpdateGitRepository` or under Repo > Settings > General. New repositories in an org follow that org's `git.private_by_default` when the org tier sets it.

## Repository types

| Type | Description |
| --- | --- |
| Standard | Populated by pushes |
| Mirror | Fetches refs from an upstream url, pushes are refused with `repository is a mirror`, see [Mirrors](../mirrors.md) |
| Fork | Copy of another repository on this instance, `fork_of` names the parent |
| Template | Standard repository offered as a starting point, `template_of` on repositories made from it |

## Default branch

The first push to an empty repository sets `HEAD`. When the pushed branches include the configured default branch it wins, otherwise the first branch pushed becomes the default. Change it under Repo > Settings > General or with `UpdateGitRepository` `default_branch`, the branch must already exist unless the repository is empty. The default branch cannot be deleted. Renaming it is covered in [Branches](branches.md).

## Templates

Mark a standard repository as a template with `UpdateGitRepository` `is_template` or the Template repository toggle under Repo > Settings > General. `GitService.UseTemplate` creates a new repository and writes every file and symlink of the template's default branch into one commit named `Initial commit from template namespace/name`. Templates above 100000 entries are refused. `include_labels` copies the template's labels. Visibility defaults to private when the template is private or the namespace is private by default. `dfcli repo create name --template namespace/name` does the same.

## Forks

`GitService.ForkGitRepository` copies every branch and tag into `target_namespace` (your username when empty) under `target_name` (the parent name when empty). The fork keeps the parent's description, visibility, default branch, and object format. Forking requires `git.allow_forks` on the instance or org tier and the Allow forks merge setting on the parent, otherwise the call fails with `forking is disabled for this repository`.

`GitService.SyncFork` fast forwards the fork's default branch from the parent. When the fork diverged or that branch is protected it opens a pull request from the parent instead and returns `pull_request_number`. `ListForks` pages the forks you can see. `dfcli repo fork namespace/name` takes `--namespace` and `--name`.

## Renaming and transferring

`GitService.MoveGitRepository` takes `new_namespace` and `new_name`, empty fields keep the current value. Owner access is required. When you may create in the target namespace the move happens at once: the directory moves on disk, name linked packages move with it, scoped grants and deploy tokens follow, and the old name keeps a redirect so clones of the old url receive a 301 on `info/refs` once the caller is allowed in.

When the target is another user's namespace the response carries a pending `transfer` instead. That user is notified and has seven days to call `AcceptTransfer` or `RejectTransfer`. `ListTransfers` shows transfers addressed to you. The web form is Repo > Settings > Danger zone > Transfer. `dfcli repo transfer name new-namespace[/new-name]` prints the repository or the pending transfer.

## Archiving

`ArchiveGitRepository` and `UnarchiveGitRepository` need owner access. An archived repository stays readable and cloneable. Pushes answer `repository is archived`, web commits and settings changes are refused, and writes to units other than code are refused. `ListGitRepositories` hides archived repositories unless `include_archived` is set. `dfcli repo archive name` archives, `--undo` restores.

## Topics

`GitService.SetTopics` replaces the list. Topics are lowercased and trimmed, must match `^[a-z0-9][a-z0-9-]{0,34}$`, and at most 25 are kept. Admin access is required. `ListGitRepositories` filters on `topic`. `dfcli repo topics name [topic...]` with no topics clears them.

## Deleting

`GitService.DeleteGitRepository` needs owner access. When images or artifact repositories are still linked the call fails unless `delete_linked` is true, which removes those packages and their stored data too. The repository rows, its wiki, LFS links, bare directory, and scoped grants go with it. `dfcli repo delete name --linked` passes the flag. The web form is Repo > Settings > Danger zone.

## Size quota

| Setting | Default | Description |
| --- | --- | --- |
| `git.max_repo_size_mb` | `0` | Cap on object store bytes, zero is unlimited. An org tier may tighten the instance value, never loosen it |

A push to a repository already over its quota is refused by the pre receive hook with `repository exceeds its N MB quota`. A push may carry at most the remaining quota plus 1 MiB, over http a larger `Content-Length` answers 413. Wikis are exempt. The recorded size is the object store measured after every write and shown under Repo > Settings > General > Storage.

## Access

Access is computed per unit (code, issues, pulls, releases, wiki, projects, packages, actions, agents) at levels none, read, write, admin, and owner. The sources, highest wins:

| Source | Level |
| --- | --- |
| Namespace owner, or any role with `git` `manage` | Owner |
| Org owner or admin | Owner |
| Org team | Per unit levels from the team, admin when the team has admin access |
| Collaborator | `read`, `write`, or `admin`. Admin unlocks settings, unit levels cap at write |
| Scoped rbac grant on `namespace/name` or `namespace` | Level implied by the granted action, `manage` and `delete` give owner |
| Public repository | Read on every enabled unit for roles with `git` `read` |
| Deploy key | Read or write on code, wiki, and packages only |
| Repository scoped token | The holder's level capped at write |

Read allows cloning and browsing. Write allows pushing, branches, tags, and web commits. Admin covers settings, collaborators, deploy keys, protections, webhooks, topics, and package links. Owner covers delete, transfer, and archive. Roles are described in [Users and roles](../administration/users-and-roles.md), orgs and teams in [Organizations](../administration/organizations.md).

`GitService.AddCollaborator` invites a user with `permission` read, write, or admin. The invitation stays pending until the user calls `AcceptCollaboration` from their notifications. `UpdateCollaborator` changes the level, `RemoveCollaborator` removes one, and a collaborator may remove themselves. The owner cannot be added. The page is Repo > Settings > Collaborators.

## Repository settings

Repo > Settings has these pages: General, Branches, Tags, Collaborators, Deploy keys, Deploy tokens, Webhooks, LFS, Mirror, Push mirrors, Email, Actions, Agents, Secrets, and Danger zone. General holds the description, website, topics, template toggle, visibility, default branch, the units shown in the tab bar (with external issue tracker and wiki urls), the merge settings, and a Run maintenance button.

Units default to issues, pulls, releases, wiki, projects, and packages on, with actions and agents following the instance for user namespaces and off for org namespaces. Writes to a disabled unit fail with `the <unit> feature is disabled on this repository`.

## Repository settings tier

Email, Actions, and Agents are a repository tier of the runtime settings resolved as defaults, then system, then the org when the namespace is one, then the repository. A repository may store `mail.*` relay fields, `git.actions.default_runs_on`, `git.actions.run_on_mirror`, `git.actions.artifact_retention_days`, `git.actions.log_retention_days`, and the `git.agents.*` limits. Lower tiers only tighten limits, a looser retention or repository size falls back to the instance value. The org tier may also set `git.lfs_enabled`, `git.private_by_default`, `git.default_branch`, `git.max_repo_size_mb`, `git.max_release_asset_mb`, and `git.actions.enabled`. See [Settings](../administration/settings.md).

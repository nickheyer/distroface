# CLI reference

Every `dfcli` command and flag. Global flags `--server`, `--timeout`, `--config` and `--debug` apply to all of them, see [CLI overview](overview.md). Repository arguments are `[namespace/]name` unless a command says otherwise.

## login

```
dfcli login [-u username] [-p password] [--token df_...]
```

Logs in with a username and password, prompting for whichever is missing, or stores a personal access token. The token must start with `df_` and is verified against the server before it is saved.

## logout

```
dfcli logout
```

Revokes the session on the server when one is stored and deletes the config file. Personal access tokens stay valid.

## version

```
dfcli version
```

## trust

```
dfcli trust show
dfcli trust install [--host host[:port]]
```

`show` prints the instance root CA PEM to stdout. `install` fetches it, prints the SHA-256 fingerprint, stores it at `~/.dfcli/ca.pem`, and on linux writes `/etc/docker/certs.d/<host>/ca.crt`, printing a `sudo` command when it lacks permission. On other systems it saves a copy under the temp dir and tells you where. `--host` overrides the certs.d directory name, default is the server host.

## token

```
dfcli token create --name NAME [--scope git:ns/name | org:name] [--read-only] [--expires-days N]
dfcli token list [--git-repo ns/name] [-t]
dfcli token delete ID
```

| Flag | Description |
| --- | --- |
| `--name` | Required token name |
| `--scope` | `git:namespace/name` limits the token to one repository and its linked packages, `org:name` to one organization. Without it the token carries your full identity |
| `--read-only` | Scoped token may only read, needs `--scope` |
| `--expires-days` | Days until expiry, 0 never expires |
| `--git-repo` | List only tokens scoped to this repository |

The plaintext is printed once by `create`.

## key

```
dfcli key add [file] [--title T] [--usage auth|signing|both] [--signing]
dfcli key list [-t]
dfcli key rm ID
```

`add` reads an ssh public key from the file, or stdin when the file is missing or `-`. The title defaults to the key comment. `--signing` is shorthand for `--usage signing`.

## gpg

```
dfcli gpg add [file]
dfcli gpg list [-t]
dfcli gpg rm ID
```

`add` reads an armored public key from the file or stdin. When the server returns a challenge it is printed on stderr, sign it and verify the key under Settings > Keys.

## image

```
dfcli image list
dfcli image tags namespace/image
```

Lists image repositories visible to you, and the tags of one image. The image must be qualified.

## artifact

Bare repository names resolve on the server, your own namespace first, then the unique visible match. `--namespace` qualifies a bare name on every subcommand.

```
dfcli artifact create REPO [-d description] [-p] [--namespace NS]
dfcli artifact list [--namespace NS]
dfcli artifact upload REPO FILE [-v version] [-p path] [--property k=v,...]
dfcli artifact download REPO [-v version] [-p path] [--property k=v] [-o out] [--num N] [--sort F] [--order ASC|DESC] [--format zip|tar.gz] [--unpack] [--flat]
dfcli artifact delete REPO VERSION PATH
dfcli artifact search [-r REPO] [--name N] [-v V] [-p P] [--property k=v] [--num N] [--offset N] [--sort F] [--order ASC|DESC] [-t]
```

| Flag | Command | Description |
| --- | --- | --- |
| `-p`, `--private` | create | Private repository |
| `-v`, `--version` | upload | Version, defaults to the file name, sanitized to word characters, dash and dot |
| `-p`, `--path` | upload | Path inside the version, defaults to the file name |
| `--property` | upload, download, search | Key value properties, repeatable or comma separated |
| `-o`, `--output` | download | File or directory, default current directory. A directory receives `<repo>_artifacts.<format>` unless `--unpack` |
| `--num` | download, search | Download default 1, search default all |
| `--sort` | download, search | `name`, `version`, `path`, `size`, `created_at`, `updated_at`, search default `created_at` |
| `--format` | download | `zip` default or `tar.gz` |
| `--unpack`, `--flat` | download | Extract the archive, flatten directories |
| `-r`, `--repo` | search | Limit to one repository, otherwise every visible one |
| `--offset` | search | Result offset |

Upload opens an upload session, streams the file with a PATCH, then completes it with version, path and properties. Download fetches an archive from the query endpoint, see [Upload and download](../artifacts/upload-and-download.md).

## repo

Bare names resolve in `--namespace`, or your own username without it.

```
dfcli repo create NAME [--namespace NS] [-d desc] [-p] [--default-branch B] [--auto-init] [--template ns/name] [--mirror URL --mirror-username U --mirror-token T]
dfcli repo list [--namespace NS] [--archived] [-t]
dfcli repo view NAME
dfcli repo delete NAME [--linked]
dfcli repo fork namespace/name [--namespace NS] [--name N]
dfcli repo transfer NAME NEW-NAMESPACE[/NEW-NAME]
dfcli repo archive NAME [--undo]
dfcli repo topics NAME [topic...]
```

| Flag | Command | Description |
| --- | --- | --- |
| `-p`, `--private` | create | Private repository |
| `--auto-init` | create | Initial commit with a readme |
| `--template` | create | Create from a template repository, must be qualified |
| `--mirror` | create | Pull mirror of an upstream git url, with optional username and token |
| `--archived` | list | Include archived repositories |
| `--linked` | delete | Also delete linked image and artifact repositories |
| `--undo` | archive | Unarchive instead |

`topics` replaces the topic list, no topics clears it.

## git

```
dfcli git clone NAME [dir] [--ssh] [--namespace NS]
dfcli git credential get|store|erase
```

`clone` resolves the clone url on the server and runs the local `git clone`. `--ssh` uses the ssh url and fails when ssh is disabled. `credential` is the git credential helper, see [CLI overview](overview.md).

## release

Repositories must be qualified as `namespace/name`.

```
dfcli release create namespace/name TAG [--title T] [--body B] [--target ref] [--draft] [--prerelease]
dfcli release list namespace/name [-t] [--drafts] [--prereleases]
dfcli release view namespace/name TAG
dfcli release upload namespace/name TAG FILE... [--label L] [--replace]
dfcli release download namespace/name TAG [-o dir] [--asset glob]
dfcli release attach-image namespace/name TAG IMAGE:TAG
dfcli release delete namespace/name TAG [--delete-tag]
```

| Flag | Command | Description |
| --- | --- | --- |
| `--target` | create | Branch or sha to tag when the tag does not exist |
| `--prereleases` | list | Include prereleases, default true |
| `--replace` | upload | Replace an asset that already carries the file name |
| `--asset` | download | Only assets whose name matches the glob |
| `--delete-tag` | delete | Also delete the git tag |

`attach-image` takes an image from the same namespace, `app:1.0` or `namespace/app:1.0`.

## issue

```
dfcli issue list namespace/name [--state open|closed] [--label L]... [--assignee U] [--author U] [-t]
dfcli issue create namespace/name --title T [--body B]
dfcli issue view namespace/name NUMBER
dfcli issue close namespace/name NUMBER
dfcli issue reopen namespace/name NUMBER
dfcli issue comment namespace/name NUMBER [body] [--body B] [--pull]
dfcli issue label namespace/name NUMBER LABEL... [--remove]
dfcli issue assign namespace/name NUMBER USERNAME... [--remove]
```

`comment --pull` says the number names a pull request. `label` and `assign` add to the current set, `--remove` drops from it. Unknown label names fail.

## pr

```
dfcli pr list namespace/name [--state open|closed] [--base B] [--head H] [--author U] [-t]
dfcli pr create namespace/name --head H --title T [--base B] [--body B] [--draft]
dfcli pr view namespace/name NUMBER
dfcli pr checkout namespace/name NUMBER [--remote origin]
dfcli pr merge namespace/name NUMBER [--method M] [--title T] [--message M] [--delete-branch]
dfcli pr review namespace/name NUMBER (--approve | --request-changes | --comment) [--body B]
dfcli pr close namespace/name NUMBER [--delete-branch]
```

| Flag | Command | Description |
| --- | --- | --- |
| `--head` | create | Branch, or `namespace/name:branch` for a fork |
| `--base` | create | Base branch, default branch when empty |
| `--remote` | checkout | Remote pointing at the server, default `origin`. The head is fetched into a local `pr-<n>` branch |
| `--method` | merge | `merge`, `squash`, `rebase`, `rebase-merge`, `ff`, default is the repository setting |
| `--body` | review | Required unless approving |

## org team

```
dfcli org team list ORG [-t]
dfcli org team view ORG TEAM
dfcli org team create ORG NAME [-d desc] [--can-create-repos] [--all-repos] [--admin]
dfcli org team add ORG TEAM USERNAME
dfcli org team remove ORG TEAM USERNAME
dfcli org team add-repo ORG TEAM REPO
dfcli org team remove-repo ORG TEAM REPO
```

Teams are matched by name or id. `view` prints the team with its members and repositories.

## runner

```
dfcli runner register --token dfr_... [--name N] [--labels a,b] [--concurrency N] [--host] [--work-dir D] [--privileged-jobs] [--docker-host URL] [--container-options "..."] [--valid-volumes a,b] [--container-network NET] [--insecure-skip-tls] [--runner-config FILE]
dfcli runner start [--runner-config FILE]
dfcli runner status [--runner-config FILE] [--check]
```

| Flag | Description |
| --- | --- |
| `--token` | One time registration token, it carries the scope |
| `--name` | Runner name, default hostname |
| `--labels` | Comma separated labels, default `ubuntu-latest` |
| `--concurrency` | Jobs at once, default 1 |
| `--host` | Enable the host executor for labels mapped to `host` |
| `--work-dir` | Parent of job work trees |
| `--privileged-jobs` | Hand jobs the docker socket and allow `--privileged` |
| `--docker-host` | Daemon address, `unix://`, `tcp://` or `ssh://`, exported as `DOCKER_HOST` |
| `--container-options` | Extra `docker run` flags for every job container, shell quoted |
| `--valid-volumes` | Host paths and named volumes jobs may mount, glob syntax |
| `--container-network` | Network job containers join, default a private network per job |
| `--insecure-skip-tls` | Skip certificate checks when cloning actions and workflows |
| `--runner-config` | Runner config path, default `DFCLI_RUNNER_CONFIG` or `~/.dfcli/runner.json` |
| `--check` | status only, send a heartbeat to verify the token, marks the runner online |

`register` writes `runner.json`, `start` polls for jobs until interrupted, `status` prints the config and checks docker and git. See [Runners](../actions/runners.md).

## agent

```
dfcli agent list namespace/name [--table]
dfcli agent sessions namespace/name [--agent A] [--trigger T] [--actor U] [--issue N] [--table]
dfcli agent run namespace/name AGENT [--prompt TEXT | --prompt -] [--ref BRANCH] [--model M] [--issue N] [--watch]
dfcli agent view namespace/name SESSION-ID [--transcript]
dfcli agent watch namespace/name SESSION-ID
dfcli agent cancel namespace/name SESSION-ID
dfcli agent approve namespace/name SESSION-ID [--reject] [--note TEXT]
dfcli agent continue namespace/name SESSION-ID INSTRUCTION [--watch]
```

| Flag | Command | Description |
| --- | --- | --- |
| `--trigger` | sessions | `manual`, `pull_request`, `issue`, `label`, `mention`, `review`, `workflow_run` |
| `--prompt` | run | Instruction, `-` reads stdin |
| `--ref` | run | Branch to work from, default branch when empty |
| `--model` | run | Override within the connector allowlist |
| `--watch` | run, continue | Stream the transcript until the session finishes |
| `--transcript` | view | Print the transcript instead of the session |
| `--reject`, `--note` | approve | Reject the pending action, note shown to the agent |

The agent argument is the bot handle with or without `@`. See [Sessions](../agents/sessions.md).

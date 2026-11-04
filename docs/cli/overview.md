# CLI overview

`dfcli` is the command line client for DistroFace. It talks to the ConnectRPC API for control and to the HTTP data planes for bytes, and drives the local `git` binary for clone, checkout and credentials.

## Installing

Static binaries for linux, mac and windows on amd64 and arm64 are on the [releases page](https://github.com/nickheyer/distroface/releases), archived as `dfcli_<version>_<os>_<arch>.tar.gz` or `.zip`. From source:

```bash
make dfcli            # build/dfcli
DFCLI_VERSION=1.2.3 make dfcli
```

`dfcli version` prints the build version.

## Logging in

```bash
dfcli login --server https://df.example.com            # prompts for username and password
dfcli login --server https://df.example.com --token df_xxxxxxxx
```

A password login stores a session token and refreshes it before expiry and after an `unauthenticated` reply, once per call. A personal access token is stored as is and never refreshed, which makes it the right credential for CI. `dfcli logout` revokes the session on the server and deletes the config file, tokens stay valid.

`DFCLI_TOKEN` overrides the stored token for one invocation without touching the config file.

## Configuration

| Item | Location |
| --- | --- |
| Config file | `~/.dfcli/config.json`, or `--config <file>` |
| Instance CA | `~/.dfcli/ca.pem`, written by `dfcli trust install` |
| Runner config | `~/.dfcli/runner.json`, or `DFCLI_RUNNER_CONFIG`, or `--runner-config` |

The config file holds `token`, `expires_at`, `username` and `server`.

## Global flags

| Flag | Default | Description |
| --- | --- | --- |
| `--server` | `http://localhost:8080`, then the server of the last login | Instance url. A bare host probes TLS on 443 first and falls back to http |
| `--timeout` | `5m` | Request timeout, runner jobs are exempt |
| `--config` | `~/.dfcli/config.json` | Config file |
| `--debug` | `false` | Trace requests, refreshes and git invocations on stderr |

An explicit `--server` always wins over the server of the last login.

## TLS

An instance that issues its own certificates is trusted after `dfcli trust install`, which fetches the root from `/.well-known/distroface/ca.pem`, prints its SHA-256 fingerprint for verification, stores it for dfcli, and on linux installs it under `/etc/docker/certs.d/<host>/ca.crt` for docker. An `x509` error from any command points at that step. See [Certificate authority](../administration/certificate-authority.md).

## Output

Commands print proto JSON by default. List commands accept `-t` or `--table` for a tab separated table. Artifact commands keep the v1 JSON shapes for repositories, artifacts and search results.

## Addressing repositories

Git, artifact and image repositories are `[namespace/]name`. For git commands a bare name resolves to your own username. For artifact commands a bare name resolves on the server, your own namespace first, then the unique visible match, qualify it when ambiguous. Image commands always need `namespace/name`. Issues and pull requests take `namespace/name` and a number, a leading `#` is tolerated.

## Git credential helper

```bash
git config --global credential.helper "dfcli git credential"
```

Git asks the helper on every http push and clone. The helper answers only for the configured server, with the stored session or token, and stays silent for every other host. Pass `--server` through the helper string to target another instance.

## Commands

| Group | Purpose |
| --- | --- |
| `login`, `logout`, `trust`, `version` | Session and trust |
| `token` | Personal access tokens |
| `key`, `gpg` | SSH and GPG keys |
| `image` | Image repositories and tags |
| `artifact` | Artifact repositories, upload, download, search |
| `repo`, `git` | Git repositories, clone, credential helper |
| `release`, `issue`, `pr` | Releases, issues, pull requests |
| `org team` | Organization teams |
| `runner` | Actions runner |
| `agent` | Agent sessions |

Every command and flag is listed in the [reference](reference.md).

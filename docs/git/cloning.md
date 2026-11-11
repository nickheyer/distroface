# Cloning and pushing

Repositories are served over smart http on the main listener and over ssh on a separate port that is off until an admin enables it. The same `namespace/name` works for both.

## Clone urls

`GitService.GetGitRepository` returns `http_clone_url` and `ssh_clone_url`, and the repository page shows both.

| Url | Form |
| --- | --- |
| http | `<scheme>://<server.public_hostname>/namespace/name.git`, https when the TLS mode is https only or the request arrived over https |
| ssh on port 22 | `git@<host>:namespace/name.git` |
| ssh on another port | `ssh://git@<host>:<port>/namespace/name.git` |
| wiki | Same urls with a `.wiki.git` suffix, see [Wiki](wiki.md) |

The ssh host is `git.ssh_public_host` when set, otherwise the public hostname with `git.ssh_port`. `ssh_clone_url` is empty while ssh is disabled.

`dfcli git clone [namespace/]name [dir]` runs the local git against the http url, `--ssh` picks the ssh url and fails when ssh is off, `--namespace` qualifies a bare name. See the [CLI reference](../cli/reference.md).

## Smart http

The `.git` suffix is optional on http paths. `info/refs`, `git-upload-pack`, `git-receive-pack`, and `git-upload-archive` are served, protocol v2 is honored through the `Git-Protocol` header, and gzip request bodies are accepted. The dumb protocol (`HEAD`, `objects/`) answers 403. After a rename or transfer the old url answers 301 on `info/refs` once the caller is allowed in. Pulling needs read on the code unit, pushing needs write, and a push to an archived repository or through a read only portal answers 403.

## Http authentication

Git clients receive `WWW-Authenticate: Basic realm="distroface"`. Any of these work:

| Credential | How |
| --- | --- |
| Password | Basic auth with your username and password |
| Personal access token | Basic auth with any username and the `df_` token as the password, or `Authorization: Bearer df_...` |
| Deploy token | A token scoped to one repository, created under Repo > Settings > Deploy tokens |

Tokens are described in [Tokens](../administration/tokens.md). Failed attempts count against `rate_limit.auth_failure_limit` and answer 429 with `Retry-After: 60` while locked. A repository you cannot see answers 401 without credentials and 404 with them.

### Credential helper

`dfcli git credential` answers git's `get` requests with the stored login, so pushes never prompt:

```bash
dfcli login
git config --global credential.helper "dfcli git credential"
```

It only answers for the configured server, scheme, host, and port must match. The username is your login name or `token`, the password is the stored session or personal access token, and an expired session is refreshed first. `store` and `erase` are accepted and ignored.

## Anonymous clones

With `auth.anonymous_access` on, a request without an `Authorization` header runs as the anonymous role, which holds `git` `read` and `pull` by default, so public repositories clone without credentials. Private repositories answer 401. Anonymous fetches are limited per client ip by `rate_limit.anon_pull_per_minute`, signed in fetches per user by `rate_limit.pull_per_minute`, both reported in `X-RateLimit-*` headers with a 429 and `Retry-After` when exceeded.

## Enabling ssh

Git over ssh is off until an admin turns it on under Admin > Git > SSH.

| Setting | Default | Description |
| --- | --- | --- |
| `git.ssh_enabled` | `false` | Opens the listener and shows ssh clone urls |
| `git.ssh_port` | `22` | Listener port, system tier only, rebinds live |
| `git.ssh_public_host` | `""` | Host or `host:port` shown in clone urls, blank uses the public hostname |
| `git.ssh_portal_ports` | `false` | Organization admins may give a portal its own ssh port, system tier only |

Saving probes the port first through `SettingsService.ProbeGitSSHPort` and the save is refused with `git ssh not saved: ...` when the port cannot be bound, either `needs elevated privileges on this host, pick a port above 1023 or grant the binary cap_net_bind_service` or `is already in use by another process on this host`. The page shows the listener state of the saved port. The container publishes 22, when it is taken on the host publish another one and tell clone urls about it:

```bash
docker run -p 8080:8080 -p 2222:22 \
  -e DISTROFACE_SETTINGS_JSON='{"git":{"sshEnabled":true,"sshPublicHost":"localhost:2222"}}' \
  -v distroface:/app/data nickheyer/distroface:latest
```

With `git.ssh_portal_ports` on, org portals may add their own ssh ports, see [Portals](../administration/portals.md).

## Host keys

An ed25519 and a 3072 bit RSA host key are generated on first start under `<storage.data_dir>/keys/ssh_host_ed25519_key` and `ssh_host_rsa_key`, each with a `.pub` beside it. Admin > Git lists their public lines and SHA256 fingerprints, also returned by `SettingsService.GetGitServerInfo`. The server announces itself as `SSH-2.0-distroface`.

## Ssh authentication

Only public key authentication is offered. The key is matched by SHA256 fingerprint against user keys and deploy keys. Keys registered for signing only, expired keys, and unknown keys are refused, with six attempts per connection, eight concurrent sessions per key, and a five minute idle timeout. Opening a shell prints `Hi <user>! You've successfully authenticated, but Distroface does not provide shell access.` The accepted commands are `git-upload-pack`, `git-receive-pack`, `git-upload-archive`, `git-lfs-authenticate`, and `git-lfs-transfer` with a `namespace/name.git` or `namespace/name.wiki.git` path.

### User keys

Register keys under Settings > Keys or with `KeyService.AddSSHKey`. The key must be in `authorized_keys` form, the title defaults to the key comment, `expires_in_days` is optional, and a fingerprint registers once per instance.

| Usage | Meaning |
| --- | --- |
| `auth` | Ssh access only |
| `signing` | Commit signature verification only, never accepted for login |
| `both` | Ssh access and signing |

`dfcli key add [file]` reads a key from a file or stdin and takes `--title`, `--usage auth|signing|both`, and `--signing`. `dfcli key list` and `dfcli key rm id` complete the set. Signing is described in [Signing](signing.md).

### Deploy keys

Repo > Settings > Deploy keys registers a key for one repository, `read_only` blocks pushes. Deploy keys reach the code, wiki, and packages units only and cannot manage anything.

## Concurrency and timeouts

| Setting | Default | Description |
| --- | --- | --- |
| `git.max_concurrent_transfers` | `32` | Pack operations and archive downloads running at once across http and ssh, zero is unlimited, system tier only |

A transfer waits up to ten seconds for a slot. Http then answers 503 with `Retry-After: 5`, ssh prints `server busy`. A transfer that moves no bytes for five minutes is cut. While a repository is being renamed http answers 503 with `Retry-After: 2` and ssh prints `repository is moving, retry shortly`.

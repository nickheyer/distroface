# Tokens

Distroface issues three kinds of credential: session tokens for the UI and API, personal access tokens for scripts and clients, and short lived registry tokens that `docker` obtains from the token endpoint. Runner registration tokens and job tokens are covered under Actions.

## Personal access tokens

A personal access token carries the identity and roles of the user who created it. The plaintext starts with `df_` and is shown once. Only a SHA-256 hash is stored, so a lost token must be replaced.

Create one under Settings > API tokens, with `dfcli token create`, or through `TokenService.CreateAPIToken`:

```bash
curl -X POST http://localhost:8080/distroface.v1.TokenService/CreateAPIToken \
  -H 'Authorization: Bearer <session token>' -H 'Content-Type: application/json' \
  -d '{"name":"ci","expiresInDays":90}'
```

| Field | Description |
| --- | --- |
| `name` | Required label |
| `expires_in_days` | Days until expiry, absent or `0` never expires |
| `scope` | Optional narrowing to one git repository or org, see below |

The token is accepted as a bearer token on every RPC, as the password for `docker login` and git over HTTP, and by `dfcli login`. Each use updates `last_used_at`. An expired token or a token of a deactivated user is refused. A scoped token cannot create tokens.

`dfcli token create --name NAME [--scope git:namespace/name | org:name] [--read-only] [--expires-days N]`, `dfcli token list [--git-repo namespace/name]` and `dfcli token delete ID` wrap the same RPCs, see [CLI reference](../cli/reference.md).

## Scoped tokens

A scope turns a personal token into a deploy token that only reaches one object.

| Scope | `id` | Reach |
| --- | --- | --- |
| `CREDENTIAL_SCOPE_TYPE_GIT_REPO` | `namespace/name` | The repository and its linked packages. Never create, delete or manage |
| `CREDENTIAL_SCOPE_TYPE_ORG` | org name | Every object inside the org namespace |

`read_only` refuses push, create, update, delete and manage. Creating a git repo scope requires that the caller can see the repository, and write access on its code unit unless the token is read only. Creating an org scope requires membership or `organizations:manage`.

A scoped token can call RPCs about its object, list within it, and read its own identity. Every other method answers `permission_denied` with `credential scope does not cover this procedure`. Deploy tokens scoped to a repository appear under that repository's Settings > Tokens and in `ListAPITokens` with `git_repo` set, where repository admins see tokens created by anyone.

## Listing and revoking

`TokenService.ListAPITokens` returns the caller's tokens, filterable by `name`. Roles with `tokens:manage` see every token. `TokenService.DeleteAPIToken` revokes by id, again any token for `tokens:manage`. Revocation is immediate for new requests.

## Registry tokens

Image clients follow the Docker token authentication flow. The registry answers `401` with a `WWW-Authenticate` challenge, the client fetches a token from `/auth/token` by `GET` or `POST`, then presents it as a bearer token.

```bash
curl -u alice:df_... 'http://localhost:8080/auth/token?service=distroface&scope=repository:alice/app:pull,push'
```

| Parameter | Description |
| --- | --- |
| `service` | Audience, echoed into the token |
| `scope` | Space separated `repository:<namespace/name>:<actions>` or `registry:catalog:*` |
| `account` | Subject override, defaults to the username |
| Credentials | HTTP basic auth, or `username` and `password` form fields. The password may be a local password or a `df_` token |

The response is `{"token","access_token","expires_in","issued_at"}`. Tokens are ES256 JWTs signed with a key generated into `<data_dir>/keys/token.key` and `token.crt` on first start. They live `auth.token_expiry_seconds`, default 900.

Requested actions are filtered to what the caller may do:

| Action | Granted when |
| --- | --- |
| `pull` | The repository is public and the roles allow `repositories:pull`, or the caller owns it, is a member of its org, holds `repositories:manage`, or has a scoped grant on it |
| `push` | The caller is the namespace owner, a member of the org, or holds `repositories:manage` on an existing namespace |
| `delete` | The owner, an org owner or admin, `repositories:manage`, or an explicit grant |
| `registry:catalog` | `repositories:manage`, never anonymous, scoped, or on a portal host |

Portals can rewrite names and strip pushes, see [Portals](portals.md). Without credentials the token is issued as the anonymous role when anonymous access is on for the host, otherwise the endpoint answers `401`. When no auth provider is enabled at all every caller is admin.

A credentialed request without a scope is the `docker login` ping and is audited as `Registry/login`. Failed credentials count toward the lockout described in [Users and roles](users-and-roles.md).

## Session tokens

`AuthService.Login`, `AuthService.Register` and the OIDC callback return an HS256 JWT backed by a row in the sessions table. It is sent as `Authorization: Bearer` or as the `distroface_session` cookie. `AuthService.RefreshSession` issues a new token and deletes the old one, `AuthService.Logout` deletes it, and a password reset deletes every session of the account. The lifetime is `auth.session_timeout_seconds`, default one day. Details are in [Users and roles](users-and-roles.md).

## Runner and job tokens

Runner registration tokens start with `dfr_` and come from `ActionsService.CreateRunnerToken`, see [Runners](../actions/runners.md). Each workflow job receives a job token as `GITHUB_TOKEN` whose registry pushes are attributed to the run, see [Actions](../actions/overview.md).

## Signing and sealing keys

Session tokens are signed with `auth.jwt_secret`. When unset a secret is generated once and stored in the database. Stored secrets such as the OIDC client secret and mail passwords are encrypted with `auth.sealing_key`, generated into `<data_dir>/sealing.key` when unset. See [Configuration](../getting-started/configuration.md).

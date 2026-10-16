<div align="center">
  <p><b>DistroFace</b><br></p>
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="web/distroface/static/icon.png">
    <img src="web/distroface/static/splash-icon.png" alt="DistroFace" width="120">
  </picture>
  <p><b>Blob Distribution & Storage</b><br></p>
</div>

![](.github/screenshots/repo.png)

## Run

```bash
docker run -p 8080:8080 -v distroface:/app/data nickheyer/distroface:latest
```

Open http://localhost:8080 and create an account, first one is admin. Git over ssh is off until an admin enables it under Admin, Git. The port defaults to 22, the settings page probes it before saving, and the container publishes it with `-p 22:22`. When 22 is taken on the host, publish another one and tell clone urls about it:

```bash
docker run -p 8080:8080 -p 2222:22 -e DISTROFACE_SETTINGS_JSON='{"git":{"sshEnabled":true,"sshPublicHost":"localhost:2222"}}' -v distroface:/app/data nickheyer/distroface:latest
```

```bash
docker login localhost:8080
docker tag alpine localhost:8080/myteam/alpine
docker push localhost:8080/myteam/alpine
```

```bash
git clone http://localhost:8080/myteam/app        # the same ns/name is one project
git clone git@localhost:myteam/app.git
```

## Inside

- Git repositories served in process by go-git over smart http and ssh, with LFS, branch protection, code browsing and editing, issues, pull requests with reviews, releases, wiki, projects, notifications, mirrors, imports, and Actions
- OCI registry under `/v2/`, namespaced per user and org
- Artifact repos: versioned files, key=value properties, query-based download
- One project per `ns/name`: images and artifacts named after a git repo link to it, share access, and show up on its releases
- Org portals: A proxied interface for org resources, scoped to org members.
- Local accounts and OIDC (Keycloak / Authelia / Entra recipes in [`oidc/`](oidc))
- RBAC, personal access tokens, invites, audit log
- Webhooks on push, pull, and delete
- In-app TLS with ACME auto-cert issuance, per-hostname certs via SNI
- Registry GC and artifact retention reapers
- Rate limits and login lockout

|                                    |                                      |
| ---------------------------------- | ------------------------------------ |
| ![](.github/screenshots/explore.png) | ![](.github/screenshots/artifacts.png) |
| ![](.github/screenshots/admin.png)   | ![](.github/screenshots/api.png)       |

## API

ConnectRPC, every method is a `POST` with a JSON body, so any HTTP client works. Interactive reference with OpenAPI download ships in the UI at `/docs/api`.

## CLI

Static `dfcli` binaries for linux/mac/windows on the [releases page](https://github.com/nickheyer/distroface/releases), or `make dfcli`.

```bash
dfcli login
dfcli artifact upload builds ./api-server.tar.gz -v 2.3.1 --property os=linux
dfcli artifact download builds -v 2.3.1 --property os=linux -o api-server.tar.gz
git config --global credential.helper "dfcli git credential"   # passwordless http pushes
dfcli repo create myteam/app --private && dfcli git clone myteam/app
dfcli pr create myteam/app --base main --head feature --title "Feature"
dfcli release upload myteam/app v1.2.3 ./dist/*
dfcli runner register --scope system --token <t> --name ci && dfcli runner start
```

Repos are addressed as `[namespace/]name`, bare names resolve to your own namespace first, then the unique visible match, qualify the name if it's ambiguous.

## Config

One `config.yaml`, every default is in [`config.example.yaml`](config.example.yaml). Any key works as a `DISTROFACE_*` env var. Seed users and orgs on first boot with the `bootstrap:` block.

## Hack

```bash
make deps   # go, npm, buf
make dev    # backend :8080 + vite frontend :5173
make build  # single binary, UI embedded
```

MIT, see [LICENSE](LICENSE).

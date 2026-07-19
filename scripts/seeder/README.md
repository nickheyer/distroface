# seeder

Seeds a running distroface instance with a configurable number of rows per data model, in dependency order.

## Run

```sh
go run ./scripts/seeder --password <admin-password>
```
> Defaults produce thousands of rows and are probably fine

## Configuration

Can set config via:

1. Flag — `--<key> <value>`
2. Environment — `SEED_<KEY>`
3. Or leave it and use the default.

### Options

| Flag | Env | Default | Description |
|------|-----|---------|-------------|
| `--base-url` | `SEED_BASE_URL` | `http://localhost:8080` | Target server base URL |
| `--username` | `SEED_USERNAME` | `admin` | Admin username |
| `--password` | `SEED_PASSWORD` | — | Admin password (required) |
| `--concurrency` | `SEED_CONCURRENCY` | `min(max(NumCPU*2,8),32)` | Worker pool size per phase |
| `--timeout` | `SEED_TIMEOUT` | `60s` | HTTP request timeout |
| `--retries` | `SEED_RETRIES` | `3` | Retries on transient failures |
| `--name-prefix` | `SEED_NAME_PREFIX` | `seed` | Created entity name prefix (lowercase) |
| `--phases` | `SEED_PHASES` | `""` | Phase allowlist, empty runs all |
| `--fail-fast` | `SEED_FAIL_FAST` | `false` | Abort phase on first error |
| `--progress-interval` | `SEED_PROGRESS_INTERVAL` | `3s` | Progress update interval |
| `--health-timeout` | `SEED_HEALTH_TIMEOUT` | `60s` | Max health wait before aborting |
| `--max-error-samples` | `SEED_MAX_ERROR_SAMPLES` | `5` | Errors printed per phase before suppression |
| `--roles` | `SEED_ROLES` | `25` | Role count |
| `--users` | `SEED_USERS` | `300` | User count |
| `--orgs` | `SEED_ORGS` | `40` | Organization count |
| `--org-members` | `SEED_ORG_MEMBERS` | `5` | Members added per organization |
| `--invites` | `SEED_INVITES` | `150` | Registration invite count |
| `--image-repos` | `SEED_IMAGE_REPOS` | `120` | Docker repository count |
| `--tags-per-repo` | `SEED_TAGS_PER_REPO` | `5` | Image tags pushed per repository |
| `--pulls` | `SEED_PULLS` | `800` | Manifest pull count |
| `--stars` | `SEED_STARS` | `800` | Repository star count |
| `--tokens` | `SEED_TOKENS` | `400` | API token count |
| `--webhooks` | `SEED_WEBHOOKS` | `150` | Webhook count |
| `--artifact-repos` | `SEED_ARTIFACT_REPOS` | `60` | Artifact repository count |
| `--artifacts` | `SEED_ARTIFACTS` | `1500` | Uploaded artifact count |
| `--portals` | `SEED_PORTALS` | `20` | Registry portal count |
| `--layer-size` | `SEED_LAYER_SIZE` | `2048` | Image layer size in bytes |
| `--artifact-size` | `SEED_ARTIFACT_SIZE` | `2048` | Artifact payload size in bytes |
| `--webhook-url` | `SEED_WEBHOOK_URL` | `http://127.0.0.1:9/seed-hook` | Webhook target URL |
| `--webhooks-active` | `SEED_WEBHOOKS_ACTIVE` | `false` | Create webhooks in active state |
| `--registry-service` | `SEED_REGISTRY_SERVICE` | `distroface-registry` | Registry token service name |
| `--portal-ports` | `SEED_PORTAL_PORTS` | `15181,15182,15183,15184,15185` | Portal listener port pool, empty keeps portals on the app port |

## Phases

Run in dependency order later phases sample from the pools built by earlier ones.

| Phase | Endpoint | Depends on | Notes |
|-------|----------|------------|-------|
| `roles` | `RoleService/CreateRole` | — | Random permission subsets |
| `users` | `UserService/AdminCreateUser` | roles | All get a baseline member role so later phases work |
| `orgs` | `OrganizationService/CreateOrganization` + `AddOrgMember` | users | First member added as org admin |
| `invites` | `AuthService/CreateInvite` | roles | Every 3rd invite gets a PIN |
| `images` | OCI `/v2/` blob + manifest push | orgs | Rotates OCI manifest, Docker schema2, OCI index, Docker list |
| `pulls` | OCI `/v2/` manifest GET | images | Bumps pull counts + audit, same target the image was pushed to |
| `stars` | `RepositoryService/StarRepository` | users, images | As seeded users (spreads sessions) |
| `tokens` | `TokenService/CreateAPIToken` | users | As seeded users |
| `webhooks` | `WebhookService/CreateWebhook` | images or orgs | Alternates repo/org scope |
| `artifact-repos` | `ArtifactService/CreateArtifactRepository` | orgs | Split between admin and org namespaces |
| `artifacts` | initiate → chunk PATCH → complete | artifact-repos | Random payloads, versioned, with properties |
| `portals` | `PortalService/CreatePortal` | orgs | Cycles app port + port pool, one catch-all per pool port, hostnames stack on every port |

## Seeding targets

Before the `images`/`artifacts` phases the seeder creates one pushable hostname portal per `--portal-ports` port, each bound to an org — half using `map_unqualified`, half using a custom mapping rule (`([^/]+)` → `org/$1`), alternating `require_auth`. The base app plus those portals become the target pool: image repos and artifact uploads are spread across all of them, so portal-proxied pushes/pulls land in org namespaces via the portal path mapper, while direct traffic hits the app as usual. Registry tokens are fetched through the same target that gets the push.

The bulk `portals` phase then adds variety on top: portals on the app port, one catch-all per pool port, and hostname portals stacked on every port.

Audit events and sessions accumulate as side effects of every phase.

```sh
# config tier only
go run ./scripts/seeder --password secret --phases roles,users,orgs,invites

# just push a lot of images
go run ./scripts/seeder --password secret --phases images --image-repos 500 --tags-per-repo 10

# everything, bigger
SEED_PASSWORD=secret SEED_USERS=2000 SEED_ARTIFACTS=10000 go run ./scripts/seeder
```

Multi-user phases (`stars`, `tokens`) log in as users created in the same run, so they need the `users` phase enabled alongside them.

# Building and development

The server is Go 1.26 with `CGO_ENABLED=0`. The UI is a SvelteKit app under `web/distroface` embedded into the binary at build time. API code is generated from the protos under `proto/` with buf.

## Prerequisites

Go 1.26, Node 22 or newer with npm, and Docker. Buf runs through the `bufbuild/buf` image, so no local install is needed.

```bash
make deps   # go mod download, buf dep update, npm install
```

## Make targets

| Target | What it does |
| --- | --- |
| `make dev` | Cleans `./data` and `build/`, then runs the backend on :8080 and the vite dev server on :5173 |
| `make run` | Same without the clean |
| `make build` | Builds the UI, then `build/distroface` with the UI embedded |
| `make prod` | `make build` with the `embed` tag |
| `make dfcli` | Builds `build/dfcli`, `DFCLI_VERSION` sets the version string |
| `make test` | `go test ./...` |
| `make fmt`, `make lint`, `make check` | Go and prettier formatting, prettier and eslint, svelte-check |
| `make proto` | Generates Go, Connect, TypeScript and OpenAPI code from `proto/` |
| `make gen` | `proto-clean` then `proto` |
| `make proto-lint`, `make proto-format`, `make proto-breaking` | Buf lint, format, breaking check against `main` |
| `make clean` | Removes `./data` and `build/distroface` |
| `make kill-dev` | Kills orphaned dev processes |
| `make dev-docker` | Builds and runs `docker-compose.dev.yaml` without cache |
| `make dev-auth-keycloak`, `make dev-auth-authelia`, `make dev-auth-entra` | Runs the matching `oidc/<provider>/docker-compose.yaml` |
| `make seed` | Runs `scripts/seeder` against a running instance, `SEED_ARGS` passes flags |
| `make bench` | Runs `scripts/bench` against a running instance, `BENCH_ARGS` passes flags |

`dev.env` in the repo root is included by make and exported, it seeds a bootstrap admin named `seeder` for local runs.

## Generated code

`buf.gen.yaml` writes Go and Connect code to `pkg/proto`, TypeScript to `web/distroface/src/lib/proto`, and OpenAPI JSON to `web/distroface/src/lib/proto/specs`, which the UI merges into the console at `/docs/api`. Generated code is not committed, run `make proto` after changing a proto or pulling.

## Container images

`docker/Dockerfile.distroface` builds the server image in four stages: buf, node, go, alpine. `docker/Dockerfile.runner` builds the standalone Actions runner image with `dfcli` as its entrypoint, see [Runners](../actions/runners.md). `APP_VERSION` is the only build arg.

## Releases

`.distroface/workflows/release.yml` runs on tags starting with `d`. It generates protos, builds the UI, builds `distroface` for linux amd64, linux arm64 and windows amd64, builds `dfcli` for linux, darwin and windows on amd64 and arm64, pushes the image to the instance registry tagged with the version and `latest`, and creates a release with checksums and a changelog. Tags ending in `-dev` only push the `dev` image. `.goreleaser.yaml` describes the dfcli archive layout for local snapshot builds.

## Seeder and benchmark

`scripts/seeder` fills an instance with roles, users, orgs, images, artifacts, git repos, issues, pull requests and releases in dependency order. Every count is a flag or a `SEED_*` variable, see `scripts/seeder/README.md`.

```bash
go run ./scripts/seeder --password <admin-password>
```

`scripts/bench` measures docker push and pull, git clone and push, LFS, and artifact upload and download against a scratch instance.

```bash
go run ./scripts/bench -op all -tier small -runs 3 -base http://localhost:8080
```

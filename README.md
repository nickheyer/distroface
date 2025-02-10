<div align="center">
  <img src="./web/static/df_text__web.webp" alt="distroface_promo_banner">
  <p>A container registry built for developers. Convenient and without compromise.</p>
</div><hl>

# DistroFace Docs

## Features

- Built-in Authentication & Authorization
- Modern Web UI
- Image Migration Tool
- Role-Based Access Control
- Tag Management
- Registry Statistics

## API Endpoints

### Authentication

| Endpoint | Method | Description | Required Role |
|----------|---------|-------------|---------------|
| `/auth/token` | GET/POST | Get registry authentication token | None |
| `/api/v1/auth/login` | POST | Web UI login | None |
| `/api/v1/auth/refresh` | POST | Refresh authentication token | None |

### Registry Operations

| Endpoint | Method | Description | Required Role |
|----------|---------|-------------|---------------|
| `/v2/_catalog` | GET | List repositories | VIEW:IMAGE |
| `/v2/{name}/tags/list` | GET | List tags | VIEW:TAG |
| `/v2/{name}/manifests/{reference}` | GET | Get manifest | PULL:IMAGE |
| `/v2/{name}/manifests/{reference}` | PUT | Upload manifest | PUSH:IMAGE |
| `/v2/{name}/blobs/{digest}` | GET | Download blob | PULL:IMAGE |
| `/v2/{name}/blobs/uploads/` | POST | Start blob upload | PUSH:IMAGE |
| `/v2/{name}/blobs/uploads/{uuid}` | PATCH | Upload blob chunk | PUSH:IMAGE |
| `/v2/{name}/blobs/uploads/{uuid}` | PUT | Complete blob upload | PUSH:IMAGE |

### Group & Role Management
| Endpoint | Method | Description | Required Role |
|----------|---------|-------------|---------------|
| `/api/v1/groups` | GET | List groups | VIEW:GROUP |
| `/api/v1/groups/{name}` | PUT | Update group | UPDATE:GROUP |
| `/api/v1/groups/{name}` | DELETE | Delete group | DELETE:GROUP |
| `/api/v1/roles` | GET | List roles | VIEW:SYSTEM |
| `/api/v1/roles/{name}` | PUT | Update role | ADMIN:SYSTEM |
| `/api/v1/roles/{name}` | DELETE | Delete role | ADMIN:SYSTEM |

### Artifact Management
| Endpoint | Method | Description | Required Role |
|----------|---------|-------------|---------------|
| `/api/v1/artifacts/repos` | GET | List artifact repositories | VIEW:REPO |
| `/api/v1/artifacts/repos` | POST | Create artifact repository | CREATE:REPO |
| `/api/v1/artifacts/repos/{repo}` | DELETE | Delete repository | DELETE:REPO |
| `/api/v1/artifacts/{repo}/upload` | POST | Initialize artifact upload | UPLOAD:ARTIFACT |  
| `/api/v1/artifacts/{repo}/upload/{uuid}` | PATCH | Upload artifact chunk | UPLOAD:ARTIFACT |
| `/api/v1/artifacts/{repo}/upload/{uuid}` | PUT | Complete artifact upload | UPLOAD:ARTIFACT |
| `/api/v1/artifacts/{repo}/{version}/{path}` | GET | Download artifact | DOWNLOAD:ARTIFACT |
| `/api/v1/artifacts/{repo}/{version}/{path}` | DELETE | Delete artifact | DELETE:ARTIFACT |
| `/api/v1/artifacts/{repo}/versions` | GET | List artifact versions | VIEW:ARTIFACT |
| `/api/v1/artifacts/search` | GET | Search artifacts | VIEW:ARTIFACT |

### Settings Management
| Endpoint | Method | Description | Required Role |
|----------|---------|-------------|---------------|
| `/api/v1/settings/{section}` | GET | Get settings | ADMIN:SYSTEM |
| `/api/v1/settings/{section}` | PUT | Update settings | ADMIN:SYSTEM |
| `/api/v1/settings/{section}/reset` | POST | Reset settings | ADMIN:SYSTEM |

### User Management

| Endpoint | Method | Description | Required Role |
|----------|---------|-------------|---------------|
| `/api/v1/users` | GET | List users | VIEW:USER |
| `/api/v1/users` | POST | Create user | CREATE:USER |
| `/api/v1/users/groups` | PUT | Update user groups | UPDATE:USER |

### Migration

| Endpoint | Method | Description | Required Role |
|----------|---------|-------------|---------------|
| `/api/v1/registry/migrate` | POST | Start migration | MIGRATE:TASK |
| `/api/v1/registry/migrate/status` | GET | Check migration status | MIGRATE:TASK |

## DFCli - API Client Tool

`dfcli` is a command-line interface for interacting with DistroFace. It provides commands for managing images, artifacts, users, groups, roles, and settings.

### Get the cli

The `dfcli` binary can be obtained from the releases tab (if one exist) or can be built by cloning this repo and running `make build-cli` in the project root.

### Authentication

```bash
# Login to server
dfcli login
dfcli login -u username -p password

# Logout
dfcli logout
```

### Image Management

```bash
# List images
dfcli image list

# Manage image tags
dfcli image tags <image-name>
dfcli image delete <image-name> <tag>

# Update visibility
dfcli image visibility <image-name> <public|private>
```

### Artifact Management

```bash
# Create/list repositories
dfcli artifact create <repo> [-d description] [-p]
dfcli artifact list

# Upload artifacts
dfcli artifact upload <repo> <file> [-v version] [-p path] [--property key=value]

# Download artifacts (using query route)
# - By default, if exactly one artifact matches, returns that file
# - If multiple match, returns ZIP or TAR.GZ (or force archive with --archive)
dfcli artifact download <repo> \
  --version 1.2.3 \
  --path bin/cli-binary \
  --property env=production \
  --archive \
  --format tar.gz \
  --output downloaded.tar.gz

# Or with shorter flags:
dfcli artifact download my-repo -v 1.2.3 -p bin/cli-binary -P env=prod,branch=main --archive --format zip -o out.zip

# Delete artifacts (legacy direct path)
dfcli artifact delete <repo> <version> <path>

# Search artifacts
dfcli artifact search [--property key=value]
```

### User Management

```bash
# List and manage users
dfcli user list
dfcli user create <username> -p <password> [-g group1,group2]
dfcli user delete <username>
dfcli user groups <username> -g <group1,group2>
```

### Group Management

```bash
# List and manage groups
dfcli group list
dfcli group create <name> -d <description> -r <role1,role2>
dfcli group update <name> [-d description] [-r role1,role2]
dfcli group delete <name>
```

### Role Management

```bash
# List and manage roles
dfcli role list
dfcli role update <name> [--add ACTION:RESOURCE] [--remove ACTION:RESOURCE]
dfcli role delete <name>
```

### Settings Management

```bash
# View and manage settings
dfcli settings get <section>
dfcli settings update <section> -s key=value
dfcli settings reset <section>
```

### Configuration

The CLI configuration is stored in `~/.dfcli/config.json`. You can specify a different server URL using the `--server` flag or by setting it in the config file:

```bash
dfcli --server http://registry.example.com
```

> [!note]
> `registry.example.com` would also work, assuming http not https.


## Authentication Model

![]()

<div align="center">
  <img src="./auth-diagram.png" alt="Authentication Flow" width="70%">
  <p>Distroface runs the webui user auth and permissions model through the same channels as the docker client.</p>
</div>

## Quick Start

```bash
# Run with Docker
docker run -p 8668:8668 \
  -v /path/to/data:/data \
  nickheyer/distroface:latest

# Default login
Username: admin
Password: admin
```

## Development

```bash
# Install dependencies
make deps

# Build + run in development mode
make dev

# Build for production
make build

# Build the cli tool
make build-cli
```

## License

MIT License - see [LICENSE](LICENSE) for details

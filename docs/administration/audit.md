# Audit log

The audit log is a database table of security relevant events: sign ins, credential use, denied calls and every mutation made through the API. Admin > Audit log shows it and `AuditService.ListAuditEvents` serves it.

## Settings

| Setting | Default | Description |
| --- | --- | --- |
| `security.audit.enabled` | `true` | Off drops every event |
| `security.audit.retention_days` | `90` | Events older than this are deleted daily, `0` keeps them forever |

Pruning runs at startup and every 24 hours, on one instance at a time when a shared cache is configured.

## What is recorded

| Action | Resource | When |
| --- | --- | --- |
| `<Service>/<Method>` | The resource of the RPC mapping | Every RPC whose mapped action is not `read` or `pull`, with the object id from the request as `detail`. Calls refused by the auth layer are recorded as `denied` |
| `AuthService/Login`, `AuthService/Register`, `AuthService/Logout`, `UserService/ChangePassword` | `auth` | Credential lifecycle, `detail` is the username or identifier |
| `Registry/login` | `auth` | The `docker login` ping at `/auth/token`, success or denied |
| `Registry/push`, `Registry/pull`, `Registry/delete` | `registry` | Manifest and tag events with the reference as `detail` |
| `Git/auth` | `auth` | Denied git credentials over HTTP |
| `Git/fetch`, `Git/push` | `git` | Transfers over HTTP and SSH |
| `Actions/run` | `actions` | A workflow run is planned |
| `Agents/session` | `agents` | An agent session starts |
| `Mail/send` | `mail` | A message is abandoned after its last delivery attempt |
| `Team/add-member` | `organizations` | A user joins a team |

Outcomes are `success`, `denied` and `error`. A denied outcome covers `unauthenticated` and `permission_denied` results, any other failure is `error`. When the caller authenticated with a client certificate the `detail` of an RPC event notes the certificate common name.

## Event fields

| Field | Description |
| --- | --- |
| `id` | Row id |
| `actor` | Username, or the attempted username for a denied login |
| `actor_id` | User id when the caller was resolved |
| `source_ip` | Client address, taken from `X-Forwarded-For` only behind a trusted proxy |
| `action` | Short name from the table above |
| `resource` | `auth`, `registry`, `git`, `users`, `roles`, `settings`, `tokens`, `organizations`, `webhooks`, `artifacts`, `actions`, `agents` or `mail` |
| `outcome` | `success`, `denied` or `error` |
| `detail` | Object, reference or subject |
| `created_at` | UTC timestamp |

Trusted proxies are configured with `server.trusted_proxies`, see [Configuration](../getting-started/configuration.md).

## Querying

`AuditService.ListAuditEvents` needs `settings:read`. The page query accepts filters on `action`, `actor`, `outcome`, `resource` and `source_ip`, free text over `action`, `actor` and `resource`, and `order_by` on `created_at`, newest first by default.

```bash
curl -X POST http://localhost:8080/distroface.v1.AuditService/ListAuditEvents \
  -H 'Authorization: Bearer df_...' -H 'Content-Type: application/json' \
  -d '{"page":{"pageSize":50,"query":{"filters":[
        {"field":"outcome","match":"MATCH_KIND_EQUALS","value":"denied"},
        {"field":"actor","value":"alice"}]}}}'
```

Pagination follows the common page object, see [API overview](../api/overview.md).

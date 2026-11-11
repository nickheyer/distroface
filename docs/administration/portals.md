# Portals

A portal is an extra address an organization owns. Requests arriving on the portal's hostname or port serve the whole app scoped to that organization, or are proxied to a backend service. Each portal picks its own certificate source and scheme.

Org admins manage portals under Org > Portals. Instance rules for hostnames live under Admin > Portals.

## Settings

| Setting | Default | Description |
| --- | --- | --- |
| `portals.require_hostname_approval` | `false` | New org claimed hostnames wait for a system admin before certificates issue. System scope only |
| `portals.hostname_blacklist` | empty | Patterns organizations may not claim, exact names or `*.suffix`. System scope only |
| `portals.isolated` | `false` | Set at org scope. Portals of the organization refuse content outside its namespace |
| `acme.email`, `acme.directory_url` | inherited | Org and portal scope overrides for the ACME account, see [ACME](acme.md) |
| `tls.mtls_mode` | inherited | Portal scope override of the client certificate policy, see [TLS](tls.md) |
| `auth.anonymous_access` | inherited | Org and portal scope override for signed out visitors |

## Creating a portal

`PortalService.CreatePortal` takes the organization id and a name plus the fields below. `PortalService.UpdatePortal` changes any subset, `PortalService.DeletePortal` removes the portal together with its uploaded certificate, its settings scope, and its hostname registration.

| Field | Description |
| --- | --- |
| `hostname` | Lowercase DNS name the portal answers on, empty makes a catch-all for the port |
| `port` | Dedicated listener port, 0 serves on the main port. Ports may be shared by several hostnames |
| `sshPort` | Dedicated git ssh port, 0 serves canonical paths on the primary ssh port only. Needs `git.ssh_enabled` and `git.ssh_portal_ports` on the instance, cleared on backend portals |
| `publicHost` | Host with optional port written into links and mail, for portals a proxy exposes elsewhere |
| `certSource` | `NONE`, `ACME`, `ORG_CA`, `ORG_CERT`, or `MANUAL` |
| `tls` | Require https, cleartext requests redirect |
| `mapUnqualified` | Single segment names get the organization prefix |
| `rules` | Regex rewrite rules, first match wins, evaluated before `mapUnqualified` |
| `allowPush` | Off makes the registry and artifact surfaces read only |
| `requireAuth` | Legacy hard deny for anonymous visitors, prefer `auth.anonymous_access` at portal scope |
| `hidePrimaryLink` | Hide the portal UI's link back to the primary host |
| `backendUrl` | Proxy every request to this http or https service. Needs instance admin rights |
| `backendRewriteHost`, `backendInsecure` | Send the backend's host instead of the client's, skip TLS verification for an https backend |

```bash
curl -X POST https://registry.example.com/distroface.v1.PortalService/CreatePortal \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"orgId":"org_123","name":"public-mirror","hostname":"mirror.example.com","certSource":"CERT_SOURCE_ACME","tls":true,"mapUnqualified":true}'
```

## Hostnames and ports

A portal needs a hostname, a port, or both. Saves are refused when the hostname equals the primary hostname, when it is already used by another portal on the same port, when a port already has a catch-all, when a catch-all is placed on the main port, or when the port cannot be bound. A hostname on the main port is stored with port 0. Dedicated ports are opened as soon as the portal is saved and closed when it is disabled or deleted. Portal listeners accept TLS and cleartext on the same port, like the main listener.

Requests resolve to a portal in this order: the port and hostname, the port's catch-all, then a hostname only portal. Links built on a catch-all portal use the primary hostname with the listener port unless `publicHost` is set.

The Docker image grants the binary `cap_net_bind_service`, so portals and the ACME challenge listener can use ports below 1024 while running as uid 1000.

## Certificate sources

| Source | Material |
| --- | --- |
| `CERT_SOURCE_NONE` | Cleartext only |
| `CERT_SOURCE_ACME` | Issued by the external ACME CA for the portal hostname, see [ACME](acme.md) |
| `CERT_SOURCE_ORG_CA` | Leaf minted on demand from the organization's signing CA, see [certificate authority](certificate-authority.md) |
| `CERT_SOURCE_ORG_CERT` | The organization's shared uploaded certificate at scope `TLS_SCOPE_ORG` |
| `CERT_SOURCE_MANUAL` | A certificate uploaded for this portal at scope `TLS_SCOPE_PORTAL` |

Upload material with `CertificateService.UploadTLSCertificate` passing `orgId` and, for manual portal certificates, `portalId`. Org > Certificates holds the shared certificate and the signing CA, the portal form holds the per portal upload. The computed state and first blocker are returned on every portal as `certState` and `certDetail`, the full list comes from `CertificateService.GetCertStatus` with `orgId` and `portalId`. Uploaded material must cover the portal hostname and be unexpired to read as ready.

## Scheme enforcement

The `tls` flag needs a certificate source other than none. Once the portal's certificate reads ready, cleartext requests on the portal get a 302 to `https://<host><path>`. Until then cleartext keeps working. This is independent of `tls.mode` on the primary hostname. Portals may also require client certificates with `tls.mtls_mode` at portal scope, trusting the organization's signing CA.

## Hostname approval and the blacklist

Hostnames claimed by organizations are checked against instance policy. The primary hostname always passes. A hostname matching `portals.hostname_blacklist` is refused on save, as is a hostname registered to another organization.

Saving a portal with the ACME source registers its hostname as an org scoped certificate domain. With `portals.require_hostname_approval` on, that registration waits for a system admin unless a system admin made the save. The portal can be saved and sit pending, certificate issuance waits. Admin > Portals lists hostnames Waiting for approval with Approve and Deny, `CertificateService.ApproveCertificateDomain` does the same. Admin > Portals also edits the Blocked patterns list.

## Isolation

With `portals.isolated` on at org scope (Org > Settings > Isolate portals) the organization's portals refuse repositories outside the organization namespace. OCI requests get a `NAME_UNKNOWN` 404, git and artifact requests get a plain 404. A rewrite rule whose replacement targets a literal namespace keeps that namespace reachable.

## What a portal serves

- The web UI, scoped to the organization. The OIDC login path bounces through the primary host because SSO state cookies only work there, see [OIDC](oidc.md).
- The OCI registry under `/v2/`. Names pass through the rewrite rules and `mapUnqualified`, writes need `allowPush`, and the bearer realm in `Www-Authenticate` is rewritten to the portal host so token flows complete on any hostname. See [registry](../registry/overview.md).
- The artifact API under `/api/v1/artifacts`. Repository names map like the registry, listings are scoped to the organization, writes need `allowPush`, and anonymous requests get a 401 when the portal denies anonymous access. See [artifacts](../artifacts/repositories.md).
- Git smart http, LFS, and browse paths. Repository names map like the registry, see [cloning](../git/cloning.md).
- With `sshPort` set, git over ssh on that port maps names through the same rules, refuses repositories outside the portal's scope, and rejects pushes unless `allowPush` is on. The port only listens while `git.ssh_portal_ports` is on, an instance admin switches it under Admin > Git > SSH and it is off by default. Turning it off closes portal ssh listeners and keeps the stored ports, saves that set or change one are refused until it is back on.

Anonymous access on a portal follows `auth.anonymous_access` at portal scope, then org scope, then the instance. `requireAuth` on the portal is a hard deny on top.

## Backend portals

A portal with `backendUrl` proxies every request on its address to that service and exposes no registry surface, the mapping and access fields are cleared. The proxy sets `X-Forwarded-For`, `X-Forwarded-Host`, and `X-Forwarded-Proto` from the edge and drops any client supplied values, adds `Via: 1.1 distroface`, and answers 508 when a request already passed through this instance. A verified client certificate common name travels in `X-Client-Cert-Cn`. Backend failures answer 502. Setting or changing the backend requires instance admin rights because it exposes an internal service on the public edge.

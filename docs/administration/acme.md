# ACME client

DistroFace obtains certificates from an external ACME directory such as Let's Encrypt for the primary hostname, portal hostnames, and registered domains. Issued certificates are stored in the database and renewed automatically.

The client is configured under Admin > Network security > ACME client. Settings can be seeded with the `settings:` block or `DISTROFACE_SETTINGS_JSON` and pinned with `overrides:` or `DISTROFACE_OVERRIDES_JSON`, see [configuration](../getting-started/configuration.md).

## Settings

| Setting | Default | Description |
| --- | --- | --- |
| `acme.enabled` | `false` | Automatic issuance. System scope only |
| `acme.email` | empty | ACME account email, required before enabling |
| `acme.directory_url` | empty | Directory URL, empty means Let's Encrypt production |
| `acme.challenge_port` | `80` | Cleartext port for http-01, empty disables http-01. System scope only |
| `acme.redirect_http` | `true` | Challenge listener redirects other requests to https in HTTPS only mode. System scope only |

Organizations and portals may set their own `acme.email` and `acme.directory_url`. Each directory and email pair gets its own ACME account. A hostname uses the portal account, then the owning organization's account, then the system account.

## Challenge types

The CA validates on port 80 (http-01) and port 443 (tls-alpn-01), nothing else. Both ports must reach this instance from the internet, whatever port clients use.

- tls-alpn-01 is always offered. The main listener and every portal listener advertise the `acme-tls/1` protocol, so port 443 must reach the listener serving the hostname.
- http-01 is offered when `acme.challenge_port` is set. A cleartext listener on that port answers `/.well-known/acme-challenge/`. Any other request on that port gets a 403, or a 302 to `https://<host><path>` when `acme.redirect_http` is on and `tls.mode` is `TLS_MODE_HTTPS_ONLY`.

Validation for a portal hostname hits the portal hostname on 80 or 443. A portal on another port therefore relies on the http-01 listener.

## Issuing a certificate from Let's Encrypt

Publish the main port as 443 and the challenge port as 80, keep 8080 for the cleartext setup session.

```bash
docker run -p 80:80 -p 443:8080 -p 8080:8080 -v distroface:/app/data nickheyer/distroface:latest
```

1. Point DNS at the host, open `http://registry.example.com:8080` and sign in as the admin.
2. Admin > Network security: set the public hostname to `registry.example.com`, no port when clients use 443.
3. ACME client: set the account email, leave the directory empty, keep the challenge port at 80, then turn on automatic issuance.
4. Certificate source: pick ACME and press Issue certificate. The first https handshake also issues.
5. Listener mode: HTTPS only once the status reads ready. Redirects start when the certificate is ready, see [TLS](tls.md).

The same deployment as config:

```yaml
settings:
  server: { public_hostname: "registry.example.com" }
  tls: { mode: "TLS_MODE_HTTPS_ONLY", primary_source: "CERT_SOURCE_ACME" }
  acme: { enabled: true, email: "ops@example.com", challenge_port: "80" }
```

A public hostname with a port such as `registry.example.com:8443` works for clients, the CA still needs 80 or 443 to reach the instance.

## Using staging or another directory

Set `acme.directory_url` to any absolute http or https URL. Use `https://acme-staging-v02.api.letsencrypt.org/directory` while wiring ports, failed validations count against the production rate limits. A directory whose hostname equals the public hostname is refused, an instance cannot enroll with its own CA. Serve from the instance CA or an org CA instead, see [certificate authority](certificate-authority.md).

## Refused settings

Saves that cannot work are rejected with the fix in the message.

- `acme.enabled` on without an `acme.email`, or clearing the email while issuance is on.
- `acme.email` that is not an email address.
- `acme.directory_url` that is not an absolute http or https URL, or points at this instance.
- `tls.primary_source` set to ACME while issuance is off, or turning issuance off while ACME is the source.
- `tls.primary_source` set to ACME while the public hostname is `localhost`, an ip, or has no dot.
- `acme.challenge_port` equal to the main listener port, the git ssh port, or the port of an enabled portal.

## Certificate domains and approval

Issuance is limited to hostnames the instance knows about. Random SNI probes never reach the CA.

- The primary hostname, when it is a public DNS name.
- The hostname of a portal whose certificate source is ACME. Saving such a portal registers the hostname as an org scoped domain.
- Domains registered with `CertificateService.AddCertificateDomain`. System admins register system scoped domains, which are always approved. Org admins register org scoped domains for their organization.

With `portals.require_hostname_approval` on, org scoped domains wait for a system admin. The status reads pending with the hint to approve under Admin > Portals, which lists waiting hostnames with Approve and Deny. `CertificateService.ApproveCertificateDomain` does the same. A domain registered by a system admin is approved on creation. Hostnames matching `portals.hostname_blacklist` cannot be registered, and a hostname registered to one organization is refused for another. See [portals](portals.md).

```bash
curl -X POST https://registry.example.com/distroface.v1.CertificateService/AddCertificateDomain \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"domain":"mirror.example.com","orgId":"org_123"}'
```

`CertificateService.ListCertificateDomains` lists registrations with the live certificate details, `pendingOnly` narrows to unapproved ones. Deleting a portal or changing its hostname releases the org's registration when no other portal of the org serves it.

## Issuing on demand

`CertificateService.IssueCertificate` orders a certificate and blocks until the CA answers, at most five minutes. The target is a `domainId`, a `portalId` for the portal's current hostname, or empty for the primary hostname. Org targets are limited to five issuances per organization per hour. The UI buttons Issue certificate and Provision now call this RPC.

```bash
curl -X POST https://registry.example.com/distroface.v1.CertificateService/IssueCertificate \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"portalId":"prt_123"}'
```

The response carries the issuer, validity window, and SANs. A failed order returns the CA's error and the status line keeps it as `issuance failed <age> ago: <reason>` until the next success. The status adds reachability hints, for example when the http-01 listener could not bind, when the challenge port is not 80, or when the public hostname carries a port other than 443.

## Renewal

Certificates renew in two ways.

- A handshake for a hostname whose certificate is within 30 days of expiry triggers a new order.
- A background sweep runs at startup and every 12 hours. It reissues every certificate within 30 days of expiry for the primary hostname when its source is ACME, every enabled portal with the ACME source, and every approved domain. With a shared cache configured one instance holds the `cert-renewal` lease at a time, see [cache](../getting-started/cache.md).

Deferred renewals are logged with the reason. An expired certificate reads as pending with `renewal happens on the next handshake`, or as an error when the last attempt failed.

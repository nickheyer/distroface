# TLS

DistroFace terminates TLS itself. The main listener answers TLS and cleartext on one port and picks the certificate by SNI, so each hostname the instance answers on can have its own certificate source. Nothing is served over TLS until a source resolves, a fresh install stays reachable over plain http until then.

All settings on this page live under Admin > Network security. They can be seeded with the `settings:` block or `DISTROFACE_SETTINGS_JSON` and pinned with `overrides:` or `DISTROFACE_OVERRIDES_JSON`, see [configuration](../getting-started/configuration.md) and [settings](settings.md).

## Settings

| Setting | Default | Description |
| --- | --- | --- |
| `server.public_hostname` | empty | Hostname clients use, with an optional port. The primary certificate is resolved for this name |
| `tls.mode` | `TLS_MODE_DUAL` | Listener mode for the primary hostname, see below |
| `tls.primary_source` | `CERT_SOURCE_CONFIG` | Where the primary hostname's certificate comes from |
| `tls.mtls_mode` | `MTLS_MODE_OFF` | Client certificate policy, portal scope may override |

## Listener modes

The listener accepts both schemes on `server.port` in every mode. The first byte of each connection decides whether it is a TLS handshake or cleartext. A handshake for a hostname no certificate resolves for is answered with a plain http 400 instead of a TLS alert, the same thing a cleartext only port does, so clients that retry insecure registries over http, docker included, fall back instead of failing.

| Mode | Behavior |
| --- | --- |
| `TLS_MODE_DUAL` | Handshakes serve TLS when a certificate resolves, plain http keeps answering |
| `TLS_MODE_HTTPS_ONLY` | Cleartext requests on the primary hostname redirect to https once a certificate is ready |
| `TLS_MODE_CLEARTEXT` | No redirects. A certificate that resolves is still served |

Portals decide their own scheme with their `tls` flag, independent of this mode. See [portals](portals.md).

## Certificate sources for the primary hostname

| Source | Material |
| --- | --- |
| `CERT_SOURCE_CONFIG` | The `tls.cert_file` and `tls.key_file` pair from the config file, loaded at startup |
| `CERT_SOURCE_MANUAL` | A PEM pair uploaded at scope `TLS_SCOPE_APP` |
| `CERT_SOURCE_ACME` | Issued by an external ACME CA, see [ACME](acme.md) |
| `CERT_SOURCE_APP_CA` | Minted on demand from the instance root CA, see [certificate authority](certificate-authority.md) |

Only the configured source is served. There is no fallback between sources, a source that cannot resolve fails the handshake for that hostname. Changing the source applies without a restart.

## Using certificates from the config file

```yaml
tls:
  cert_file: /app/data/server.crt
  key_file: /app/data/server.key
```

The same keys are read from `DISTROFACE_TLS_CERT_FILE` and `DISTROFACE_TLS_KEY_FILE`. The pair is loaded once at startup and a pair that does not parse stops the process. With `tls.primary_source` left at `CERT_SOURCE_CONFIG` and no pair configured, the primary hostname serves cleartext only and the status line says so.

## Uploading a certificate

Admin > Network security > Upload server certificate stores a PEM pair at scope `TLS_SCOPE_APP`. The pair must parse as a key pair. Set `tls.primary_source` to `CERT_SOURCE_MANUAL` to serve it.

```bash
curl -X POST https://registry.example.com/distroface.v1.CertificateService/UploadTLSCertificate \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d "{\"scope\":\"TLS_SCOPE_APP\",\"certPem\":$(jq -Rs . < server.crt),\"keyPem\":$(jq -Rs . < server.key)}"
```

`CertificateService.GetTLSMaterial` with an empty `orgId` reports the stored app certificate and instance CA. Private keys never leave the server. `CertificateService.DeleteTLSCertificate` removes the material. Uploads take effect immediately on every instance.

## How SNI picks a certificate

For every handshake the engine resolves the server name and the local port in this order.

1. A portal matching the port and hostname, then the port's catch-all portal, then a hostname only portal. The portal's `cert_source` decides the material. A catch-all portal never takes over the primary hostname.
2. Otherwise the primary source. Unknown server names and handshakes without SNI also get the primary source.

Portal listeners use the same engine, so a portal on its own port serves TLS and cleartext too.

## Reading the status line

Admin > Network security shows the computed state under the public hostname. The same data comes from `CertificateService.GetCertStatus` with empty ids for the primary hostname, or `orgId` plus `portalId` for a portal.

| State | Meaning |
| --- | --- |
| `CERT_STATE_NONE` | Cleartext, no certificate expected |
| `CERT_STATE_PENDING` | Source configured, certificate not servable yet |
| `CERT_STATE_READY` | Handshakes serve a valid certificate |
| `CERT_STATE_ERROR` | Source configured but handshakes will fail |

`problems` lists the blocker and the next step, for example an expired certificate, uploaded material that does not cover the hostname, a missing CA, or an ACME order that failed with the CA's error text. In HTTPS only mode a line reminds you that redirects start once the certificate is ready.

## HTTPS only redirects

With `tls.mode` at `TLS_MODE_HTTPS_ONLY` a cleartext request to the app gets a 307 to `https://<server.public_hostname><path>`. The redirect is skipped for requests that already arrived over TLS, requests on portal hosts, requests from in process runners, the `/acme/` subtree, and `/.well-known/distroface/ca.pem`, so clients can fetch the trust anchor and enroll before they trust the instance. Enforcement waits until the primary certificate reports `CERT_STATE_READY`, readiness is cached for five seconds.

The http-01 challenge listener redirects too when `acme.redirect_http` is on and the mode is HTTPS only, see [ACME](acme.md).

## Requiring client certificates

`tls.mtls_mode` sets the client certificate policy for the primary hostname. Admin > Network security > Mutual TLS.

| Mode | Handshake |
| --- | --- |
| `MTLS_MODE_OFF` | No client certificate requested |
| `MTLS_MODE_OPTIONAL` | Verified when presented, the identity is attached to the request |
| `MTLS_MODE_REQUIRED` | Handshake fails without a trusted client certificate |

The primary hostname trusts client certificates that chain to the instance root CA. Portal hosts trust their organization's CA and may override the mode with `tls.mtls_mode` at portal scope. When the trust CA is missing the host logs a warning and behaves as off instead of refusing every connection.

A verified identity carries the certificate common name, DNS names, and issuer. The audit trail records `mtls client <common name>` on each event and backend portals receive it in the `X-Client-Cert-Cn` header. Client certificates are issued with `CertificateService.SignCSR`, see [certificate authority](certificate-authority.md).

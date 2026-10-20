# Certificate authority

DistroFace can run a private PKI. An instance root CA signs server certificates for the primary hostname, intermediate CAs for organizations, client certificates, and the certificates handed out by the built in ACME server. Everything chains to one root that clients fetch from the instance.

The root lives under Admin > Certificate authority. Organization CAs live under Org > Certificates.

## Settings

| Setting | Default | Description |
| --- | --- | --- |
| `ca.acme_enabled` | `false` | Serve the built in ACME directory at `/acme/`. Refused until a root exists |
| `tls.primary_source` | `CERT_SOURCE_CONFIG` | Set to `CERT_SOURCE_APP_CA` to serve the primary hostname from the root |

## Creating the instance root

`CertificateService.GenerateAppCA` mints a self signed ECDSA P-256 root valid for ten years with path length one, so it can sign intermediates. The common name defaults to `DistroFace Instance Root CA`. Admin > Certificate authority > Instance CA has the same button plus Upload instance CA, which stores a PEM pair at scope `TLS_SCOPE_APP_CA`. Uploaded material must be a CA certificate. A self signed root passes alone, an intermediate must be bundled with its issuing chain.

```bash
curl -X POST https://registry.example.com/distroface.v1.CertificateService/GenerateAppCA \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" -d '{}'
```

With `tls.primary_source` at `CERT_SOURCE_APP_CA` the primary hostname serves a leaf minted from the root on demand. Leaves last 90 days, capped at the root's expiry, and are re-minted when fewer than seven days remain. Clients must trust the root, see below.

Replacing or deleting the root has consequences for everything signed by it. Generating or uploading a new root re-mints an existing built in ACME issuer and logs every organization whose intermediate no longer chains. Those intermediates read as orphaned in `CertificateService.GetTLSMaterial` until reissued. Deleting the root is refused while any organization holds an intermediate CA.

## Organization CAs

Each organization holds one signing CA at scope `TLS_SCOPE_ORG_CA`. It mints leaves for portals using the `CERT_SOURCE_ORG_CA` source, signs CSRs for the organization, and is the trust anchor for client certificates on the organization's portal hosts. Org > Certificates > Signing CA offers three ways to get one.

| Action | Result |
| --- | --- |
| `CertificateService.IssueOrgICA` | Intermediate signed by the instance root, ten years capped at the root's expiry, root appended to the chain. Needs a root |
| `CertificateService.GenerateOrgCA` | Standalone self signed CA with path length zero, chains to nothing |
| `CertificateService.UploadTLSCertificate` with scope `TLS_SCOPE_ORG_CA` | Imported CA, same bundle rules as the root |

The common name defaults to the organization name. Each action replaces the existing CA. Org admins and users with organization manage rights may do this, the root's private key is never exposed to them.

## Signing a CSR

`CertificateService.SignCSR` signs a PEM certificate request without the private key leaving the client. With `orgId` set the organization's CA signs, with it empty the instance root signs and the caller must be a system admin. Subject names come from the CSR common name, DNS names, and IP addresses. Every DNS name must pass the hostname policy described under Allowed hostnames. `validityDays` defaults to 90 and is capped at the CA's expiry. The leaf carries both server and client authentication usages, so one certificate can serve TLS and authenticate as a client.

```bash
openssl req -new -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
  -keyout runner1.key -out runner1.csr -subj "/CN=runner1.internal"
curl -X POST https://registry.example.com/distroface.v1.CertificateService/SignCSR \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d "{\"orgId\":\"org_123\",\"csrPem\":$(jq -Rs . < runner1.csr),\"validityDays\":90}"
```

The response holds the leaf followed by the issuing chain. Org > Certificates > Sign a CSR does the same in the browser.

## Client certificate identity

A client presenting a certificate signed this way is verified against the instance root on the primary hostname and against the organization CA on that organization's portal hosts, when `tls.mtls_mode` is optional or required. The verified common name is attached to the request, recorded on audit events as `mtls client <common name>`, and forwarded to backend portals as `X-Client-Cert-Cn`. Modes are described in [TLS](tls.md).

## Trusting the root

The instance serves its root at `/.well-known/distroface/ca.pem` as `application/x-pem-file`. The path is public, answers over cleartext even in HTTPS only mode, and returns 404 until a root exists.

```bash
dfcli trust show --server https://registry.example.com
dfcli trust install --server https://registry.example.com
```

`show` prints the PEM. `install` prints the SHA-256 fingerprint for comparison with Admin > Certificate authority, stores the root at `~/.dfcli/ca.pem` so dfcli trusts the instance, and on Linux writes it to `/etc/docker/certs.d/<host>/ca.crt` so `docker pull` trusts the registry without a daemon restart. `--host` overrides the certs.d directory name. On other platforms the file is saved to the temp directory for manual installation. When the https fetch fails because no certificate is served yet, dfcli retries over http. See [CLI](../cli/overview.md).

## Built in ACME server

Internal machines can enroll with the instance over ACME instead of copying certificates around. Turn on `ca.acme_enabled` under Admin > Certificate authority > ACME server. The toggle is refused until a root exists. The directory answers at `https://registry.example.com/acme/directory`, and over cleartext at `http://registry.example.com:8080/acme/directory` because `/acme/` is exempt from https redirects, so clients can enroll before the primary hostname serves TLS. With the toggle off every request under `/acme/` returns 503.

The first issuance mints an intermediate named `distroface acme issuer` from the root at scope `TLS_SCOPE_ACME_CA`. Issued certificates are valid for 90 days and come back as leaf, issuer, root. The server accepts ES256 and RS256 account keys, `dns` identifiers only, and orders expire after one hour. Revocation and key change are listed in the directory but not implemented. With a shared cache configured, in flight orders and nonces live there so a client may reach any instance, see [cache](../getting-started/cache.md). Accounts are stored in the database either way.

Validation reaches the client on port 80 for http-01 or port 443 for tls-alpn-01, so the client must be reachable from the instance on one of those. Targets resolving to loopback, unspecified, link-local, or multicast addresses are refused. The instance and its portals cannot enroll against this directory, use the `CERT_SOURCE_APP_CA` or `CERT_SOURCE_ORG_CA` sources for them.

## Allowed hostnames

Every name issued by the built in server, and every DNS name signed through `SignCSR`, must pass the hostname policy. Allowed names are the primary hostname, hostnames of portals using the ACME source, and registered certificate domains. Add names under Admin > Certificate authority > Allowed hostnames, which registers system scoped domains, or with `CertificateService.AddCertificateDomain`. Anything else fails with `host not allowed`. Blacklist and approval rules from [portals](portals.md) apply to org scoped names.

## Enrolling with lego

```bash
curl -fsSL http://registry.example.com:8080/.well-known/distroface/ca.pem -o distroface-ca.pem
LEGO_CA_CERTIFICATES=distroface-ca.pem lego --server https://registry.example.com/acme/directory \
  --email ops@example.com --domains runner1.internal --http run
```

`runner1.internal` must be listed under Allowed hostnames and reachable from the instance on port 80 for the `--http` challenge.

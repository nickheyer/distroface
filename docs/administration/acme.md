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
| `acme.renew_interval_minutes` | `10` | Minutes between renewal sweeps, at least 1. System scope only |
| `acme.challenge` | unset | Unset lets the CA pick http-01 or tls-alpn-01. `ACME_CHALLENGE_TYPE_HTTP_01`, `ACME_CHALLENGE_TYPE_TLS_ALPN_01` or `ACME_CHALLENGE_TYPE_DNS_01` pins one |
| `acme.dns.provider` | unset | `DNS_PROVIDER_RFC2136` or `DNS_PROVIDER_CLOUDFLARE` |
| `acme.dns.server` | empty | RFC 2136 nameserver that accepts dynamic updates, host or host:port, port 53 when omitted |
| `acme.dns.tsig_key_name` | empty | RFC 2136 TSIG key name |
| `acme.dns.tsig_algorithm` | empty | RFC 2136 TSIG algorithm, empty means `hmac-sha256` |
| `acme.dns.tsig_secret` | empty | RFC 2136 TSIG secret, base64. Write only |
| `acme.dns.api_token` | empty | Cloudflare API token with DNS edit on the zone. Write only |
| `acme.dns.resolvers` | empty | Nameservers that confirm the record before the CA is told to look, empty uses system DNS |
| `acme.dns.propagation_timeout_seconds` | `120` | Longest wait for the record to appear, zero skips the wait |
| `acme.account_key` | empty | Private key of an account already registered at the directory as PEM, base64 DER or JWK. Write only |
| `acme.eab_key_id`, `acme.eab_hmac_key` | empty | External account binding issued by the CA, both or neither. The HMAC key is write only |
| `acme.ca_bundle` | empty | PEM roots trusted for the directory's own certificate, empty uses the system roots |

Write only fields never come back from the API, reads report `account_key_set`, `eab_hmac_key_set`, `dns.tsig_secret_set` and `dns.api_token_set` instead.

Organizations and portals may set their own `acme.email`, `acme.directory_url`, `acme.challenge`, `acme.dns.*`, `acme.account_key`, `acme.eab_*` and `acme.ca_bundle`. A hostname uses the portal values, then the owning organization's, then the system's.

Two groups of fields travel together so credentials are never sent somewhere they were not issued for.

- A tier that sets `acme.directory_url` owns `acme.account_key`, `acme.eab_key_id`, `acme.eab_hmac_key` and `acme.ca_bundle`. Values from higher tiers do not apply to its directory.
- A tier that sets `acme.dns.provider` owns the whole `acme.dns` block. Nothing in it is inherited.

## Challenge types

`acme.challenge` picks how the CA confirms control of a hostname. Left unset, the CA chooses between http-01 and tls-alpn-01. Setting it pins exactly one type.

### http-01 and tls-alpn-01

The CA validates on port 80 (http-01) and port 443 (tls-alpn-01), nothing else. Both ports must reach this instance from wherever the CA runs, whatever port clients use.

- tls-alpn-01 is offered unless `acme.challenge` pins http-01. The main listener and every portal listener advertise the `acme-tls/1` protocol, so port 443 must reach the listener serving the hostname.
- http-01 is offered when `acme.challenge_port` is set and `acme.challenge` does not pin tls-alpn-01. A cleartext listener on that port answers `/.well-known/acme-challenge/`. Any other request on that port gets a 403, or a 302 to `https://<host><path>` when `acme.redirect_http` is on and `tls.mode` is `TLS_MODE_HTTPS_ONLY`.

Validation for a portal hostname hits the portal hostname on 80 or 443. A portal on another port therefore relies on the http-01 listener.

### dns-01

The instance publishes a TXT record at `_acme-challenge.<hostname>` through the DNS provider, waits until the resolvers see it, and removes it after validation. The CA never connects to the instance, so this works behind a firewall, on a network with no inbound path, and for hostnames that do not resolve to the instance at all. It is the only challenge a CA accepts for wildcard certificates.

RFC 2136 is the standard dynamic update protocol and needs no vendor API. BIND, Knot, PowerDNS and Windows DNS accept it with a TSIG key.

```yaml
settings:
  acme:
    enabled: true
    email: "ops@example.com"
    challenge: "ACME_CHALLENGE_TYPE_DNS_01"
    dns:
      provider: "DNS_PROVIDER_RFC2136"
      server: "ns1.corp.example:53"
      tsig_key_name: "distroface"
      tsig_secret: "bXkgdHNpZyBzZWNyZXQ="
      resolvers: ["10.0.0.2:53"]
```

Set `acme.dns.resolvers` to nameservers that can see the zone when the system resolver cannot, for example a split horizon zone or an isolated network.

## Wildcard certificates

Register a wildcard such as `*.eng.example.com` as a certificate domain, under Admin > Certificate authority or with `CertificateService.AddCertificateDomain`. With dns-01 in effect, every ACME hostname one label under it is served from that single wildcard certificate instead of ordering its own. That covers the primary hostname, portal hostnames and registered domains.

- A system scoped wildcard covers hostnames of every organization and the instance.
- An org scoped wildcard covers only that organization's hostnames.
- With `portals.require_hostname_approval` on, the wildcard must be approved first.
- A wildcard covers one label, `*.eng.example.com` serves `a.eng.example.com` and not `b.a.eng.example.com`.

Under http-01 and tls-alpn-01 a wildcard registration is ignored and each hostname orders its own certificate. Ordering the wildcard itself is refused with a message naming dns-01.

## Accounts

Each distinct directory and email gets its own ACME account, registered on first use with a generated key.

To order with an account that already exists, for example one created by another ACME client, set `acme.account_key` to its private key and `acme.directory_url` to the same directory. The key may be PEM, bare base64 DER or a JWK, the forms other clients keep in their state files, and is converted on use. The account is looked up by that key and never registered again. A key the directory does not know fails with `accountDoesNotExist`.

For a CA that requires external account binding, set `acme.eab_key_id` and `acme.eab_hmac_key` to the pair it issued.

The directory URL is the CA's directory document, for example `https://acme-v02.api.letsencrypt.org/directory`. An account URL such as `.../acme/acct/123` is not a directory and fails with a 405.

## Private and isolated directories

Any ACME directory works, including one that is only reachable inside the network. Set `acme.directory_url` to it and, when its certificate is signed by a private root, put that root in `acme.ca_bundle`.

```yaml
settings:
  acme:
    enabled: true
    email: "ops@corp.example"
    directory_url: "https://ca.corp.example/acme/acme/directory"
    ca_bundle: |
      -----BEGIN CERTIFICATE-----
      ...
      -----END CERTIFICATE-----
```

Every challenge type works against a private directory. http-01 and tls-alpn-01 need the CA host to reach the instance on 80 or 443, dns-01 needs the instance to reach the nameserver.

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
- `acme.challenge` set to dns-01 without a provider, RFC 2136 without a nameserver or a base64 TSIG key, Cloudflare without an API token.
- `acme.challenge` set to http-01 while `acme.challenge_port` is empty.
- `acme.renew_interval_minutes` below 1.
- `acme.account_key` that is not a PEM, base64 DER or JWK private key, an `acme.ca_bundle` with no certificate, one half of the external account binding without the other.
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

`CertificateService.IssueCertificate` orders a certificate and blocks until the CA answers, at most five minutes. A hostname covered by a wildcard orders the wildcard. The target is a `domainId`, a `portalId` for the portal's current hostname, or empty for the primary hostname. Org targets are limited to five issuances per organization per hour. The UI buttons Issue certificate and Provision now call this RPC.

```bash
curl -X POST https://registry.example.com/distroface.v1.CertificateService/IssueCertificate \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"portalId":"prt_123"}'
```

The response carries the issuer, validity window, and SANs. A failed order returns the CA's error and the status line keeps it as `issuance failed <age> ago: <reason>` until the next success. Under http-01 and tls-alpn-01 the status adds reachability hints, for example when the http-01 listener could not bind, when the challenge port is not 80, or when the public hostname carries a port other than 443. Under dns-01 it names the TXT record or the wildcard that serves the hostname. Settings that cannot issue at all, such as dns-01 with no provider, are reported before any order is tried.

## Renewal

Certificates renew in two ways.

- A handshake for a hostname whose certificate is inside its renewal window triggers a new order.
- A background sweep runs at startup, after every settings change, and then every `acme.renew_interval_minutes`. It covers the primary hostname when its source is ACME, every enabled portal with the ACME source, and every approved domain. A hostname with a current certificate costs one storage read and no order. A hostname whose order failed is retried at most once an hour, which stays inside the failed validation limits of public CAs. With a shared cache configured one instance holds the `cert-renewal` lease at a time, see [cache](../getting-started/cache.md).

The default of 10 minutes is the check interval of the ACME client library and suits certificates of any lifetime, including ones that live a day or less.

The renewal window follows the certificate rather than a fixed number of days. It opens when the CA's renewal information says so, and otherwise in the last third of the certificate's lifetime. A 90 day certificate renews with about 30 days left and a 24 hour certificate from a private CA renews with about 8 hours left.

Deferred renewals are logged with the reason. The ACME client's own log lines, including orders, challenges and handshakes refused for hostnames nothing configures, go to the application log at info level and above. An expired certificate reads as pending with `renewal happens on the next handshake`, or as an error when the last attempt failed.

Certificates issued before this storage layout are carried over on the first sweep and keep serving without a new order.

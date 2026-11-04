# OIDC

Distroface signs users in through an external OpenID Connect provider, alongside or instead of local accounts. The provider settings live under `auth.oidc` and are edited at Admin > Authentication in the OIDC / SSO section.

## How sign in works

1. The login page calls `AuthService.GetOIDCLoginURL`, which answers `/api/v1/auth/oidc/login`.
2. That route sets a five minute `oidc_state` cookie and redirects to the provider's authorization endpoint.
3. The provider sends the browser back to `/api/v1/auth/oidc/callback`. The server checks the state, exchanges the code, verifies the ID token against the discovered keys, and merges claims from the UserInfo endpoint.
4. The account is found or created, roles and org memberships are synced from claims, and a session is created.
5. The browser lands on `/login#token=...`. The fragment keeps the session token out of server logs.

Provider discovery runs on the first login and again whenever the issuer, client, redirect url, scopes or TLS setting change. Portal hosts bounce through the primary hostname and return to the portal origin after the callback, see [Portals](portals.md).

## Settings

| Setting | Default | Description |
| --- | --- | --- |
| `auth.oidc.enabled` | `false` | Turns the provider on. Sign in also needs `issuer_uri` |
| `auth.oidc.issuer_uri` | empty | Issuer used for discovery, must match the `iss` claim |
| `auth.oidc.client_id` | empty | Client registered at the provider |
| `auth.oidc.client_secret` | empty | Write only, reads return `client_secret_set` |
| `auth.oidc.redirect_url` | empty | Public callback url, `https://host/api/v1/auth/oidc/callback` |
| `auth.oidc.scopes` | `openid profile email` | Requested scopes when the list is empty |
| `auth.oidc.role_claim` | empty | Claim whose values become roles, empty disables role sync |
| `auth.oidc.role_mapping` | empty | Provider value to role name, when empty the value is used as the role name |
| `auth.oidc.group_claim` | `groups` | Claim read for org membership |
| `auth.oidc.group_org_mapping` | empty | Provider group to org name, empty disables org sync |
| `auth.oidc.skip_tls_verify` | `false` | Accepts the provider's certificate without verification |

`auth.local_enabled` may stay on so local accounts and OIDC accounts coexist. `AuthService.GetAuthStatus` reports `oidc_enabled` to clients.

## Enabling a provider

Register a confidential client at the provider with the callback url as its redirect uri, then store the settings. Through the API:

```bash
curl -X POST http://localhost:8080/distroface.v1.SettingsService/UpdateSettings \
  -H 'Authorization: Bearer df_...' -H 'Content-Type: application/json' \
  -d '{
    "scope": {"type": "SETTINGS_SCOPE_TYPE_SYSTEM"},
    "settings": {"auth": {"oidc": {
      "enabled": true,
      "issuerUri": "https://keycloak.example.com/realms/distroface",
      "clientId": "distroface",
      "clientSecret": "...",
      "redirectUrl": "https://registry.example.com/api/v1/auth/oidc/callback",
      "roleClaim": "groups",
      "roleMapping": {"registry-admins": "admin"},
      "groupOrgMapping": {"platform": "platform"}
    }}},
    "updateMask": "auth.oidc.enabled,auth.oidc.issuer_uri,auth.oidc.client_id,auth.oidc.client_secret,auth.oidc.redirect_url,auth.oidc.role_claim,auth.oidc.role_mapping,auth.oidc.group_org_mapping"
  }'
```

The same document fits the `settings:` or `overrides:` block of the config file, and every field is an environment variable such as `DISTROFACE_SETTINGS_AUTH_OIDC_CLIENT_ID`, see [Configuration](../getting-started/configuration.md).

```bash
DISTROFACE_SETTINGS_AUTH_OIDC_ENABLED=true
DISTROFACE_SETTINGS_AUTH_OIDC_ISSUER_URI=https://keycloak.example.com/realms/distroface
DISTROFACE_SETTINGS_AUTH_OIDC_CLIENT_ID=distroface
DISTROFACE_SETTINGS_AUTH_OIDC_CLIENT_SECRET=...
DISTROFACE_SETTINGS_AUTH_OIDC_REDIRECT_URL=https://registry.example.com/api/v1/auth/oidc/callback
DISTROFACE_SETTINGS_AUTH_OIDC_ROLE_CLAIM=groups
```

## Account matching

On each callback the server looks for a user by subject and issuer. When none exists and the token carries an email, a user with that email is linked to the subject, so an existing local account keeps its username, roles and tokens. Otherwise a new account is created with `auth_provider` set to `oidc` and the default roles.

The username comes from the first of `preferred_username`, `name`, `email` that slugs to a valid name, lowercased with separators collapsed. A name owned by another user or org gets a numeric suffix, and a name that slugs to nothing becomes `user-` plus a hash of the subject. Reserved route names are avoided.

Every login updates the email, the last login time and the avatar from the `picture` claim. Deactivated accounts are refused.

OIDC accounts have no local password. They cannot change a password, request a reset, or upload an avatar. For `docker login`, git over HTTP and `dfcli login` they use a personal access token, see [Tokens](tokens.md).

## Mapping roles

With `auth.oidc.role_claim` set, the claim is read as an array or a JSON encoded array. Each value is translated through `role_mapping`, or used as is when the map is empty, and must name a role that exists under Admin > Roles.

Roles granted this way are tagged with the `oidc` source and resynced at every login. A role that disappears from the claim is revoked. Roles assigned by an administrator keep their `local` source and are never touched by the sync, so the default roles given at creation survive.

## Mapping groups to organizations

With `auth.oidc.group_org_mapping` set, the `group_claim` values are matched against the map and the user is added to each named org as a member. The org must already exist, unknown orgs are logged and skipped. A user who is already a member from any source is left alone.

Memberships added by the sync carry the `oidc` source. When the group is gone at the next login the membership is removed, except for a sole owner. Users cannot leave a provider managed membership themselves because it would return at the next sign in. See [Organizations](organizations.md).

## Provider recipes

The `oidc/` directory of the repository holds compose files for three providers. Each one configures the provider and passes the matching `DISTROFACE_SETTINGS_AUTH_*` variables to the app. Run one with `make dev-auth-keycloak`, `make dev-auth-authelia` or `make dev-auth-entra`.

| Directory | Provider | Notes |
| --- | --- | --- |
| `oidc/keycloak` | Keycloak 26 | Imports `config/realm.json` with a `distroface` realm, groups `admin` and `user`, and mappers that put `groups` and `roles` in the token. Admin console on port 8180 |
| `oidc/authelia` | Authelia | `config/configuration.yml` defines a claims policy that places `groups` in the ID token, which the sync needs. Ships a self signed certificate, so the recipe turns on `skip_tls_verify` |
| `oidc/entra` | Microsoft Entra ID | Issuer `https://login.microsoftonline.com/<TENANT_ID>/v2.0`, app roles valued `admin` and `user`, `role_claim` set to `roles` |

All three use `groups` or `roles` as the role claim with values equal to Distroface role names, so no `role_mapping` is needed.

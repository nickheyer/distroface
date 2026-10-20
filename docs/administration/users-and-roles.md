# Users and roles

Accounts sign in with a local password or through OIDC. What an account may do comes from its roles, each a set of resource and action grants, and from org membership and repository collaboration on top. Sign in rules live at Admin > Authentication, accounts at Admin > Users, roles at Admin > Roles.

## First account

While no user exists `AuthService.GetAuthStatus` reports `first_user_setup` and the login page offers registration even when it is disabled. The first account created receives the `admin` role. The `bootstrap:` block of the config file and the `DISTROFACE_BOOTSTRAP_USERNAME`, `DISTROFACE_BOOTSTRAP_PASSWORD`, `DISTROFACE_BOOTSTRAP_EMAIL` and `DISTROFACE_BOOTSTRAP_ROLES` variables create accounts and orgs once at startup, see [Configuration](../getting-started/configuration.md).

When local sign in and OIDC are both off, every caller is treated as `admin`.

## Sign in settings

| Setting | Default | Description |
| --- | --- | --- |
| `auth.local_enabled` | `true` | Password sign in. Off leaves OIDC only |
| `auth.local_allow_registration` | `true` | Open registration. Off still admits invites and the first user |
| `auth.require_email_verification` | `false` | Local accounts with an email must verify it before signing in |
| `auth.anonymous_access` | `false` | Signed out callers act as the `anonymous` role. Org and portal tiers override it per host |
| `auth.session_timeout_seconds` | `86400` | Session lifetime, at least 300 |
| `auth.token_expiry_seconds` | `900` | Registry token lifetime, at least 60, see [Tokens](tokens.md) |
| `auth.oidc.*` | off | See [OIDC](oidc.md) |

## Registration and invites

`AuthService.Register` takes `username`, `email`, `password` and an optional `invite_code` with `invite_pin`. Usernames are 3 to 40 characters of lowercase letters, digits, `_`, `.` and `-`, starting and ending with a letter or digit, and may not be a reserved route name such as `admin`, `api`, `orgs`, `settings` or `git`. Passwords need at least 8 characters. An email is optional but must be unique. New accounts get every role flagged `is_default`, which is `user` out of the box.

Invites admit registration while it is closed. `AuthService.CreateInvite` needs `settings:create` and takes a `description`, `role_ids` that replace the default roles, an optional `pin`, `max_uses`, `expires_in_hours`, and `emails` that receive a link to `/register?invite=<code>`. Admin > Invites lists and deletes them. `AuthService.ValidateInvite` is public so the registration form can check a code.

## Email verification and password reset

With `auth.require_email_verification` on, a login from an unverified local account is refused and a fresh link is mailed. Links last one day. Changing the email clears the verification.

`AuthService.RequestPasswordReset` is public, accepts a username or email, and always answers success. A link lasting one hour is mailed to active local accounts that have an email, at most 5 per hour per client ip and per identifier. `AuthService.ResetPassword` sets the new password and deletes every session of the account. Both flows need mail, see [Mail](mail.md). `AuthService.GetAuthStatus` reports `password_reset_enabled`.

## Managing accounts

Admin > Users is backed by these RPCs, each gated by the `users` resource:

| RPC | Action | Notes |
| --- | --- | --- |
| `UserService.ListUsers` | `read` | Filters on `username`, `email`, `display_name`, `auth_provider` |
| `UserService.AdminCreateUser` | `create` | `username`, `password`, `email`, `display_name`, `role_ids`, `must_change_password` |
| `UserService.AdminUpdateUser` | `update` | `email`, `is_active`, and `role_ids` which replaces the role set when non empty |
| `UserService.AdminDeleteUser` | `delete` | Refuses the caller's own account, deletes the git repositories in the user's namespace |
| `UserService.AdminBulkUpdateUsers` | `update` | `is_active`, `add_role_ids`, `remove_role_ids` for many users |
| `UserService.AdminBulkDeleteUsers` | `delete` | Many users, never the caller |

```bash
curl -X POST http://localhost:8080/distroface.v1.UserService/AdminCreateUser \
  -H 'Authorization: Bearer df_...' -H 'Content-Type: application/json' \
  -d '{"username":"alice","password":"changeme123","email":"alice@example.com","roleIds":["role-user"],"mustChangePassword":true}'
```

A deactivated account cannot sign in and its sessions and tokens stop validating. An account created with `must_change_password` can only call `GetCurrentUser`, `Logout`, `RefreshSession` and `ChangePassword` until the password is changed. `UserService.GetUser` returns the public profile to everyone and the email, roles and status only to the user or holders of `users:read`.

## Sessions

`AuthService.Login` accepts a username or email with the password and returns a session token, an HS256 JWT signed with `auth.jwt_secret` and backed by a sessions row. Clients send it as `Authorization: Bearer` or the `distroface_session` cookie. `AuthService.RefreshSession` rotates it, `AuthService.Logout` ends it.

## Roles

| Role | Description |
| --- | --- |
| `admin` | A wildcard grant on every resource, action and object. The grant cannot be edited, the role cannot be renamed or deleted |
| `user` | Flagged `is_default`, assigned to new accounts. Permissions editable |
| `anonymous` | Used by signed out callers when anonymous access is on. Permissions editable |

The three are system roles and cannot be renamed, described or deleted. `RoleService.CreateRole` adds custom roles with a name, description, `is_default` and permissions. Renaming a custom role moves its assignments and grants, deleting it clears both. `RoleService.AssignRole` and `UnassignRole` change one user's roles, `RoleService.GetPermissionMatrix` returns every role's grants plus the valid actions per resource. All of these need the `roles` resource.

## Permissions

A permission is a resource, an action and an object id. `*` means every object, otherwise the id is `namespace/name` for `repositories`, `artifacts` and `git`, or the org id for `organizations`. `RoleService.ListScopeableObjects` pages the objects of those four resources for the editor.

| Resource | Actions |
| --- | --- |
| `repositories`, `artifacts`, `git` | `read`, `create`, `update`, `delete`, `push`, `pull`, `manage` |
| `users`, `roles`, `settings`, `organizations` | `read`, `create`, `update`, `delete`, `manage` |
| `tokens` | `read`, `create`, `delete`, `manage` |
| `webhooks` | `read`, `create`, `update`, `delete` |

A wildcard grant is a capability that applies to public objects. Private repositories still need a relationship: ownership, org membership, a collaborator or team level, `manage`, or a grant naming that object. `manage` is the administrative override and also unlocks the registry catalog.

Default grants:

| Role | Grants |
| --- | --- |
| `user` | `repositories` read, pull, update, delete. `tokens` read, create, delete. `organizations` read, create. `webhooks` read, create, update, delete. `artifacts` and `git` read, pull, push, create, update, delete |
| `anonymous` | `repositories`, `artifacts`, `git` read and pull |

Every RPC maps to one resource and action, with an object id taken from the request where one applies, see [API overview](../api/overview.md). Org roles and team levels are applied inside the service after that check, see [Organizations](organizations.md).

## Lockout and rate limits

| Setting | Default | Description |
| --- | --- | --- |
| `rate_limit.auth_failure_limit` | `10` | Failed credential attempts per client ip before lockout, `0` disables |
| `rate_limit.auth_failure_window_seconds` | `300` | Sliding window for the count |
| `rate_limit.pull_per_minute` | `0` | Manifest pulls and git fetches per minute per user, `0` unlimited |
| `rate_limit.anon_pull_per_minute` | `0` | The same per client ip for anonymous callers |

Failures are counted on `AuthService.Login`, `AuthService.Register`, `UserService.ChangePassword`, the registry token endpoint, git over HTTP and the artifact v1 API. A locked ip gets `resource_exhausted` from RPCs and `429` with `Retry-After: 60` elsewhere until the window passes. A successful sign in clears the count. Pull limits answer `429` with `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset` and `Retry-After` headers.

Counters are shared across instances through the cache when one is configured, see [Cache](../getting-started/cache.md). The client ip honors `X-Forwarded-For` only from `server.trusted_proxies`.

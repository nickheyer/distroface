# Organizations

An organization is a shared namespace that owns image repositories, artifact repositories and git repositories. It has members with one of three roles, teams that scope access inside the org, and its own settings tier.

## Creating an organization

Any user whose roles grant `organizations:create` may create one. The default `user` role has it. In the UI use Orgs > New, or call `OrganizationService.CreateOrganization`:

```bash
curl -X POST http://localhost:8080/distroface.v1.OrganizationService/CreateOrganization \
  -H 'Authorization: Bearer df_...' -H 'Content-Type: application/json' \
  -d '{"name":"platform","displayName":"Platform Team","description":"Shared images"}'
```

The name follows the username rules and must not collide with a username or a reserved route name, see [Users and roles](users-and-roles.md). The creator becomes the owner and a default team named Members is created.

Organizations can also be seeded from the `bootstrap:` block of the config file with their members, see [Configuration](../getting-started/configuration.md).

## Membership roles

| Role | Allows |
| --- | --- |
| `ORG_ROLE_OWNER` | Everything below, plus deleting the org, transferring ownership and granting the owner role |
| `ORG_ROLE_ADMIN` | Editing the org, adding and removing members, changing member roles, managing teams, org settings and org mail, deleting and managing repositories in the namespace |
| `ORG_ROLE_MEMBER` | Reading and pulling everything in the namespace, pushing images to it, and whatever the member's teams grant on git repositories |

Users whose roles carry `organizations:manage` for the org, such as instance admins, pass every membership check without being a member.

## Managing members

Org > Members lists members and their roles. The RPCs:

| RPC | Rule |
| --- | --- |
| `OrganizationService.AddOrgMember` | Owner or admin. Only an owner can grant `ORG_ROLE_OWNER` |
| `OrganizationService.UpdateOrgMemberRole` | Owner or admin. The last owner cannot be demoted |
| `OrganizationService.RemoveOrgMember` | Owner or admin, or the member leaving. The last owner cannot be removed. A membership created by the identity provider cannot be left, see [OIDC](oidc.md) |
| `OrganizationService.TransferOrgOwnership` | Owner only. The target must already be a member. The caller becomes an admin |

```bash
curl -X POST http://localhost:8080/distroface.v1.OrganizationService/AddOrgMember \
  -H 'Authorization: Bearer df_...' -H 'Content-Type: application/json' \
  -d '{"orgId":"<org id>","userId":"<user id>","role":"ORG_ROLE_MEMBER"}'
```

`OrganizationService.ListOrganizations` returns the orgs the caller belongs to. Callers with `organizations:manage` see every org, anonymous callers see none, and a portal host sees only the org that owns it. `GetOrganization` needs `organizations:read` on the org, which the `user` role has for every org.

## Teams

Teams group members and grant levels on git repositories inside the org. Org > Teams manages them, and `dfcli org team list|view|create|add|remove|add-repo|remove-repo` does the same from the shell, see [CLI reference](../cli/reference.md).

Every org has a default team named Members. Every member belongs to it, it includes every repository, it may create repositories, and it grants write on every unit. It cannot be deleted, renamed, or limited to a subset of repositories, but its units and `can_create_repos` can be changed to tighten what plain membership grants.

| Field | Description |
| --- | --- |
| `name` | Letters, digits, spaces, `.`, `_`, `-`, up to 64 characters |
| `can_create_repos` | Members may create repositories in the org namespace |
| `includes_all_repos` | The team covers every repository, otherwise repositories are added by name |
| `admin_access` | Members are repository admins on the covered repositories |
| `units` | A level per unit: `code`, `issues`, `pulls`, `releases`, `wiki`, `projects`, `packages`, `actions`, `agents` |

Unit levels are `TEAM_UNIT_LEVEL_NONE`, `TEAM_UNIT_LEVEL_READ` and `TEAM_UNIT_LEVEL_WRITE`. Only org members can be added to a team. Team member additions are written to the audit log, see [Audit log](audit.md). How levels combine with collaborators and visibility is described in [Git repositories](../git/repositories.md).

## Organization settings

An org stores its own overrides of a fixed set of settings, resolved above the system tier for everything in its namespace. Org > Settings edits most of them and Org > Email edits the mail relay. Owners, admins and holders of `organizations:manage` may write them. The accepted paths, among them `auth.anonymous_access`, `portals.isolated`, artifact limits and retention, git defaults and quotas, mail, actions and agents policy, are listed in [Settings](settings.md).

Org owned hostnames and certificates are covered in [Portals](portals.md) and [TLS](tls.md).

## Identity provider groups

When `auth.oidc.group_org_mapping` maps provider groups to org names, members are added and removed at each sign in. The org must exist first. See [OIDC](oidc.md).

## Deleting an organization

`OrganizationService.DeleteOrganization` needs the owner role or `organizations:manage`. It deletes every git repository in the namespace with its linked packages, the image repositories and their storage, the org settings row, and the org itself.

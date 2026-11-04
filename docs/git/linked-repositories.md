# Linked repositories

One `namespace/name` is one project. An image repository or an artifact repository named after a git repository links to it, shares its access, moves and dies with it, and shows its matching tags on the releases page.

## How links are made

| Trigger | Rule |
| --- | --- |
| Creating a git repository | Unlinked images named `namespace/name` or `namespace/name/...` and the artifact repository `namespace/name` are adopted when the creator may manage them |
| Creating an image repository through the api | Links when a git repository with the first path segment of the image name exists in the namespace |
| First push of an image through the registry | The auto created image repository links the same way and inherits the git repository's private flag. When no name matches, an `org.opencontainers.image.source` label pointing at a clone url of this instance links it instead |
| Creating an artifact repository | Links when `namespace/name` matches a git repository |
| `GitService.LinkRepository` | Links any package in the same namespace by hand with `kind` `IMAGE` or `ARTIFACT` and `package_name`. Needs admin on the repository and manage on the package, since linking shares it with the repository's collaborators |

A package belongs to one git repository at a time. `GitService.ListLinkedRepositories` lists the links with `linked_by_name` marking the ones the naming rule made, and `UnlinkRepository` removes a link while keeping the package's contents. The page is Repo > Packages, shown while the packages unit is on.

```bash
curl -X POST https://df.example.com/distroface.v1.GitService/LinkRepository \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"namespace":"myteam","name":"app","kind":"LINKED_REPOSITORY_KIND_IMAGE","packageName":"app-worker"}'
```

Registry naming is described in [Registry overview](../registry/overview.md), artifact repositories in [Artifact repositories](../artifacts/repositories.md).

## How access resolves

Access checks for images and artifacts call back into the git access model whenever a package has a source repository. The check computes the caller's level on the repository's packages unit from ownership, org roles, teams, collaborators, and scoped grants, never from public visibility, so a public git repository does not open a private package to everyone. The packages unit must be enabled, owners and admins pass regardless.

| Package action | Needed level on the packages unit |
| --- | --- |
| `read`, `pull` | Read |
| `push`, `create` | Write |
| `update` | Admin |
| `delete`, `manage` | Owner |

The linked path is one source among the package's own rules, so a user who owns the package, belongs to the org, or holds a grant on the package keeps that access whether or not a link exists. Anonymous users never gain access through a link. Deploy keys and deploy tokens scoped to the git repository reach its linked packages with the same read or write they hold on the repository, and the registry token endpoint and the artifact api accept the source repository name as the scope. Deploy keys reach packages only through this path. Public packages still follow their own public rules.

## Releases

`ReleaseService.GetRelease` lists the images of a release: the ones pinned to it and, for every linked image repository, the tag equal to the release tag when the registry has it. The release editor notes that linked images tagged like the release appear automatically and lets you pin others. See [Releases](releases.md).

## Renames and transfers

`GitService.MoveGitRepository` moves name linked packages first: images named `name` or `name/...` in the same namespace, and the artifact repository named `name`. They take the new namespace and name, and a failure restores whatever had already moved before the move fails. Packages linked by hand under other names keep their place and their link. Moving a linked package directly through `RepositoryService.MoveRepository` or `ArtifactService.MoveArtifactRepository` is refused with `follows its source repository, move that instead`.

## Deleting

`GitService.DeleteGitRepository` fails with `repository still has N linked packages, pass delete_linked or unlink them first` while links exist. With `delete_linked` true every linked image repository is deleted from the registry along with its layers and manifests, and every linked artifact repository is deleted with its files, before the git repository goes. `dfcli repo delete name --linked` passes the flag, see the [CLI reference](../cli/reference.md). Unlink the packages first to keep them.

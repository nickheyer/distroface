# Wiki

The wiki is a set of markdown pages kept in a separate git repository next to the code repository. Every edit is a commit, so the history is git history.

## Storage

Pages live in a bare repository at `<namespace>/<name>.wiki.git` under the git root, created on the first write with the `main` branch. The wiki unit must be enabled on the repository, and setting `external_wiki_url` replaces the built in wiki with a link. Reading needs read access on the wiki unit and writing needs write access.

Each page is one `.md` file at the top of the tree. The title maps to the file name by replacing spaces with dashes and appending `.md`, and back again when listing. `Home` sorts first. Files whose name starts with `_` are not pages. Two of them are rendered around every page when present:

| File | Description |
| --- | --- |
| `_Sidebar.md` | Returned as `sidebar` on every page read |
| `_Footer.md` | Returned as `footer` on every page read |

A title is limited to 200 characters and may not contain `..`, a backslash, or start with `/`.

## Cloning

The wiki clones and pushes like any repository, over HTTP at `/<namespace>/<name>.wiki.git` and over SSH when it is on. `ListWikiPages` returns both URLs. Pushes may update branches, tags and notes only. There is no dfcli command for the wiki, use git.

```bash
git clone https://forge.example.com/acme/api.wiki.git
```

## Reading pages

Pages are at Repo > Wiki. `WikiService.ListWikiPages` returns titles with their last commit, filters by `page.query` on the title, and reports `exists` once there is at least one page. `GetWikiPage` returns one page by title, at the tip or at a given `sha`.

```bash
curl -X POST https://forge.example.com/distroface.v1.WikiService/GetWikiPage \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"namespace":"acme","name":"api","title":"Home"}'
```

## Editing pages

Repo > Wiki > New page creates a page and the Edit button on a page updates it. Each write is one commit on the wiki branch, authored by the caller, and it bypasses pull request and signed commit rules.

| RPC | Description |
| --- | --- |
| `WikiService.CreateWikiPage` | `title`, `content`, optional `message` (default `Create <title>`). Refuses when the title exists |
| `WikiService.UpdateWikiPage` | `title` plus `content` and or `new_title` to rename, optional `message` (default `Update <title>`) |
| `WikiService.DeleteWikiPage` | Removes the file with the message `Delete <title>` |

`UpdateWikiPage` accepts `expected_sha`. When the wiki branch moved past that commit the write is refused, which lets the editor detect concurrent edits. An update that changes nothing returns the current page without a commit.

```bash
curl -X POST https://forge.example.com/distroface.v1.WikiService/UpdateWikiPage \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"namespace":"acme","name":"api","title":"Home","content":"# API\n\nWelcome.","message":"Reword intro","expected_sha":"3f2a..."}'
```

## History

Repo > Wiki > page > History lists the commits that touched a page. `WikiService.ListWikiRevisions` pages through them with `page.page_token` and returns each as a `GitCommit`. Pass a commit sha to `GetWikiPage` to read the page as it was at that revision.

## RPC reference

| RPC | Access | Description |
| --- | --- | --- |
| `WikiService.ListWikiPages` | read | Titles, last commits, clone URLs |
| `WikiService.GetWikiPage` | read | One page with sidebar and footer, optionally at a sha |
| `WikiService.CreateWikiPage` | write | Add a page |
| `WikiService.UpdateWikiPage` | write | Change or rename a page |
| `WikiService.DeleteWikiPage` | write | Remove a page |
| `WikiService.ListWikiRevisions` | read | Commit history of one page |

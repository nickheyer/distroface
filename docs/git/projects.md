# Projects

A project is a kanban board of issue and pull request cards arranged in columns. Boards belong to a repository or to an organization, where they can hold issues from every repository in that organization.

## Creating a board

Create one at Repo > Projects > New project or Org > Projects > New project, or with `ProjectService.CreateProject`. Name a repository with `namespace` and `name`, or an organization with `org_id`.

```bash
curl -X POST https://forge.example.com/distroface.v1.ProjectService/CreateProject \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"namespace":"acme","name":"api","title":"Q3 roadmap","template":"PROJECT_TEMPLATE_BASIC_KANBAN","public":false}'
```

| Field | Description |
| --- | --- |
| `title` | Required, up to 500 characters |
| `description` | Free text |
| `template` | Starting columns, see below. Unspecified uses the basic kanban |
| `public` | Visible to everyone who can see the repository or organization. Off by default |

| Template | Columns |
| --- | --- |
| `PROJECT_TEMPLATE_NONE` | No columns, add your own |
| `PROJECT_TEMPLATE_BASIC_KANBAN` | To do, In progress, Done |
| `PROJECT_TEMPLATE_BUG_TRIAGE` | Needs triage, High priority, Low priority, Closed |

The first column of a template is the default column. `UpdateProject` changes title, description and `public`. `CloseProject` closes or reopens a board and `DeleteProject` removes it. `ListProjects` returns boards without cards, filtered by `state` of `open` or `closed`.

## Access

| Scope | Who sees private boards | Who may change boards |
| --- | --- | --- |
| Repository | Collaborators with write on the projects unit | Write on the projects unit, repository not archived |
| Organization | Organization members | Organization owners and admins |

Public boards are visible to anyone who can see the repository or organization. The projects unit must be enabled on a repository for anyone but owners and admins to use its boards. A card is shown only when the viewer can read the issue on its own repository, so an organization board hides cards from repositories the viewer cannot see.

## Columns

| RPC | Description |
| --- | --- |
| `ProjectService.CreateColumn` | Adds a column at the end with `title` and `color`. The first column on a board becomes the default |
| `ProjectService.UpdateColumn` | Changes `title`, `color` or `is_default` |
| `ProjectService.MoveColumn` | Moves a column to `position` |
| `ProjectService.DeleteColumn` | Removes a column and moves its cards to the default column, or the first remaining one. A board keeps at least one column |

## Cards

A card is an issue or pull request placed in one column. An issue sits on at most one board.

| RPC | Description |
| --- | --- |
| `ProjectService.AddIssueToProject` | Places `issue_id` at the bottom of `column_id`, or of the default column when empty. The issue must belong to the board's repository, or to a repository of the board's organization |
| `ProjectService.MoveIssue` | Moves a card to `column_id` at `position` |
| `ProjectService.RemoveIssueFromProject` | Takes the card off the board |
| `ProjectService.GetProject` | Returns the board with columns and their cards in order |

Each placement writes a `project_added`, `project_moved` or `project_removed` event on the issue timeline. `IssueService.CreateIssue` accepts `project_id` to place a new issue on a board in one step, and `ListIssues` filters by `project_id`. An issue reports its `project_id` and `project_column_id` when the viewer can see the board.

```bash
curl -X POST https://forge.example.com/distroface.v1.ProjectService/MoveIssue \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"project_id":"<board id>","issue_id":"<issue id>","column_id":"<column id>","position":0}'
```

## RPC reference

| RPC | Description |
| --- | --- |
| `ProjectService.CreateProject` | Create a board for a repository or organization |
| `ProjectService.GetProject` | Board with columns and cards |
| `ProjectService.ListProjects` | Boards of a scope without cards |
| `ProjectService.UpdateProject` | Title, description, public |
| `ProjectService.CloseProject` | Close or reopen |
| `ProjectService.DeleteProject` | Remove a board |
| `ProjectService.CreateColumn`, `UpdateColumn`, `DeleteColumn`, `MoveColumn` | Column management |
| `ProjectService.AddIssueToProject`, `MoveIssue`, `RemoveIssueFromProject` | Card management |

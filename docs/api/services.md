# Services

Every method is an HTTP POST to `/distroface.v1.<Service>/<Method>` with a JSON body, so any HTTP client works. The interactive reference with the OpenAPI document download is at `/docs/api` in the UI. See [API overview](overview.md) for authentication, pagination and errors.

Methods marked `(public)` need no credentials. Methods marked `(stream)` return a server stream instead of a single response.

## ActionsService

Manages workflows, runs, logs, secrets, variables, and runners. Documented in [Actions overview](../actions/overview.md) and [Runners](../actions/runners.md).

| Method | Description |
| --- | --- |
| ListWorkflows | Returns the workflow files of the default branch |
| GetWorkflow | Returns one workflow file with its parse result |
| EnableWorkflow | Allows a workflow to run |
| DisableWorkflow | Stops a workflow from running |
| DispatchWorkflow | Triggers a workflow_dispatch run |
| ListWorkflowTemplates | Lists the starter workflows the instance offers |
| GetWorkflowTemplate | Renders one starter workflow for a repository |
| ListWorkflowRuns | Pages runs |
| GetWorkflowRun | Returns one run with its jobs |
| CancelRun | Cancels a run and its jobs |
| RerunRun | Reruns failed jobs or every job |
| ApproveRun | Approves a run from a fork pull request |
| GetJob | Returns one job with its steps |
| StreamJobLog (stream) | Streams log lines until the job finishes |
| ListRunArtifacts | Returns the artifacts a run uploaded |
| ListRunImages | Returns the container image tags a run pushed to this instance |
| ListSecrets | Returns secret names of a scope |
| SetSecret | Creates or replaces a secret |
| DeleteSecret | Removes a secret |
| ListVariables | Returns variables of a scope |
| SetVariable | Creates or replaces a variable |
| DeleteVariable | Removes a variable |
| ListRunners | Returns runners of a scope |
| CreateRunnerToken | Mints a one time registration token for a scope |
| DeleteRunner | Removes a runner |
| UpdateRunner | Changes labels or name |

## ActivityService

Lists the event stream behind the home feed and profile pages.

| Method | Description |
| --- | --- |
| ListActivity | Pages events of one feed, newest first |

## AgentsService

Manages inference connectors, agent definitions, and agent sessions at system, org, or repo scope. Documented in [Agents overview](../agents/overview.md).

| Method | Description |
| --- | --- |
| ListAgentProviders | Returns inference connectors of a scope |
| GetAgentProvider | Returns one connector with its secret material redacted |
| CreateAgentProvider | Registers a connector |
| UpdateAgentProvider | Changes a connector, an absent api key keeps the stored one |
| DeleteAgentProvider | Removes a connector, agents bound to it fall back to the scope default |
| TestAgentProvider | Sends a short completion and lists models through a saved or unsaved connector |
| ListAgentProviderModels | Returns the models a connector serves |
| ListAgents | Returns agent definitions of a scope |
| GetAgent | Returns one agent definition |
| CreateAgent | Defines an agent and provisions its bot identity |
| UpdateAgent | Changes an agent definition |
| DeleteAgent | Removes an agent definition, its sessions stay readable |
| SetAgentAvatar | Stores an uploaded picture for the agent bot account |
| RemoveAgentAvatar | Drops the uploaded picture, the definition url applies again |
| GetRepoAgents | Returns what the agents tab of a repository needs, usable agents and limits |
| StartAgentSession | Queues a session for an agent against a repository target |
| ListAgentSessions | Pages sessions of a repository |
| GetAgentSession | Returns one session |
| CancelAgentSession | Stops a queued, running, or waiting session |
| RerunAgentSession | Starts a fresh session with the same inputs |
| ContinueAgentSession | Appends an instruction to a finished session and resumes it |
| ResolveAgentApproval | Approves or rejects a gated action, resuming the session |
| ListAgentSessionEvents | Pages the transcript of a session |
| StreamAgentSessionEvents (stream) | Streams transcript events until the session finishes |
| DeleteAgentSession | Removes a finished session and its transcript |
| GetAgentUsage | Aggregates sessions and tokens for a scope |

## ArtifactService

Manages artifact repositories, uploads, and search. Data transfer itself is plain HTTP under `/api/v1/artifacts`. Documented in [Artifact repositories](../artifacts/repositories.md).

| Method | Description |
| --- | --- |
| CreateArtifactRepository | Creates a new artifact repository |
| GetArtifactRepository | Returns a single artifact repository by name |
| ListArtifactRepositories | Returns artifact repositories visible to the caller |
| UpdateArtifactRepository | Modifies repository description or visibility |
| DeleteArtifactRepository | Removes a repository, its artifacts, and unreferenced blobs |
| SyncArtifactRepository | Starts an immediate mirror sync in the background |
| StopArtifactRepositorySync | Cancels the running mirror sync, if any |
| MoveArtifactRepository | Renames a repository or transfers it to another namespace |
| InitiateArtifactUpload | Starts a chunked upload session and returns its ID |
| CompleteArtifactUpload | Finalizes an upload session into a stored artifact |
| GetArtifact | Returns a single artifact by ID |
| ListArtifacts | Returns artifacts in a repository, newest first |
| ListArtifactVersions | Returns artifacts grouped by version |
| SearchArtifacts | Searches artifacts by name, version, path, or properties |
| UpdateArtifact | Renames or moves an artifact or replaces its metadata JSON |
| SetArtifactProperties | Replaces the full property set of an artifact |
| DeleteArtifact | Removes an artifact and its blob when unreferenced |
| StarArtifactRepository | Stars a repository for the current user |
| UnstarArtifactRepository | Removes the current user's star |
| ListStarredArtifactRepositories | Returns repositories a user starred, newest star first |

## AuditService

Read access to the security audit trail, admin only. Documented in [Audit](../administration/audit.md).

| Method | Description |
| --- | --- |
| ListAuditEvents | Lists audit events with optional filters and time ordering |

## AuthService

Handles user authentication and session management. Documented in [Users and roles](../administration/users-and-roles.md).

| Method | Description |
| --- | --- |
| Register (public) | Creates a new user account |
| Login (public) | Authenticates a user and returns a session token |
| Logout | Invalidates the current session |
| GetCurrentUser | Returns the currently authenticated user |
| RefreshSession | Issues a fresh session token and invalidates the current one |
| GetAuthStatus (public) | Returns which auth methods are available |
| GetOIDCLoginURL (public) | Returns the OIDC provider redirect URL |
| CreateInvite | Creates a registration invite code (admin) |
| ListInvites | Returns all registration invites (admin) |
| GetInvite | Returns a single invite by ID (admin) |
| DeleteInvite | Deletes a registration invite (admin) |
| BulkDeleteInvites | Deletes many registration invites (admin) |
| ValidateInvite (public) | Checks if an invite code is valid |
| RequestPasswordReset (public) | Mails a reset link, always answers success |
| ResetPassword (public) | Sets a new password from a mailed token |
| RequestEmailVerification | Mails a verification link to the current user |
| VerifyEmail (public) | Confirms an address from a mailed token |

## CertificateService

Manages TLS certificate domains and ACME issuance. System admins manage system scope, org admins manage domains matching their portal hostnames. Documented in [TLS](../administration/tls.md).

| Method | Description |
| --- | --- |
| ListCertificateDomains | Lists registered domains, org scoped when org_id is set |
| AddCertificateDomain | Registers a domain as allowed to receive certificates |
| RemoveCertificateDomain | Removes a registered domain |
| BulkRemoveCertificateDomains | Removes multiple registered domains in one call |
| ApproveCertificateDomain | Marks an org scoped domain as allowed to issue, system admin only |
| IssueCertificate | Requests immediate issuance for a target, blocks until done |
| UploadTLSCertificate | Uploads PEM TLS material for the app, an org, an org CA, or a portal |
| DeleteTLSCertificate | Removes uploaded TLS material |
| GetTLSMaterial | Reports uploaded TLS material for the app, an org, or a portal |
| GenerateOrgCA | Mints a fresh standalone signing CA for an org, replacing any existing one |
| GenerateAppCA | Mints the instance root CA, system admin only |
| IssueOrgICA | Signs an org intermediate from the instance root, replacing any org CA |
| GetCertStatus | Resolved certificate state for the app or a portal, with problems |
| SignCSR | Signs a PEM CSR with the org CA, or the app CA when org_id is empty |

## GCService

Triggers and reports registry garbage collection. Documented in [Garbage collection](../registry/garbage-collection.md).

| Method | Description |
| --- | --- |
| RunGC | Starts a garbage collection run in the background (admin) |
| GetGCStatus | Returns whether GC is running and the last run's result (admin) |
| GetStorageUsage | Registry and artifact disk usage broken down per namespace and repo |

## GitFileService

Reads trees, blobs, commits, diffs, and blame, and writes files from the web. Documented in [Code](../git/code.md).

| Method | Description |
| --- | --- |
| GetTree | Lists one directory of a tree |
| GetBlob | Returns one file with content inline up to one megabyte |
| GetReadme | Finds and returns the readme for a directory |
| ListCommits | Pages the history of a ref, optionally filtered by path or author |
| GetCommit | Returns one commit with stats, verification, and statuses |
| GetCommitDiff | Pages the files one commit changed against a parent, hunks excluded |
| CompareRefs | Returns the shas, merge base, distance, and change totals between two refs |
| ListCompareCommits | Pages the commits head adds on top of base, oldest first |
| GetCompareDiff | Pages the files changed between two refs, hunks excluded |
| GetDiffFile | Returns the hunks of one file between two refs |
| Blame | Attributes each line of a file to a commit |
| SearchCode | Greps one ref of a repository |
| UpdateFiles | Commits a batch of file operations to a branch |
| RevertCommit | Commits the inverse of a commit onto a branch |
| CherryPickCommit | Applies a commit onto a branch |
| GetArchiveURL | Returns the download url for a ref archive |
| GetTemplates | Returns issue and pull request templates and codeowners presence |
| CreateCommitStatus | Records an external check result on a commit |
| ListCommitStatuses | Returns every status recorded on a commit |
| GetCombinedStatus | Folds the latest status per context into one state |

## GitRefService

Manages branches, tags, and their protection rules. Documented in [Branches](../git/branches.md).

| Method | Description |
| --- | --- |
| ListBranches | Returns branches with their tip commit and distance from the default branch |
| GetBranch | Returns one branch |
| CreateBranch | Creates a branch from a ref or sha |
| DeleteBranch | Deletes a branch |
| RenameBranch | Renames a branch, retargeting pull requests and the default branch |
| ListGitTags | Returns tags in natural version order, newest first |
| GetTag | Returns one tag |
| CreateTag | Creates a lightweight or annotated tag |
| DeleteTag | Deletes a tag |
| SetBranchProtection | Creates or replaces a protection rule |
| ListBranchProtections | Returns the protection rules of a repository |
| DeleteBranchProtection | Removes a protection rule |
| SetTagProtection | Creates or replaces a tag protection rule |
| ListTagProtections | Returns the tag protection rules of a repository |
| DeleteTagProtection | Removes a tag protection rule |

## GitService

Manages git repositories, their settings, links, stars, and collaborators. Clone, fetch, and push themselves are plain git over HTTPS and SSH. Documented in [Repositories](../git/repositories.md).

| Method | Description |
| --- | --- |
| CreateGitRepository | Creates an empty, initialized, mirrored, or forked repository |
| GetGitRepository | Returns one repository with the caller's access per unit |
| ListGitRepositories | Returns repositories visible to the caller |
| SearchGitRepositories | Searches name, description, and topics across visible repositories |
| UpdateGitRepository | Changes metadata, units, merge settings, and mirror configuration |
| DeleteGitRepository | Removes the repository and optionally its linked packages |
| MoveGitRepository | Renames or transfers a repository, linked packages follow |
| AcceptTransfer | Completes a pending transfer into the caller's namespace |
| RejectTransfer | Cancels a pending transfer |
| ArchiveGitRepository | Makes a repository read only |
| UnarchiveGitRepository | Restores writes |
| ForkGitRepository | Copies a repository into another namespace |
| SyncFork | Fast forwards the fork's default branch from its parent |
| ListForks | Returns the forks of a repository |
| UseTemplate | Creates a repository from a template repository |
| SetTopics | Replaces the topic list |
| StarGitRepository | Stars a repository for the current user |
| UnstarGitRepository | Removes the current user's star |
| ListStarredGitRepositories | Returns the current user's starred repositories |
| WatchGitRepository | Sets the caller's watch mode |
| SyncGitRepository | Starts an immediate mirror sync in the background |
| StopGitRepositorySync | Cancels a running mirror sync or import |
| DismissGitImport | Stops a running import and forgets its record |
| GenerateMirrorKey | Creates the ssh key pair used for ssh upstreams |
| DetachMirror | Stops following the upstream and makes the repository standard |
| ListPushMirrors | Returns the push mirrors of a repository |
| CreatePushMirror | Adds a remote the repository pushes to |
| UpdatePushMirror | Changes a push mirror |
| DeletePushMirror | Removes a push mirror |
| SyncPushMirror | Pushes to one mirror now |
| ListLinkedRepositories | Returns the image and artifact repositories linked to a repository |
| LinkRepository | Links an image or artifact repository in the same namespace |
| UnlinkRepository | Removes a package link |
| ListCollaborators | Returns per repository collaborators |
| AddCollaborator | Invites a user with a permission level |
| UpdateCollaborator | Changes a collaborator's permission |
| RemoveCollaborator | Removes a collaborator |
| AcceptCollaboration | Accepts a pending collaborator invitation |
| GetRepositoryActivity | Returns weekly commit counts for the last year |
| GetRepositoryLanguages | Returns bytes per detected language on the default branch |
| ListContributors | Aggregates commits by author |
| RunMaintenance | Repacks and prunes one repository now |
| ListTransfers | Returns pending transfers addressed to the caller |

## HealthService

Provides application health checking. Documented in [Health](../administration/health.md).

| Method | Description |
| --- | --- |
| HealthCheck (public) | Returns the current health status of the application |

## IssueService

Manages issues, comments, labels, milestones, reactions, dependencies, and time tracking. Pull requests share the issue number sequence and the comment timeline. Documented in [Issues](../git/issues.md).

| Method | Description |
| --- | --- |
| CreateIssue | Opens an issue |
| GetIssue | Returns one issue |
| ListIssues | Pages issues or pull requests of a repository |
| SearchIssues | Searches across every repository the caller can read |
| UpdateIssue | Changes the set fields |
| BulkUpdateIssues | Applies the same change to many issues |
| LockIssue | Locks the conversation |
| UnlockIssue | Unlocks the conversation |
| PinIssue | Pins an issue to the top of the list |
| UnpinIssue | Removes a pin |
| DeleteIssue | Removes an issue and its comments |
| CreateComment | Adds a comment |
| UpdateComment | Edits a comment |
| DeleteComment | Removes a comment |
| ListComments | Pages the timeline, comments and events |
| AddReaction | Reacts to an issue or comment |
| RemoveReaction | Removes the caller's reaction |
| ListLabels | Returns repository labels plus inherited organization labels |
| CreateLabel | Adds a repository or organization label |
| UpdateLabel | Changes a label |
| DeleteLabel | Removes a label from every issue |
| ListMilestones | Pages milestones |
| CreateMilestone | Adds a milestone |
| UpdateMilestone | Changes a milestone |
| DeleteMilestone | Removes a milestone, issues keep no milestone |
| AddDependency | Marks an issue as blocked by another |
| RemoveDependency | Removes a blocker |
| ListDependencies | Returns blockers and blocked issues |
| AddTrackedTime | Records time spent |
| DeleteTrackedTime | Removes a time entry |
| ListTrackedTime | Returns time entries of an issue |
| SubscribeIssue | Subscribes the caller to notifications for an issue |
| UnsubscribeIssue | Removes the caller's subscription |

## KeyService

Manages SSH keys, deploy keys, and GPG keys. Documented in [Cloning](../git/cloning.md).

| Method | Description |
| --- | --- |
| AddSSHKey | Registers a user key or a repository deploy key |
| ListSSHKeys | Lists the caller's keys or a repository's deploy keys |
| DeleteSSHKey | Removes a key |
| AddGPGKey | Registers an armored public key |
| ListGPGKeys | Lists the caller's GPG keys |
| DeleteGPGKey | Removes a GPG key |
| VerifyGPGKey | Proves ownership by signing a challenge |

## MailService

Tests and inspects outbound mail for a settings scope. Documented in [Mail](../administration/mail.md).

| Method | Description |
| --- | --- |
| SendTestEmail | Sends a message with the effective settings of a scope |
| ListMailDeliveries | Pages queued and sent messages for a scope |
| RetryMailDelivery | Requeues a failed message |

## MirrorService

Streams live mirror sync activity. Documented in [Mirrors](../mirrors.md).

| Method | Description |
| --- | --- |
| WatchSyncs (stream) | Pushes sync lifecycle events until the client disconnects |

## NotificationService

Serves the caller's inbox and preferences. Documented in [Notifications](../git/notifications.md).

| Method | Description |
| --- | --- |
| ListNotifications | Pages the inbox |
| GetUnreadCount | Returns the unread total |
| MarkRead | Marks notifications read or unread |
| MarkAllRead | Marks every unread notification read, optionally for one repository |
| PinNotification | Pins or unpins a notification |
| WatchNotifications (stream) | Streams unread count changes until the client disconnects |
| GetNotificationPrefs (public) | Returns the caller's web and email preferences |
| UpdateNotificationPrefs (public) | Changes the caller's preferences |

## OrganizationService

Manages organizations and team membership. Documented in [Organizations](../administration/organizations.md).

| Method | Description |
| --- | --- |
| CreateOrganization | Creates a new organization |
| GetOrganization | Returns an organization by name |
| ListOrganizations | Returns all organizations |
| UpdateOrganization | Updates an organization |
| DeleteOrganization | Deletes an organization |
| ListOrgMembers | Returns members of an organization |
| AddOrgMember | Adds a user to an organization |
| RemoveOrgMember | Removes a user from an organization |
| UpdateOrgMemberRole | Changes a member's role in an organization |
| TransferOrgOwnership | Makes another member the owner and demotes the caller to admin |
| ListTeams | Returns the organization's teams |
| GetTeam | Returns one team with its permissions |
| CreateTeam | Adds a team to an organization |
| UpdateTeam | Changes a team's flags and unit permissions |
| DeleteTeam | Removes a team, the default team cannot be deleted |
| ListTeamMembers | Returns the members of a team |
| AddTeamMember | Adds an org member to a team |
| RemoveTeamMember | Removes a user from a team |
| ListTeamRepos | Returns the repositories a team includes |
| AddTeamRepo | Includes a repository in a team |
| RemoveTeamRepo | Drops a repository from a team |

## PortalService

Manages per org registry portals, proxied alias hostnames with URL mapping rules. Documented in [Portals](../administration/portals.md).

| Method | Description |
| --- | --- |
| CreatePortal | Creates a registry portal for an organization |
| ResolvePortal (public) | Resolves the portal serving the requesting host, empty off portal hosts |
| GetPortal | Returns a single portal by id |
| ListPortals | Returns all portals belonging to an organization |
| UpdatePortal | Updates a portal's hostname, mapping rules, or access options |
| DeletePortal | Removes a portal |

## ProjectService

Manages kanban boards on repositories and organizations. Documented in [Projects](../git/projects.md).

| Method | Description |
| --- | --- |
| CreateProject | Creates a board for a repository or an organization |
| GetProject | Returns a board with its columns and cards |
| ListProjects | Pages boards |
| UpdateProject | Changes title and description |
| DeleteProject | Removes a board |
| CloseProject | Closes or reopens a board |
| CreateColumn | Adds a column |
| UpdateColumn | Renames or recolors a column |
| DeleteColumn | Removes a column, moving its cards to the default column |
| MoveColumn | Reorders columns |
| AddIssueToProject | Places an issue on the board |
| MoveIssue | Moves a card between columns or within a column |
| RemoveIssueFromProject | Takes an issue off the board |

## PullRequestService

Manages pull requests, reviews, and merges. Documented in [Pull requests](../git/pull-requests.md).

| Method | Description |
| --- | --- |
| CreatePullRequest | Opens a pull request between two branches |
| GetPullRequest | Returns one pull request |
| ListPullRequests | Pages pull requests |
| UpdatePullRequest | Changes title, body, base, draft, or maintainer edits |
| GetPullRequestDiff | Pages the files changed by the pull request, hunks excluded |
| GetPullRequestFile | Returns the hunks of one changed file |
| ListPullRequestCommits | Lists the commits a pull request adds |
| GetMergeability | Computes whether the pull request can merge and why not |
| MergePullRequest | Merges with a method |
| ScheduleAutoMerge | Merges automatically once every rule passes |
| CancelAutoMerge | Disarms a scheduled merge |
| ClosePullRequest | Closes without merging |
| ReopenPullRequest | Reopens a closed pull request |
| UpdateBranch | Brings the head branch up to date with the base |
| RequestReviewers | Asks users or teams for review |
| RemoveReviewers | Withdraws review requests |
| CreateReview | Starts a pending review or submits one with comments |
| SubmitReview | Submits a pending review with a state |
| DismissReview | Removes the weight of a review |
| ListReviews | Returns reviews on a pull request |
| CreateReviewComment | Adds an inline comment, to a pending review or standalone |
| UpdateReviewComment | Edits an inline comment |
| DeleteReviewComment | Removes an inline comment |
| ListReviewComments | Returns inline comments grouped by thread |
| ResolveReviewThread | Resolves or unresolves a thread |
| ApplySuggestion | Commits a suggestion block to the head branch |
| RestoreHeadBranch | Recreates a deleted head branch from the pull request ref |

## ReleaseService

Manages releases, their assets, and their images. Asset files upload by streaming to `POST /<ns>/<name>/-/releases/<tag>/assets?name=<file>`. Documented in [Releases](../git/releases.md).

| Method | Description |
| --- | --- |
| CreateRelease | Creates a release for a tag, creating the tag when it does not exist |
| GetRelease | Returns one release with assets and images |
| ListReleases | Pages releases newest first |
| UpdateRelease | Changes notes and flags |
| DeleteRelease | Removes a release and optionally its tag |
| PublishRelease | Turns a draft into a published release |
| UpdateReleaseAsset | Renames or relabels an asset |
| DeleteReleaseAsset | Removes an asset and its file |
| AttachReleaseImage | Pins an image reference to a release |
| DetachReleaseImage | Removes a pinned image |
| GetLatestRelease | Returns the newest published non prerelease |
| GenerateReleaseNotes | Drafts notes from commits and merged pull requests since the previous tag |

## RepositoryService

Manages container image repositories. Documented in [Registry overview](../registry/overview.md).

| Method | Description |
| --- | --- |
| CreateRepository | Creates a repository ahead of any push, required for mirrors |
| SyncRepository | Starts an immediate mirror sync in the background |
| StopRepositorySync | Cancels the running mirror sync, if any |
| GetRepository | Returns a repository by namespace and name |
| ListRepositories | Returns a paginated list of repositories |
| DeleteRepository | Deletes a repository |
| ListTags | Returns a paginated list of tags for a repository |
| ResolveTag | Resolves a tag name to its descriptor with children populated |
| UpdateRepository | Updates a repository's metadata |
| StarRepository | Stars a repository for the current user |
| UnstarRepository | Removes the current user's star from a repository |
| ListStarredRepositories | Returns the current user's starred repositories |
| MoveRepository | Renames a repository or transfers it to another namespace |

## RoleService

Manages roles and permissions. Documented in [Users and roles](../administration/users-and-roles.md).

| Method | Description |
| --- | --- |
| ListRoles | Returns all roles |
| GetRole | Returns a single role |
| CreateRole | Creates a new custom role |
| UpdateRole | Updates a role |
| DeleteRole | Deletes a custom role |
| GetPermissionMatrix | Returns permissions for all roles |
| ListScopeableObjects | Pages objects usable in per object permissions |
| UpdatePermissions | Sets permissions for a role |
| AssignRole | Assigns a role to a user |
| UnassignRole | Removes a role from a user |
| GetUserRoles | Returns roles for a user |

## RunnerService

Spoken by `dfcli runner`, authenticated by the runner token header rather than a user session. Documented in [Actions overview](../actions/overview.md) and [Runners](../actions/runners.md).

| Method | Description |
| --- | --- |
| RegisterRunner (public) | Exchanges a registration token for a runner identity |
| Heartbeat (public) | Marks the runner online |
| FetchTask (public) | Long polls for a job matching the runner's labels |
| UpdateTask (public) | Reports step states and outputs |
| AppendTaskLog (public) | Appends log bytes |
| CompleteTask (public) | Reports the final conclusion |
| FetchExec (public) | Long polls for the next command of an interactive job |
| CompleteExec (public) | Reports the outcome of one command of an interactive job |

## SettingsService

Runtime settings stored per scope, resolved repository over portal over org over system over config file over defaults, where field presence drives inheritance. Documented in [Settings](../administration/settings.md).

| Method | Description |
| --- | --- |
| GetSettings | Stored values at one scope plus file locked field paths |
| UpdateSettings | Field masked patch of one scope, applied live |
| GetEffectiveSettings (public) | Fully resolved values for a scope with per field provenance, signed out callers get the redacted public subset |
| ProbeGitSSHPort | Checks a git ssh port binds on this host before it is saved |
| GetGitServerInfo | Host keys, signing key, storage path and usage for the admin git page |

## TokenService

Manages personal access tokens. Documented in [Tokens](../administration/tokens.md).

| Method | Description |
| --- | --- |
| CreateAPIToken | Creates a new personal access token |
| ListAPITokens | Lists the current user's API tokens |
| DeleteAPIToken | Revokes a personal access token |

## UserService

Manages user profiles. Documented in [Users and roles](../administration/users-and-roles.md).

| Method | Description |
| --- | --- |
| GetUser | Returns a user by username |
| UpdateUser | Updates the current user's profile |
| ChangePassword | Changes the current user's password |
| ListUsers | Returns all users (admin) |
| SearchUsers | Public profile subset of active accounts for member pickers |
| AdminUpdateUser | Updates a user's status and roles (admin) |
| AdminDeleteUser | Deletes a user (admin) |
| AdminCreateUser | Creates a local user with a password directly (admin) |
| AdminBulkUpdateUsers | Updates status and roles on many users (admin) |
| AdminBulkDeleteUsers | Deletes many users (admin) |
| SetAvatar | Stores an uploaded profile image for the current local user |
| RemoveAvatar | Drops the uploaded profile image of the current user |

## WebhookService

Manages repository and organization webhooks. Documented in [Webhooks](../webhooks.md).

| Method | Description |
| --- | --- |
| CreateWebhook | Registers a new webhook |
| ListWebhooks | Returns webhooks for a repo or org |
| GetWebhook | Returns a single webhook by ID |
| UpdateWebhook | Modifies an existing webhook |
| DeleteWebhook | Removes a webhook |
| ListWebhookDeliveries | Returns delivery history for a webhook |
| RedeliverWebhook | Re-sends a past delivery |

## WikiService

Edits the markdown pages of a repository wiki. Every write is a commit in the wiki repository. Documented in [Wiki](../git/wiki.md).

| Method | Description |
| --- | --- |
| ListWikiPages | Returns every page title |
| GetWikiPage | Returns one page |
| CreateWikiPage | Adds a page |
| UpdateWikiPage | Changes or renames a page |
| DeleteWikiPage | Removes a page |
| ListWikiRevisions | Pages the history of one page |

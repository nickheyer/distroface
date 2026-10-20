# Agents

Agents are model driven reviewers, coders, assistants and triage bots that act on a repository through an inference connector. Each agent runs under its own bot account and every run is a session with a stored transcript.

## How agents fit together

- A connector is an endpoint and credential the server speaks to, see [Connectors](connectors.md).
- A definition is a named persona with a kind, a handle, instructions, tools, triggers and limits, see [Definitions](definitions.md).
- A session is one execution of an agent against a repository, an issue or a pull request, see [Sessions](sessions.md).

Connectors and definitions live at instance, org or repo scope, like runners and secrets. A repository can use everything defined at its own scope, at its org and at the instance. When two scopes define the same handle the nearest scope wins.

## Kinds

| Kind | Behavior |
| --- | --- |
| review | Reads the diff of a pull request and submits one review with inline comments. |
| code | Implements issues and feedback by committing to a working branch and opening pull requests. |
| chat | Answers questions in issue and pull request threads. |
| triage | Labels, assigns and deduplicates issues. |
| custom | No built in guidance, the toolset and prompt come from the definition alone. |

## Enabling agents

Agents are off until an admin turns them on. Sessions run for a repository only when all three switches are on.

1. Under Admin > Agents set `git.agents.enabled` and add at least one connector. Instance connectors and definitions serve every repository.
2. Under Org > Agents an org owner or admin may turn agents off for the org, restrict what repositories may define, and add org level connectors and definitions.
3. Under Repo > Settings switch the Agents unit on. Repo > Settings > Agents then holds the repository limits, its own connectors and definitions, and a usage report.

Once enabled the repository shows an Agents tab with the usable agents, the instruction files found and the recent sessions.

## Settings

Runtime settings live under `git.agents` in the settings tree, see [Settings](../administration/settings.md).

| Setting | Default | Description |
| --- | --- | --- |
| `git.agents.enabled` | false | Sessions may run. Instance and org only. |
| `git.agents.allow_commands` | false | Sessions may run commands through a workflow runner. |
| `git.agents.commands_runs_on` | "" | Runner labels for command jobs, comma separated. Empty uses `git.actions.default_runs_on`, then `ubuntu-latest`. |
| `git.agents.max_turns` | 40 | Model round trips per session. Zero is unlimited. |
| `git.agents.max_session_minutes` | 0 | Wall clock cap on one session. Zero is unlimited. |
| `git.agents.max_sessions_per_day` | 0 | Sessions per repository per UTC day. Zero is unlimited. |
| `git.agents.max_concurrent_sessions` | 2 | Sessions running at once per repository. Zero is unlimited. |
| `git.agents.token_budget_per_session` | 1000000 | Transcript tokens per session, each counted once. Zero is unlimited. |
| `git.agents.log_retention_days` | 90 | Days finished sessions and their transcripts are kept. Zero keeps them forever. |
| `git.agents.allow_private_networks` | true | Connectors may reach loopback, private and link local addresses. Instance only. |
| `git.agents.allow_repo_providers` | true | Repository admins may register their own connectors. Instance and org only. |
| `git.agents.allow_repo_agents` | true | Repository admins may define agents. Instance and org only. |
| `git.agents.allow_file_agents` | true | Agents declared in repository files are loaded. |
| `git.agents.command_timeout_minutes` | 20 | Cap on one command. Zero trusts the timeout the model asks for. |
| `git.agents.debug_tool_calls` | false | Every tool call and result goes to the server log. Instance only. |

Lower scopes may only tighten these. An org or repository value above the instance cap is clamped to the cap, and a boolean the instance turned off cannot be turned on below it.

## Who may define what

| Scope | Managed by | Location |
| --- | --- | --- |
| system | Users with the git manage permission | Admin > Agents |
| org | Org owners and admins | Org > Agents |
| repo | Repository owners and admins | Repo > Settings > Agents |

Repository connectors need `git.agents.allow_repo_providers`, repository definitions need `git.agents.allow_repo_agents`, and file agents need both `git.agents.allow_repo_agents` and `git.agents.allow_file_agents`. Connectors and definitions inherited from a higher scope are listed at the lower scope but edited only where they were created.

Sessions follow the repository permission on the Agents unit. Read lists sessions and transcripts, write starts, cancels, continues and approves, admin deletes finished sessions. A definition can narrow who may start it by hand with `allowed_caller_ids`, a list of user and team ids. Automatic triggers bypass that list.

## Bot users

Every handle gets a bot account the first time an agent with that handle is saved. The account uses the handle as its username when it is free, otherwise `handle-agent`, then `handle-agent-2` and up. Its display name and picture follow the definition, an uploaded picture wins over the url. The bot is a regular user with the `user` role that acts through the forge like anyone else, so its comments, reviews, commits and pull requests show up under `@handle`. Every commit carries an `Agent: @handle session N` trailer.

Mentioning `@handle` in a comment or review comment starts a session for agents with the mention trigger. Assigning the bot to an issue or requesting a review from it does the same for the matching triggers. The bot is deactivated when no stored definition uses the handle any more.

## AgentsService

`AgentsService` groups its RPCs by resource. Every call takes a bearer token, see [Services](../api/services.md).

| Group | RPCs |
| --- | --- |
| Connectors | `ListAgentProviders`, `GetAgentProvider`, `CreateAgentProvider`, `UpdateAgentProvider`, `DeleteAgentProvider`, `TestAgentProvider`, `ListAgentProviderModels` |
| Definitions | `ListAgents`, `GetAgent`, `CreateAgent`, `UpdateAgent`, `DeleteAgent`, `SetAgentAvatar`, `RemoveAgentAvatar` |
| Repository | `GetRepoAgents` |
| Sessions | `StartAgentSession`, `ListAgentSessions`, `GetAgentSession`, `CancelAgentSession`, `RerunAgentSession`, `ContinueAgentSession`, `ResolveAgentApproval`, `ListAgentSessionEvents`, `StreamAgentSessionEvents`, `DeleteAgentSession` |
| Usage | `GetAgentUsage` |

Scoped calls take an `ActionsScope` with `kind` set to `system`, `org` or `repo` and `id` set to the org name or id, or `namespace/name` for a repository. They require administration of that scope. Session calls name the repository with `namespace` and `name`.

`dfcli agent list namespace/name` prints the agents a repository may use, see [CLI reference](../cli/reference.md).

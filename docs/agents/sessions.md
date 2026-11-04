# Agent sessions

A session is one run of an agent against a repository, optionally targeting an issue or a pull request. The server drives the model loop, executes tool calls, records every step as a transcript and finishes with a summary. Sessions are listed under the Agents tab of the repository and opened at `/<namespace>/<name>/agents/sessions/<id>`.

## Lifecycle

A session is `queued`, then `running`, possibly `waiting_approval`, and finally `completed` with one conclusion.

| Conclusion | Meaning |
| --- | --- |
| success | The model answered without a tool call. The answer is the summary. |
| failure | A tool, the connector or the repository failed, see the error. |
| cancelled | Cancelled by a user or by a newer pull request event. |
| timed_out | `git.agents.max_session_minutes` ran out. |
| rejected | A human rejected a gated action, or cancelled while one was pending. |
| budget_exceeded | The turn limit, the token budget or the connector daily budget was reached. |

A triggered session with a target that fails, times out or runs out of budget posts a short comment on the target with a link to the session. Sessions survive a server restart, running ones are relaunched from their transcript and queued ones are dispatched again.

## Triggers

| Trigger | Source |
| --- | --- |
| manual | Started from the Agents tab, `dfcli agent run` or `AgentsService.StartAgentSession`. |
| pull_request | A pull request was opened, synchronized, marked ready, or a review was requested from the bot. |
| issue | An issue or pull request was assigned to the bot. |
| label | A configured label was added. |
| mention | A comment or review comment mentioned `@handle`. The comment is the prompt. |
| review | A review landed on a pull request the agent opened. |
| workflow_run | A workflow run failed on a pull request the agent opened. |
| continue | A follow up instruction was sent to a finished session. |
| rerun | `AgentsService.RerunAgentSession` repeated a session with the same inputs. |

Which events an agent reacts to is set on its triggers, see [Definitions](definitions.md). A trigger without a prompt gets a default task such as `Review pull request !12` or `Implement issue #7`.

## Starting a session

Starting needs write access on the Agents unit of the repository, an agent that is enabled, a connector that resolves and room under the daily cap. A manual start is refused when the agent lists `allowed_caller_ids` and the caller is not on it.

```bash
dfcli agent run acme/app reviewer --issue 42
dfcli agent run acme/app coder --prompt "Add a healthz endpoint" --ref main --watch
cat task.md | dfcli agent run acme/app coder --prompt - --watch
```

| Flag | Description |
| --- | --- |
| --prompt | Instruction for the agent, `-` reads stdin. Empty uses the default task of the trigger. |
| --ref | Branch to work from. Empty uses the default branch, or the head of a targeted pull request. |
| --model | Model override within the connector allowlist. |
| --issue | Issue number to target. |
| --watch | Stream the transcript until the session finishes. |

The RPC takes the same fields plus `isPull`, which marks `issueNumber` as a pull request number. Issues and pull requests number separately.

```bash
curl -X POST https://forge.example.com/distroface.v1.AgentsService/StartAgentSession \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"namespace": "acme", "name": "app", "agent": "reviewer", "issueNumber": 12, "isPull": true}'
```

## Working branch

Edits made with the file tools live in an overlay on top of the working branch and reach git only through the `commit` tool. The working branch is `agents/<handle>/<session number>`, cut from `--ref` or the default branch. On a pull request the agent opened itself the session commits to the pull request branch. On someone else's pull request the working branch is cut from the pull request head, or from the base branch when the head lives in a fork. `open_pull_request` proposes the working branch into its base.

When a code agent finishes with commits and no pull request yet, the server opens one for it, adds `Closes #N` when the session targeted an issue, and posts the summary on the target. A review agent that finished without submitting a review gets its summary posted as a comment review. A chat agent gets its summary posted as a comment. Uncommitted edits are counted in the summary and do not survive the session.

## Tools

| Tool | Does | Gate |
| --- | --- | --- |
| get_repository_info | Repository, default branch, languages, working branch and instruction files. | |
| list_files | Lists a directory of the working tree, up to 2000 entries. | |
| read_file | Reads a text file with line numbers, 1000 lines or 32000 bytes per call. | |
| search_code | Regular expression search over committed files, 200 matches by default. | |
| get_diff | Pull request diff against its base, or the uncommitted edits of the session. | |
| get_pull_request | Description, commits, review threads, checks and comments of a pull request. | |
| get_issue | An issue or pull request with labels, assignees and comments. | |
| list_commits | Recent commits of the pull request or the working branch, 50 by default. | |
| get_checks | Commit statuses and workflow runs of the head commit. | |
| get_workflow_run_log | Tail of a workflow job log, 500 lines by default. | |
| search_issues | Issues and pull requests by text, state and labels. | |
| create_issue | Opens an issue. A session without a target adopts it. | write |
| post_comment | Comments on the target or another issue. | write |
| submit_review | One review with inline comments on the new side of the diff. | write |
| update_issue | Labels, assignees, title or state of an issue. | write |
| request_review | Requests reviews from users on a pull request. | write |
| write_file, edit_file, delete_file, rename_file | Change the working tree overlay. | |
| commit | Commits every pending edit to the working branch. | commit |
| open_pull_request | Opens a pull request from the working branch into its base. | commit |
| run_command | Runs a bash script in the command workspace. | commit |

The gate column names the approval policy that pauses the call, `commits` pauses the commit rows and `all_writes` pauses both. Results above 30000 bytes lose their middle, old results may be replaced by a one line stub after ten turns, and a repeated read with the same arguments points at its earlier result.

## Commands

Agent sessions that run commands open one job per session, the command workspace. It checks the working branch out once, writes the session's uncommitted edits in front of each command, and stays open until the session ends, so installed dependencies survive between commands. Commands need `git.agents.allow_commands` and a runner serving `git.agents.commands_runs_on` or the actions default label, see [Runners](../actions/runners.md).

The runner must be online and advertise the `exec` capability, which `dfcli runner` does. The job runs as a workflow run named `Agent session #N` with read only permissions and no secrets. A workflow file named `agent-setup-steps` with a job of the same name is run first when the checked out commit has one, so toolchains are installed there. Its `runs-on`, `container`, `services`, `env` and `defaults` apply to the workspace. Each command is capped by `git.agents.command_timeout_minutes`, the job by `git.agents.max_session_minutes` or 23 hours. The script changes nothing in git, only `commit` and `open_pull_request` touch the branch.

## Approval gates

A gated call parks the session as `waiting_approval`, records an approval event and notifies the user who started it. The Agents tab and the session page show the pending action with its arguments. Anyone with write access decides.

```bash
dfcli agent approve acme/app <session-id>
dfcli agent approve acme/app <session-id> --reject --note "Not on this branch"
```

Approving resumes the session and runs the call. Rejecting resumes it with the note as the tool result, so the model can adapt. The RPC is `AgentsService.ResolveAgentApproval` with the `approvalId` from the session's `pendingApproval`. Cancelling a waiting session rejects the pending action and ends with conclusion `rejected`.

## Budgets

| Setting | Effect |
| --- | --- |
| `git.agents.max_turns` | Model round trips. The agent `max_turns` tightens it. Exceeding ends with `budget_exceeded`. |
| `git.agents.token_budget_per_session` | Input plus output tokens, transcript growth counted once. The agent `max_tokens` tightens it. |
| `git.agents.max_session_minutes` | Wall clock from the first start. Exceeding ends with `timed_out`. |
| `git.agents.max_sessions_per_day` | Sessions per repository per UTC day. Starting beyond it is refused. |
| `git.agents.max_concurrent_sessions` | Sessions running at once per repository, the rest wait in the queue. |

The agent `concurrency` field and the connector `tokens_per_day` and `requests_per_minute` limits apply on top. Zero means unlimited everywhere.

## Transcripts and events

Every step is an event with a sequence number, a kind, text and json details.

| Kind | Content |
| --- | --- |
| status | Queued, running, waiting, retrying, completed. |
| system | The system prompt and the frozen target context. |
| user | The prompt and every follow up instruction. |
| assistant | Model text and the tool calls it asked for. |
| tool_call, tool_result | One call and its output. |
| approval | A gate requested or decided. |
| usage | Tokens of one completion and running totals. |
| error | The failure that ended the session. |
| result | The final summary. |

```bash
dfcli agent sessions acme/app --table
dfcli agent sessions acme/app --agent reviewer --trigger pull_request --issue 12
dfcli agent view acme/app <session-id>
dfcli agent view acme/app <session-id> --transcript
dfcli agent watch acme/app <session-id>
```

`sessions` filters by `--agent`, `--trigger`, `--actor` and `--issue`. `view` prints the session, `--transcript` prints the events instead. `watch` streams events through `AgentsService.StreamAgentSessionEvents` until the session finishes. `AgentsService.ListAgentSessionEvents` pages the stored transcript.

Finishing dispatches a `git_agent_session` webhook with action `completed`, pausing dispatches one with `waiting_approval`, see [Webhooks](../webhooks.md). The user who started the session is notified on both, see [Notifications](../git/notifications.md).

## Continuing a finished session

A completed session accepts follow up instructions. The instruction is appended to the transcript, the session is queued again with a fresh turn and token budget on top of what it spent, and the model continues with the full history on the same working branch.

```bash
dfcli agent continue acme/app <session-id> "Also add a unit test for the handler" --watch
```

`AgentsService.RerunAgentSession` instead starts a new session with the same agent, prompt, ref, target and model.

## Cancelling and deleting

```bash
dfcli agent cancel acme/app <session-id>
```

Cancel stops a queued, running or waiting session and closes its command workspace. Finished sessions are deleted with `AgentsService.DeleteAgentSession`, which needs admin on the repository, and automatically after `git.agents.log_retention_days` days. The retention sweep runs every six hours.

## Debugging

`git.agents.debug_tool_calls` is an instance setting that writes every tool call, its arguments, its result and its duration to the server log, see [Logging](../getting-started/logging-and-profiling.md). The transcript already holds the same information, the log is for correlating with connector and runner output.

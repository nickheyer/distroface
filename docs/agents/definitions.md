# Agent definitions

A definition is a named agent persona: a kind, a handle, a connector and model, instructions, the tools it may call, the events that start it and the limits it runs under. Definitions are stored at system, org or repo scope, or declared in files inside the repository.

## Kinds and default tools

Every kind has built in guidance in the system prompt and a default toolset. A `tools` list on the definition replaces the default.

| Kind | Default tools |
| --- | --- |
| review | get_repository_info, list_files, read_file, search_code, get_diff, get_pull_request, list_commits, get_checks, get_workflow_run_log, post_comment, submit_review |
| code | get_repository_info, list_files, read_file, search_code, get_diff, get_pull_request, get_issue, list_commits, get_checks, get_workflow_run_log, search_issues, write_file, edit_file, delete_file, rename_file, commit, open_pull_request, run_command, post_comment, request_review, update_issue, create_issue |
| chat | get_repository_info, list_files, read_file, search_code, get_diff, get_pull_request, get_issue, list_commits, get_checks, get_workflow_run_log, search_issues, post_comment |
| triage | get_repository_info, read_file, search_code, get_issue, search_issues, list_commits, update_issue, create_issue, post_comment |
| custom | get_repository_info, list_files, read_file, search_code, get_diff, get_pull_request, get_issue, list_commits, search_issues, post_comment, create_issue |

Any agent given the `commit` tool receives the branch and pull request guidance of the code kind. The tools themselves are described in [Sessions](sessions.md).

## Fields

| Field | Description |
| --- | --- |
| slug | Handle, 3 to 40 lower case letters, digits or hyphens. Doubles as the mention handle and bot username, so it cannot be a reserved name or belong to a user or org. Unique per scope. |
| name | Display name, up to 100 characters. |
| description | Shown in lists. |
| kind | review, code, chat, triage or custom. |
| provider_id | Connector to use. Empty uses the default of the nearest scope. Must be visible from the agent's scope. |
| model | Model on the connector. Empty uses the connector default. Must be in the connector allowlist. |
| instructions | Extra system prompt appended after the kind guidance and the repository instructions, up to 64 KiB. |
| tools | Tool names the agent may call. Empty uses the kind default. Unknown names are rejected. |
| triggers | Events that start a session, see below. |
| max_turns | Model round trips per session. Zero inherits `git.agents.max_turns`, a lower value tightens it. |
| max_tokens | Token budget per session. Zero inherits `git.agents.token_budget_per_session`, a lower value tightens it. |
| temperature | Sampling temperature between 0 and 2. Unset uses the model default. |
| approval | none, commits or all_writes, see below. |
| enabled | Disabled agents neither trigger nor start. |
| concurrency | Sessions of this agent running at once per repository. Zero is unlimited within `git.agents.max_concurrent_sessions`. |
| allowed_caller_ids | User and team ids who may start it by hand. Empty allows anyone with write access. |
| avatar_url | Picture url, http(s) or site relative. `AgentsService.SetAgentAvatar` uploads a png, jpeg, gif or webp under 1 MB instead, and the upload wins over the url. |

## Triggers

| Field | Starts a session when |
| --- | --- |
| pull_request_opened | A pull request is opened. |
| pull_request_synchronize | New commits land on a pull request. |
| pull_request_ready_for_review | A draft is marked ready. |
| review_requested | A review is requested from the agent's bot. |
| issue_assigned | An issue or pull request is assigned to the agent's bot. |
| issue_labels | One of the listed labels is added to an issue or pull request. |
| mention | A comment or review comment mentions `@handle`. The comment text becomes the prompt. |
| review_feedback | A review requesting changes, or a comment review with a body, lands on a pull request the agent opened. |
| workflow_failure | A workflow run fails on a pull request the agent opened, at most three times per pull request. |
| base_branches | Glob filters on the base branch for pull request triggers. Empty matches every branch. |
| paths, paths_ignore | Globs over the changed files of the pull request. Both empty matches everything. |
| include_drafts | Draft pull requests also trigger. |
| respond_to_bots | Events raised by other bots trigger too. An agent never reacts to its own bot. |

Pull request triggers cancel a live `pull_request` session of the agent on the same pull request before starting a new one. Assignment, label, review feedback and workflow triggers start nothing while a session of the agent is already live on the same issue or pull request.

## Approval policies

| Policy | Pauses for a human before |
| --- | --- |
| none | Nothing. |
| commits | commit, open_pull_request and run_command. |
| all_writes | The commits set plus create_issue, post_comment, submit_review, update_issue and request_review. |

File edits never pause, they stay in the session workspace until a commit. Resolving a pause is described in [Sessions](sessions.md).

## Creating an agent

Under Admin > Agents, Org > Agents or Repo > Settings > Agents open Definitions and choose New. The same form is `AgentsService.CreateAgent`, which also provisions the bot account.

```bash
curl -X POST https://forge.example.com/distroface.v1.AgentsService/CreateAgent \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "scope": {"kind": "repo", "id": "acme/app"},
    "spec": {
      "slug": "reviewer",
      "name": "Reviewer",
      "kind": "AGENT_KIND_REVIEW",
      "model": "qwen2.5-coder:32b",
      "instructions": "Focus on correctness and error handling.",
      "triggers": {"pullRequestOpened": true, "pullRequestSynchronize": true, "mention": true, "baseBranches": ["main"]},
      "approval": "AGENT_APPROVAL_POLICY_NONE",
      "enabled": true
    }
  }'
```

`AgentsService.UpdateAgent` replaces every field. Running sessions keep a snapshot of the definition they started with, so edits apply to the next session.

## File agents

A repository can declare agents in markdown files on its default branch under `.distroface/agents/` or `.github/agents/`. Every `.md` file there is one agent. The handle is the file name without `.md` or `.agent.md`, so `.distroface/agents/security.md` defines `@security`. The yaml front matter holds the fields and the markdown body is the instructions. A file without front matter is a custom agent made of instructions alone.

File agents need `git.agents.allow_repo_agents` and `git.agents.allow_file_agents`. They appear in lists with source `file`, id `file:<handle>` and the file path, and are read only through the API. A parse error is reported on the agent, which stays listed with the fields that did parse.

| Key | Description |
| --- | --- |
| name, description | As above. The file name is the default name. |
| kind | review, code, chat, triage or custom. Default custom. |
| provider | Connector name or id among those the repository may use. Unknown names fall back to the default connector and set an error. |
| model | Model on the connector. |
| tools | List of tool names. |
| approval | none, commits or all_writes. Default commits. |
| max_turns, max_tokens, temperature, concurrency | As above. |
| enabled | Default true. |
| avatar | Picture url. |
| triggers.pull_request | List of `opened`, `synchronize`, `ready_for_review`. |
| triggers.review_requested, issue_assigned, mention, review_feedback, workflow_failure, include_drafts, respond_to_bots | Booleans, as above. |
| triggers.labels | List of labels. |
| triggers.base_branches, paths, paths_ignore | Lists of globs. |

```markdown
---
name: Security Reviewer
description: Finds vulnerabilities in pull requests
kind: review
provider: ollama-lab
model: qwen2.5-coder:32b
tools: [get_diff, read_file, search_code, submit_review]
approval: all_writes
max_turns: 12
temperature: 0.2
triggers:
  pull_request: [opened, synchronize]
  review_requested: true
  labels: [security]
  base_branches: ["main", "release/*"]
  mention: true
---
Focus on injection, authentication and authorization issues.
Cite the file and line for every finding.
```

## Repository instructions

Every session reads `AGENTS.md`, `.distroface/AGENTS.md`, `.distroface/agents/instructions.md` and `.github/copilot-instructions.md` from the default branch, when present, and puts their contents in the system prompt ahead of the agent instructions. The Agents tab of the repository lists which ones were found.

## Enabling and disabling

Clear `enabled` on a stored agent, or set `enabled: false` in the file, to keep the definition while stopping triggers and manual starts. Deleting a definition keeps its sessions readable, they carry their own snapshot. The bot account is deactivated once no stored definition uses the handle.

`dfcli agent list namespace/name --table` prints the agents a repository may use with their handle, kind, source and model, see [CLI reference](../cli/reference.md).

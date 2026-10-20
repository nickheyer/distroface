# Webhooks

A webhook is an HTTP POST sent to a URL of your choice when something happens in a container image repository or a git repository. Webhooks are managed with `WebhookService` or in the UI.

Artifact repositories do not send webhooks.

## Scopes

| Scope | Fires for | Managed at |
|---|---|---|
| `WEBHOOK_SCOPE_REPOSITORY` | One image repository | Images > repository > Webhooks |
| `WEBHOOK_SCOPE_GIT_REPOSITORY` | One git repository | Repo > Settings > Webhooks |
| `WEBHOOK_SCOPE_ORGANIZATION` | Every image and git repository in the organization namespace | Org > Webhooks |

Image repository webhooks need the `repositories` `update` action on the repository. Git repository webhooks need admin level on the repository. Organization webhooks need `organizations` `update` or an owner or admin role in the organization. Listing and reading image repository webhooks need `repositories` `read`, organization webhooks need `organizations` `read` or membership, git repository webhooks need admin level for every call. See [users and roles](administration/users-and-roles.md).

## Events

A webhook fires only for events in its `events` list and only while `active` is true.

| Event | Sent when |
|---|---|
| `push` | A manifest is pushed, carries `tag` and `digest` |
| `pull` | A manifest is pulled, carries `tag` and `digest` |
| `delete` | A manifest is deleted (`digest` only) or a tag is deleted (`tag` only) |
| `git_push` | A branch receives commits, carries `push` with up to 20 commits |
| `git_branch` | A branch is created or deleted, carries `ref` |
| `git_tag` | A tag is created or deleted, carries `ref` |
| `git_issue`, `git_issue_comment` | Issue and issue comment changes |
| `git_pull_request`, `git_pull_request_review`, `git_pull_request_comment` | Pull request changes |
| `git_release` | A release changes, carries `release` |
| `git_wiki` | A wiki page changes, carries `wiki` |
| `git_repository` | Repository lifecycle changes |
| `git_workflow_run` | A workflow run changes state, carries `workflow_run` |
| `git_agent_session` | An agent session changes state, carries `agent_session` |
| `git_fork` | Accepted by the API, nothing sends it today |

Git events carry an `action` string naming the change. Push, branch and tag events use `created`, `deleted`, `forced` or `updated`.

## Creating a webhook

```bash
curl -X POST https://df.example.com/distroface.v1.WebhookService/CreateWebhook \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{
    "scope":"WEBHOOK_SCOPE_GIT_REPOSITORY","repo_id":"<git repo id>",
    "url":"https://ci.example.com/hooks/distroface","secret":"s3cret",
    "events":["WEBHOOK_EVENT_GIT_PUSH","WEBHOOK_EVENT_GIT_TAG"],"active":true
  }'
```

| Parameter | Description |
|---|---|
| `scope` | One of the scopes above, unspecified means image repository |
| `repo_id` | Image or git repository id, required for repository scopes |
| `org_id` | Organization id, required for organization scope |
| `url` | Absolute `http` or `https` URL |
| `secret` | Optional, used to sign deliveries, never returned (`has_secret` is) |
| `events` | At least one `WebhookEvent` |
| `active` | Deliveries are skipped while false |
| `content_type` | `Content-Type` header of deliveries, default `application/json` |
| `payload_template` | Optional Go text template that replaces the default body |

`WebhookService.UpdateWebhook` takes the same fields. Empty `url`, `secret`, `events` and `content_type` keep the stored value, `active` and `payload_template` only change when present.

## Payload

Image events send:

```json
{"event":"push","timestamp":"2024-01-01T00:00:00Z","repository":{"namespace":"acme","name":"api","full_name":"acme/api"},"tag":"1.4.2","digest":"sha256:..."}
```

Git events send `event`, `action`, `timestamp`, `repository` and `sender`, plus one object named after the event:

```json
{
  "event":"git_push","action":"updated","timestamp":"2024-01-01T00:00:00Z",
  "repository":{"namespace":"acme","name":"api","full_name":"acme/api","clone_url":"...","ssh_url":"...","html_url":"...","private":false,"default_branch":"main"},
  "sender":{"username":"alice"},
  "push":{"ref":"refs/heads/main","before":"<sha>","after":"<sha>","forced":false,"pusher":"alice","commits":[{"sha":"...","message":"...","url":"...","author":{"name":"...","email":"..."},"committer":{"name":"...","email":"..."},"timestamp":"..."}],"truncated":false,"compare_url":"..."}
}
```

The other objects are `ref` (`ref`, `name`, `sha`), `wiki`, `issue`, `comment`, `pull_request`, `review`, `release`, `forkee`, `workflow_run` and `agent_session`.

Templates receive the payload above and may use `toJSON`, `toUpper`, `toLower` and `replace`. A template is validated against a sample payload when saved. If rendering fails at delivery time the default body is sent instead.

## Headers and signature

| Header | Value |
|---|---|
| `Content-Type` | The webhook's `content_type` |
| `X-Distroface-Event` | Event name |
| `X-Distroface-Delivery` | Delivery id |
| `X-Distroface-Signature-256` | `sha256=<hex>`, present when a secret is set |

The signature is HMAC-SHA256 of the raw request body keyed with the secret. Compute it over the exact bytes received and compare with the header.

## Delivery and retries

Each delivery times out after 10 seconds and succeeds on any 2xx status. A failed delivery is retried up to 5 attempts in total, waiting 10 seconds, 40 seconds, 160 seconds and 640 seconds between attempts, each delay varied by up to 20 percent. Every attempt is written to the delivery log with its status code, duration, request body and up to 10 KB of the response body.

## Private networks

| Setting | Default | Description |
|---|---|---|
| `webhooks.allow_private_networks` | `false` | Permit deliveries to private IP ranges |

The destination is checked when the connection is dialed, after DNS resolution. Loopback, unspecified, link local and multicast addresses are always refused. Private ranges are refused unless the setting is on. See [settings](administration/settings.md).

## Delivery log and redelivery

`WebhookService.ListWebhookDeliveries` returns past attempts newest first, filterable by `event`. Each entry has `id`, `event`, `status_code`, `success`, `request_body`, `response_body`, `duration_ms`, `attempt` and `delivered_at`. The UI shows the same list for each webhook.

`WebhookService.RedeliverWebhook` sends the stored request body of a past delivery again in a single attempt, without retries, and returns the result in the response. The redelivery is not added to the log. It needs `update` permission on the webhook's scope.

## RPCs

| RPC | Description |
|---|---|
| `WebhookService.CreateWebhook` | Register a webhook |
| `WebhookService.ListWebhooks` | List by `repo_id` or `org_id`, filters `url`, `events`, `created_by` |
| `WebhookService.GetWebhook` | Fetch one by id |
| `WebhookService.UpdateWebhook` | Change URL, secret, events, state, content type or template |
| `WebhookService.DeleteWebhook` | Remove a webhook and its deliveries |
| `WebhookService.ListWebhookDeliveries` | Delivery history for one webhook |
| `WebhookService.RedeliverWebhook` | Resend a past delivery |

dfcli has no webhook commands.

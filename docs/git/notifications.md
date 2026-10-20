# Notifications

Notifications are inbox entries for activity on repositories a user watches or takes part in. Each user also has preferences that decide what reaches the inbox and what is emailed.

## What triggers a notification

| Kind | Reason | Sent when |
| --- | --- | --- |
| `issue`, `pull` | `opened`, `closed`, `reopened`, `merged` | An issue or pull request changes state |
| `issue`, `pull` | `commented` | A comment or an inline review comment is posted |
| `issue`, `pull` | `assigned` | The recipient is assigned |
| `issue`, `pull`, `review` | `mentioned` | The recipient or one of their teams is `@mentioned` |
| `review` | `review_requested` | The recipient or their team is asked to review |
| `review` | `reviewed` | A review is submitted |
| `release` | `released` | A release is published |
| `workflow_run` | | A workflow run completes. The triggering user is named directly |
| `invite` | | A collaborator invitation. The invited user is named directly |
| `transfer` | | A repository transfer offer. The target user is named directly |
| `agent_session` | `approval`, `completed` | An agent session needs approval or finishes, see [Sessions](../agents/sessions.md) |

Repeated activity on the same issue updates the existing entry instead of adding one. The acting user never notifies themselves.

## Who receives it

Recipients are gathered in this order, then filtered:

1. Explicit recipients: mentioned users, requested reviewers, new assignees, and users named by the event (invites, transfers, workflow runs).
2. Participants of the issue: the author, every commenter, assignees, requested reviewers, and anyone subscribed. Users who unsubscribed are removed unless mentioned.
3. Users watching the repository with mode `all`.

Users whose watch mode is `ignore` are dropped unless they were mentioned or named by the event. Inactive users and users who cannot see the repository are dropped. Review requests only notify the requested users and team members, not participants or watchers.

## Watching a repository

`GitService.WatchGitRepository` sets the caller's mode for a repository. Creating a repository watches it with `all`.

| Mode | Description |
| --- | --- |
| `WATCH_MODE_PARTICIPATING` | Only issues and pull requests the user takes part in |
| `WATCH_MODE_ALL` | Every issue, pull request and release on the repository |
| `WATCH_MODE_IGNORE` | Nothing unless mentioned or named directly |

```bash
curl -X POST https://forge.example.com/distroface.v1.GitService/WatchGitRepository \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"namespace":"acme","name":"api","mode":"WATCH_MODE_ALL"}'
```

## Subscribing to an issue

Opening, commenting on, or reviewing an issue or pull request subscribes the caller to it. `IssueService.SubscribeIssue` and `UnsubscribeIssue` change that per issue, and the issue reports `subscribed` for the viewer. Unsubscribing removes the user from the participant set for that issue.

## The inbox

The inbox is at `/notifications`. Every entry has a status of `unread`, `read` or `pinned`, a kind, the last reason, who acted, and a link to the issue, pull request, commit, release, run or agent page.

| RPC | Description |
| --- | --- |
| `NotificationService.ListNotifications` | Pages the inbox. Empty `status` lists unread and pinned, `all` lists everything, `repo_full_name` narrows to one repository |
| `NotificationService.GetUnreadCount` | Unread total |
| `NotificationService.MarkRead` | Marks listed ids read, or unread with `unread: true` |
| `NotificationService.MarkAllRead` | Marks everything read, or one repository with `repo_full_name` |
| `NotificationService.PinNotification` | Pins an entry so it stays listed, or unpins it to read |
| `NotificationService.WatchNotifications` | Server stream of the unread count. Sends the count on connect, on every change, and every 45 seconds as a keepalive |

```bash
curl -X POST https://forge.example.com/distroface.v1.NotificationService/MarkRead \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"ids":["<notification id>"]}'
```

## Preferences

Preferences are at Settings > Notifications and through `NotificationService.GetNotificationPrefs` and `UpdateNotificationPrefs`. `UpdateNotificationPrefs` replaces every field.

| Field | Default | Description |
| --- | --- | --- |
| `web_mode` | `PARTICIPATING` | `NONE` writes no inbox entries at all |
| `email_mode` | `PARTICIPATING` | `ALL` emails every notification, `NONE` emails only the per kind toggles below, `PARTICIPATING` emails events on issues the user takes part in and events naming the user |
| `email_mentions` | `true` | Email on mention regardless of `email_mode` |
| `email_review_requests` | `true` | Email on review request regardless of `email_mode` |
| `email_assignments` | `true` | Email on assignment regardless of `email_mode` |
| `email_own_activity` | `false` | Stored with the preferences. The acting user is excluded from recipients regardless |

## Email delivery

Mail is sent only when mail is enabled for the repository's settings scope, see [Mail](../administration/mail.md). Each message carries `List-Id`, `X-Distroface-Reason` and `X-Distroface-Repository` headers. Messages about an issue thread under a `Message-ID` built from the issue number, and comments reply to it, so mail clients group a conversation. When incoming mail is configured with a reply address, the message has a `Reply-To` that posts a reply as a comment, see [Issues reply by email](issues.md#reply-by-email).

The footer links to the notification preferences with a signed token valid for 30 days. Passing it as `unsubscribe_token` to `GetNotificationPrefs` or `UpdateNotificationPrefs` changes that user's preferences without a session.

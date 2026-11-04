# Mail

Distroface sends mail through an SMTP relay for password resets, email verification, registration invites and repository notifications, and can read replies from an IMAP mailbox to post them as comments. Mail is configured at Admin > Mail.

## Outbound settings

| Setting | Default | Description |
| --- | --- | --- |
| `mail.enabled` | `false` | Sending also needs `smtp_host` |
| `mail.smtp_host` | empty | Relay hostname |
| `mail.smtp_port` | `587` | Relay port |
| `mail.security` | `MAIL_SECURITY_STARTTLS` | `NONE`, `STARTTLS`, or `TLS` for implicit TLS |
| `mail.username` | empty | SMTP auth user, auth is skipped when empty |
| `mail.password` | empty | Write only, reads return `password_set` |
| `mail.from_address` | empty | Required, delivery fails without it |
| `mail.from_name` | `Distroface` | Display name on the from header |
| `mail.reply_to` | empty | Reply-To header on every message |
| `mail.allow_private_networks` | `true` | Off refuses relays that resolve to loopback, private or link local addresses |
| `mail.skip_tls_verify` | `false` | Accepts the relay certificate without verification |
| `mail.max_per_minute` | `0` | Messages per minute per scope, `0` unlimited |

Connections use TLS 1.2 or newer and time out after 30 seconds, a send after 60.

## Tiers

Organizations and repositories may store their own relay under Org > Email and the repository Settings > Email page. They may set `enabled`, `smtp_host`, `smtp_port`, `security`, `username`, `password`, `from_address`, `from_name`, `reply_to` and `skip_tls_verify`. The throttle, the private network rule and the inbound settings stay at system scope. See [Settings](settings.md).

A repository event sends through the nearest tier that names its own `smtp_host`. A tier that turns mail off stops the search, so an org can silence notifications for its repositories without a relay of its own.

## Testing and the delivery log

`MailService.SendTestEmail` queues a test message with the effective settings of a scope. Without `to` it goes to the caller's address.

```bash
curl -X POST http://localhost:8080/distroface.v1.MailService/SendTestEmail \
  -H 'Authorization: Bearer df_...' -H 'Content-Type: application/json' \
  -d '{"scope":{"type":"SETTINGS_SCOPE_TYPE_SYSTEM"},"to":"ops@example.com"}'
```

The Deliveries list on Admin > Mail, backed by `MailService.ListMailDeliveries`, shows queued, sent and failed messages with attempts and the last error. `MailService.RetryMailDelivery` requeues a failed message. System scope needs `settings:manage`, org scope an org owner or admin, repository scope a repository admin.

Messages are rows in the database. A worker drains up to 50 due messages every 10 seconds, on one instance at a time when a shared cache is configured. A failed send is retried with a delay of 2 to the power of the attempt count in minutes and marked failed after five attempts, which is also written to the audit log as `Mail/send`. The log keeps messages for 30 days. Every message carries `Auto-Submitted: auto-generated` so auto responders stay quiet.

## What is sent

| Message | Trigger | Link lifetime |
| --- | --- | --- |
| Password reset | `AuthService.RequestPasswordReset` | One hour |
| Email verification | Registration with an email, `AuthService.RequestEmailVerification`, or a login refused for a missing verification | One day |
| Invite | `AuthService.CreateInvite` with `emails` | The invite's expiry |
| Notifications | Issues, pull requests, reviews, comments and releases, per the recipient's notification preferences | |

`AuthService.GetAuthStatus` reports `password_reset_enabled` only when local sign in and system mail are both on. Notification preferences are described in [Notifications](../git/notifications.md).

## Reply by email

With inbound mail on, every notification is sent from a unique reply address and a reply posts a comment on the issue or pull request.

| Setting | Default | Description |
| --- | --- | --- |
| `mail.incoming.enabled` | `false` | Polls the mailbox once a minute |
| `mail.incoming.imap_host` | empty | IMAP server |
| `mail.incoming.imap_port` | `993` | IMAP port |
| `mail.incoming.tls` | `true` | Implicit TLS, off uses STARTTLS which must succeed |
| `mail.incoming.username` | empty | Mailbox login |
| `mail.incoming.password` | empty | Write only, reads return `password_set` |
| `mail.incoming.mailbox` | `INBOX` | Folder to poll |
| `mail.incoming.reply_address` | empty | Address whose plus extension carries the token, `notify@example.com` |

Each notification gets a reply address of the form `notify+<token>@example.com`. The token is bound to the recipient and the issue and expires after 30 days. The relay and the mailbox must deliver plus addressed mail to the configured account.

The poller reads unseen messages, finds the token in the recipient headers, ignores auto responses and list traffic, and takes the first text part, or stripped HTML. The sender address must equal the email of the notified user, the user must be active and able to read issues on the repository, and quoted history and signatures are stripped. The comment is posted as that user with a via email marker. Processed messages are marked seen, and messages that could not be posted are moved to a `Failed` folder.

<script lang="ts">
	import { rpc } from '$lib/rpc';
	import { Lister } from '$lib/list.svelte';
	import { WebhookEvent, WebhookScope, type Webhook, type WebhookDelivery } from '$lib/proto/distroface/v1/types_pb';
	import { fmtWhen, fmtDuration, webhookEventLabel } from '$lib/fmt';
	import { errata } from '$lib/state/errata.svelte';
	import Confirm from '$lib/bits/Confirm.svelte';
	import Tally from '$lib/bits/Tally.svelte';
	import Mark from '$lib/bits/Mark.svelte';

	let { repoId = '', orgId = '' }: { repoId?: string; orgId?: string } = $props();

	// svelte-ignore state_referenced_locally
	const scope = repoId ? WebhookScope.REPOSITORY : WebhookScope.ORGANIZATION;

	const hooks = new Lister<Webhook>((page) =>
		rpc.webhook.listWebhooks({ page, repoId, orgId }).then((r) => ({ rows: r.webhooks, page: r.page }))
	);

	$effect(() => {
		hooks.first();
	});

	// ── Compose / amend ─────────────────────────────────────────────
	let editing = $state<Webhook | null>(null);
	let formOpen = $state(false);
	let url = $state('');
	let secret = $state('');
	let events = $state<WebhookEvent[]>([WebhookEvent.PUSH]);
	let active = $state(true);
	let contentType = $state('application/json');
	let template = $state('');
	let busy = $state(false);

	const allEvents = [WebhookEvent.PUSH, WebhookEvent.PULL, WebhookEvent.DELETE];

	function toggleEvent(ev: WebhookEvent, on: boolean) {
		events = on ? [...events, ev] : events.filter((e) => e !== ev);
	}

	function startEdit(h: Webhook) {
		editing = h;
		url = h.url;
		secret = '';
		events = [...h.events];
		active = h.active;
		contentType = h.contentType || 'application/json';
		template = h.payloadTemplate;
		formOpen = true;
	}

	function startNew() {
		editing = null;
		url = '';
		secret = '';
		events = [WebhookEvent.PUSH];
		active = true;
		contentType = 'application/json';
		template = '';
		formOpen = true;
	}

	async function submit(e: Event) {
		e.preventDefault();
		if (events.length === 0) {
			errata.report('A webhook must subscribe to at least one event.');
			return;
		}
		busy = true;
		try {
			if (editing) {
				await rpc.webhook.updateWebhook({
					id: editing.id,
					url,
					secret,
					events,
					active,
					contentType,
					payloadTemplate: template
				});
				errata.remark('Webhook saved.');
			} else {
				await rpc.webhook.createWebhook({
					scope,
					repoId,
					orgId,
					url,
					secret,
					events,
					active,
					contentType,
					payloadTemplate: template
				});
				errata.remark('Webhook created.');
			}
			formOpen = false;
			await hooks.fetch();
		} catch {
			// Interceptor reports
		} finally {
			busy = false;
		}
	}

	async function toggleActive(h: Webhook) {
		await rpc.webhook.updateWebhook({
			id: h.id,
			url: h.url,
			events: h.events,
			active: !h.active,
			contentType: h.contentType,
			payloadTemplate: h.payloadTemplate
		});
		await hooks.fetch();
	}

	async function remove(h: Webhook) {
		await rpc.webhook.deleteWebhook({ id: h.id });
		if (openHook?.id === h.id) openHook = null;
		await hooks.fetch();
	}

	// ── Deliveries ──────────────────────────────────────────────────
	let openHook = $state<Webhook | null>(null);
	let deliveries = $state<WebhookDelivery[]>([]);

	async function showDeliveries(h: Webhook) {
		if (openHook?.id === h.id) {
			openHook = null;
			return;
		}
		openHook = h;
		const r = await rpc.webhook.listWebhookDeliveries({ webhookId: h.id });
		deliveries = r.deliveries;
	}

	async function redeliver(d: WebhookDelivery) {
		await rpc.webhook.redeliverWebhook({ deliveryId: d.id });
		errata.remark('Redelivery dispatched.');
		if (openHook) {
			const r = await rpc.webhook.listWebhookDeliveries({ webhookId: openHook.id });
			deliveries = r.deliveries;
		}
	}
</script>

{#if hooks.loaded && hooks.rows.length === 0 && !formOpen}
	<p class="vacant">No webhooks for this {repoId ? 'repository' : 'organization'} yet.</p>
{:else if hooks.rows.length > 0}
	<div class="ledger-scroll">
		<table class="ledger">
			<thead>
				<tr>
					<th>Endpoint</th>
					<th>Events</th>
					<th>State</th>
					<th>Recorded</th>
					<th class="end">&nbsp;</th>
				</tr>
			</thead>
			<tbody>
				{#each hooks.rows as h (h.id)}
					<tr>
						<td class="mono" style="overflow-wrap: anywhere">{h.url}</td>
						<td><span class="caps soft">{h.events.map((e) => webhookEventLabel[e]).join(' · ')}</span></td>
						<td>
							{#if h.active}
								<Mark kind="ok" label="active" />
							{:else}
								<Mark kind="off" label="paused" />
							{/if}
						</td>
						<td class="mono">{fmtWhen(h.createdAt)}</td>
						<td class="end">
							<button class="rowact plain" onclick={() => showDeliveries(h)}>
								{openHook?.id === h.id ? 'hide deliveries' : 'deliveries'}
							</button>
							<button class="rowact plain" onclick={() => toggleActive(h)}>
								{h.active ? 'pause' : 'resume'}
							</button>
							<button class="rowact plain" onclick={() => startEdit(h)}>edit</button>
							<Confirm onconfirm={() => remove(h)} />
						</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</div>
	<Tally lister={hooks} unit="webhooks" />
{/if}

{#if openHook}
	<div class="panel">
		<p class="panel-title">Deliveries · {openHook.url}</p>
		{#if deliveries.length === 0}
			<p class="note">Nothing has been delivered yet.</p>
		{:else}
			<div class="ledger-scroll">
				<table class="ledger">
					<thead>
						<tr>
							<th>When</th>
							<th>Event</th>
							<th>Status</th>
							<th class="num">Took</th>
							<th class="num">Attempt</th>
							<th class="end">&nbsp;</th>
						</tr>
					</thead>
					<tbody>
						{#each deliveries as d (d.id)}
							<tr>
								<td class="mono">{fmtWhen(d.deliveredAt)}</td>
								<td><span class="caps soft">{webhookEventLabel[d.event]}</span></td>
								<td>
									{#if d.success}
										<Mark kind="ok" label={String(d.statusCode)} />
									{:else}
										<Mark kind="bad" label={d.statusCode ? String(d.statusCode) : 'failed'} />
									{/if}
								</td>
								<td class="num mono">{fmtDuration(d.durationMs)}</td>
								<td class="num mono">{d.attempt}</td>
								<td class="end">
									<button class="rowact plain" onclick={() => redeliver(d)}>redeliver</button>
								</td>
							</tr>
							{#if d.requestBody || d.responseBody}
								<tr>
									<td colspan="6" style="padding-top: 0">
										<details>
											<summary class="caps faint" style="cursor: pointer">exchange</summary>
											{#if d.requestBody}
												<pre class="tract" style="margin-top: 0.5rem">{d.requestBody}</pre>
											{/if}
											{#if d.responseBody}
												<pre class="tract" style="margin-top: 0.5rem">{d.responseBody}</pre>
											{/if}
										</details>
									</td>
								</tr>
							{/if}
						{/each}
					</tbody>
				</table>
			</div>
		{/if}
	</div>
{/if}

{#if formOpen}
	<form class="panel" onsubmit={submit}>
		<p class="panel-title">{editing ? 'Edit webhook' : 'New webhook'}</p>
		<label class="field">
			<span>Endpoint URL</span>
			<input type="url" bind:value={url} placeholder="https://…" required />
		</label>
		<label class="field">
			<span>Secret</span>
			<input type="password" bind:value={secret} placeholder={editing?.hasSecret ? 'unchanged unless set' : ''} />
			<span class="hint">Used to sign each delivery. {editing?.hasSecret ? 'A secret is on file.' : ''}</span>
		</label>
		<fieldset class="field">
			<span>Events</span>
			{#each allEvents as ev (ev)}
				<label class="tick">
					<input
						type="checkbox"
						checked={events.includes(ev)}
						onchange={(e) => toggleEvent(ev, e.currentTarget.checked)}
					/>
					{webhookEventLabel[ev]}
				</label>
			{/each}
		</fieldset>
		<label class="field">
			<span>Content type</span>
			<select bind:value={contentType}>
				<option value="application/json">application/json</option>
				<option value="application/x-www-form-urlencoded">application/x-www-form-urlencoded</option>
			</select>
		</label>
		<label class="field">
			<span>Payload template</span>
			<textarea bind:value={template} rows="4" placeholder="empty sends the standard payload"></textarea>
		</label>
		<label class="tick">
			<input type="checkbox" bind:checked={active} />
			Active
		</label>
		<div class="row gap-top">
			<button class="act wax" type="submit" disabled={busy}>{editing ? 'Save' : 'Create'}</button>
			<button class="rowact plain" type="button" onclick={() => (formOpen = false)}>cancel</button>
		</div>
	</form>
{:else}
	<div class="gap-top">
		<button class="act" onclick={startNew}>New webhook</button>
	</div>
{/if}

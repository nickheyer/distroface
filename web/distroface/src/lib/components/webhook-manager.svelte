<script lang="ts">
	import { onMount } from 'svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { pageToToken, relativeTime, webhookEventLabels } from '$lib/utils';
	import { toast } from 'svelte-sonner';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import {
		Table, TableBody, TableCell, TableHead, TableHeader, TableRow
	} from '$lib/components/ui/table';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import WebhookFormPanel from '$lib/components/webhook-form-panel.svelte';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import type { Webhook as WebhookType, WebhookDelivery } from '$lib/proto/distroface/v1/types_pb';
	import { WebhookEvent, WebhookScope } from '$lib/proto/distroface/v1/types_pb';
	import {
		Webhook, Plus, Pencil, Trash2, RotateCw, CircleCheck, CircleX, ChevronDown
	} from '@lucide/svelte';
	import isURL from 'validator/lib/isURL';

	let {
		scope,
		scopeId,
		emptyDescription = 'Add a webhook to get notified of push, pull, and delete events.',
		createDescription = 'Receive HTTP POST notifications for events.'
	}: {
		scope: WebhookScope;
		scopeId: string;
		emptyDescription?: string;
		createDescription?: string;
	} = $props();

	// ── List state ──────────────────────────────────────────────────────
	let webhooks = $state<WebhookType[]>([]);
	let loading = $state(false);
	let totalCount = $state(0);
	let currentPage = $state(1);
	const pageSize = 20;

	// ── Create state ────────────────────────────────────────────────────
	let createOpen = $state(false);
	let newUrl = $state('');
	let newSecret = $state('');
	let newActive = $state(true);
	let newEvents = $state<WebhookEvent[]>([WebhookEvent.PUSH]);
	let newPayloadTemplate = $state('');
	let creating = $state(false);

	// ── Edit state ──────────────────────────────────────────────────────
	let editOpen = $state(false);
	let editTarget = $state<WebhookType | null>(null);
	let editUrl = $state('');
	let editSecret = $state('');
	let editActive = $state(true);
	let editEvents = $state<WebhookEvent[]>([]);
	let editPayloadTemplate = $state('');
	let saving = $state(false);

	// ── Delete state ────────────────────────────────────────────────────
	let deleteOpen = $state(false);
	let deleteTarget = $state<WebhookType | null>(null);
	let deleting = $state(false);

	// ── Delivery state ──────────────────────────────────────────────────
	let expandedId = $state<string | null>(null);
	let deliveries = $state<WebhookDelivery[]>([]);
	let deliveriesLoading = $state(false);
	let redelivering = $state<string | null>(null);


	const isValidUrl = (v: string) => isURL(v, { protocols: ['http', 'https'], require_protocol: true, require_tld: false });

	// ── Handlers ────────────────────────────────────────────────────────
	async function load() {
		loading = true;
		try {
			const listReq: Record<string, unknown> = {
				pageSize,
				pageToken: pageToToken(currentPage, pageSize)
			};
			if (scope === WebhookScope.REPOSITORY) listReq.repoId = scopeId;
			else listReq.orgId = scopeId;

			const resp = await rpcClient.webhook.listWebhooks(listReq);
			webhooks = resp.webhooks;
			totalCount = Number(resp.totalCount);
		} catch { webhooks = []; }
		finally { loading = false; }
	}

	function openCreate() {
		newUrl = '';
		newSecret = '';
		newActive = true;
		newEvents = [WebhookEvent.PUSH];
		newPayloadTemplate = '';
		createOpen = true;
	}

	async function submitCreate() {
		creating = true;
		try {
			const createReq: Record<string, unknown> = {
				scope,
				url: newUrl,
				secret: newSecret,
				events: newEvents,
				active: newActive,
				contentType: 'application/json',
				payloadTemplate: newPayloadTemplate
			};
			if (scope === WebhookScope.REPOSITORY) createReq.repoId = scopeId;
			else createReq.orgId = scopeId;

			await rpcClient.webhook.createWebhook(createReq);
			createOpen = false;
			toast.success('Webhook created');
			load();
		} catch { /* error interceptor */ }
		finally { creating = false; }
	}

	function openEdit(wh: WebhookType) {
		editTarget = wh;
		editUrl = wh.url;
		editSecret = '';
		editActive = wh.active;
		editEvents = [...wh.events];
		editPayloadTemplate = wh.payloadTemplate;
		editOpen = true;
	}

	async function submitEdit() {
		if (!editTarget) return;
		saving = true;
		try {
			await rpcClient.webhook.updateWebhook({
				id: editTarget.id,
				url: editUrl,
				secret: editSecret || undefined,
				events: editEvents,
				active: editActive,
				contentType: 'application/json',
				payloadTemplate: editPayloadTemplate
			});
			editOpen = false;
			toast.success('Webhook updated');
			load();
		} catch { /* error interceptor */ }
		finally { saving = false; }
	}

	function confirmDelete(wh: WebhookType) {
		deleteTarget = wh;
		deleteOpen = true;
	}

	async function doDelete() {
		if (!deleteTarget) return;
		deleting = true;
		try {
			await rpcClient.webhook.deleteWebhook({ id: deleteTarget.id });
			deleteOpen = false;
			toast.success('Webhook deleted');
			if (expandedId === deleteTarget.id) expandedId = null;
			load();
		} catch { /* error interceptor */ }
		finally { deleting = false; }
	}

	async function toggleExpand(whId: string) {
		if (expandedId === whId) { expandedId = null; return; }
		expandedId = whId;
		deliveriesLoading = true;
		try {
			const resp = await rpcClient.webhook.listWebhookDeliveries({ webhookId: whId, pageSize: 10 });
			deliveries = resp.deliveries;
		} catch { deliveries = []; }
		finally { deliveriesLoading = false; }
	}

	async function redeliver(deliveryId: string) {
		redelivering = deliveryId;
		try {
			await rpcClient.webhook.redeliverWebhook({ deliveryId });
			toast.success('Redelivery triggered');
			if (expandedId) {
				const resp = await rpcClient.webhook.listWebhookDeliveries({ webhookId: expandedId, pageSize: 10 });
				deliveries = resp.deliveries;
			}
		} catch { /* error interceptor */ }
		finally { redelivering = null; }
	}

	onMount(() => { load(); });
</script>

<div class="space-y-4">
	<div class="section-header">
		<div>
			<h2 class="section-title">Webhooks</h2>
		</div>
		<Button size="sm" onclick={openCreate}>
			<Plus class="h-4 w-4 mr-1.5" />Add Webhook
		</Button>
	</div>

	{#if loading}
		<div class="space-y-2">
			{#each { length: 2 }, i (i)}
				<Skeleton class="h-14 w-full rounded-xl" />
			{/each}
		</div>
	{:else if webhooks.length === 0}
		<EmptyState icon={Webhook} message="No webhooks" description={emptyDescription}>
			{#snippet actions()}
				<Button variant="outline" size="sm" onclick={openCreate}>
					<Plus class="h-3.5 w-3.5 mr-1.5" />Add Webhook
				</Button>
			{/snippet}
		</EmptyState>
	{:else}
		<div class="data-table">
			<Table class="table-fixed">
				<TableHeader>
					<TableRow>
						<TableHead class="th w-10"></TableHead>
						<TableHead class="th">URL</TableHead>
						<TableHead class="th w-40">Events</TableHead>
						<TableHead class="th w-20 text-center">Status</TableHead>
						<TableHead class="th w-24 text-right">Actions</TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{#each webhooks as wh (wh.id)}
						<TableRow class="cursor-pointer group/row" onclick={() => toggleExpand(wh.id)}>
							<TableCell class="py-3 px-3">
								<ChevronDown class="h-3.5 w-3.5 text-muted-foreground/50 transition-transform {expandedId === wh.id ? 'rotate-180' : ''}" />
							</TableCell>
							<TableCell class="py-3 px-3">
								<span class="font-mono text-xs text-muted-foreground truncate block">{wh.url}</span>
							</TableCell>
							<TableCell class="py-3 px-3">
								<div class="flex flex-wrap gap-1">
									{#each wh.events as ev (ev)}
										<Badge variant="outline" class="text-[10px] py-0 h-4.5">{webhookEventLabels[ev] ?? 'unknown'}</Badge>
									{/each}
								</div>
							</TableCell>
							<TableCell class="py-3 px-3 text-center">
								{#if wh.active}
									<Badge variant="secondary" class="text-[10px] py-0 h-4.5 text-green-600 dark:text-green-400">Active</Badge>
								{:else}
									<Badge variant="secondary" class="text-[10px] py-0 h-4.5 text-muted-foreground">Inactive</Badge>
								{/if}
							</TableCell>
							<TableCell class="py-3 px-3 text-right" onclick={(e: MouseEvent) => e.stopPropagation()}>
								<div class="flex items-center justify-end gap-1">
									<Button variant="ghost" size="icon" class="h-7 w-7" onclick={() => openEdit(wh)}>
										<Pencil class="h-3 w-3" />
									</Button>
									<Button variant="ghost" size="icon" class="h-7 w-7 text-destructive" onclick={() => confirmDelete(wh)}>
										<Trash2 class="h-3 w-3" />
									</Button>
								</div>
							</TableCell>
						</TableRow>

						{#if expandedId === wh.id}
							<TableRow>
								<TableCell colspan={5} class="p-0">
									<div class="bg-muted/20 border-t border-border/40 px-5 py-4">
										<h4 class="text-xs font-medium text-muted-foreground mb-3">Recent Deliveries</h4>
										{#if deliveriesLoading}
											<div class="space-y-2">
												<Skeleton class="h-8 w-full" />
												<Skeleton class="h-8 w-full" />
											</div>
										{:else if deliveries.length === 0}
											<p class="text-xs text-muted-foreground/60 py-2">No deliveries yet</p>
										{:else}
											<div class="space-y-1.5">
												{#each deliveries as d (d.id)}
													<div class="flex items-center gap-3 text-xs rounded-lg bg-background border border-border/40 px-3 py-2">
														{#if d.success}
															<CircleCheck class="h-3.5 w-3.5 text-green-500 shrink-0" />
														{:else}
															<CircleX class="h-3.5 w-3.5 text-destructive shrink-0" />
														{/if}
														<Badge variant="outline" class="text-[10px] py-0 h-4.5 font-mono">{webhookEventLabels[d.event] ?? 'unknown'}</Badge>
														<span class="font-mono text-muted-foreground">{d.statusCode || '---'}</span>
														<span class="text-muted-foreground/50">{d.durationMs}ms</span>
														{#if d.deliveredAt}
															<span class="text-muted-foreground/50 ml-auto">{relativeTime(timestampDate(d.deliveredAt))}</span>
														{/if}
														<Button
															variant="ghost" size="icon" class="h-6 w-6 shrink-0"
															disabled={redelivering === d.id}
															onclick={(e: MouseEvent) => { e.stopPropagation(); redeliver(d.id); }}
														>
															<RotateCw class="h-3 w-3 {redelivering === d.id ? 'animate-spin' : ''}" />
														</Button>
													</div>
												{/each}
											</div>
										{/if}
									</div>
								</TableCell>
							</TableRow>
						{/if}
					{/each}
				</TableBody>
			</Table>
		</div>

		<DataPagination
			page={currentPage} pageSize={pageSize} totalCount={totalCount}
			onPrev={() => { if (currentPage > 1) { currentPage--; load(); } }}
			onNext={() => { if (currentPage * pageSize < totalCount) { currentPage++; load(); } }}
		/>
	{/if}
</div>

<!-- Create Webhook Panel -->
<WebhookFormPanel
	bind:open={createOpen}
	title="Add Webhook"
	description={createDescription}
	formMode="create"
	bind:url={newUrl}
	bind:secret={newSecret}
	bind:events={newEvents}
	bind:payloadTemplate={newPayloadTemplate}
	bind:active={newActive}
>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (createOpen = false)}>Cancel</Button>
		<Button onclick={submitCreate} disabled={creating || !isValidUrl(newUrl) || newEvents.length === 0}>
			{creating ? 'Creating...' : 'Create Webhook'}
		</Button>
	{/snippet}
</WebhookFormPanel>

<!-- Edit Webhook Panel -->
<WebhookFormPanel
	bind:open={editOpen}
	title="Edit Webhook"
	description="Update webhook configuration."
	formMode="edit"
	idPrefix="wh-edit"
	bind:url={editUrl}
	bind:secret={editSecret}
	bind:events={editEvents}
	bind:payloadTemplate={editPayloadTemplate}
	bind:active={editActive}
>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (editOpen = false)}>Cancel</Button>
		<Button onclick={submitEdit} disabled={saving || !isValidUrl(editUrl) || editEvents.length === 0}>
			{saving ? 'Saving...' : 'Save Changes'}
		</Button>
	{/snippet}
</WebhookFormPanel>

<!-- Delete Webhook -->
<ConfirmDialog bind:open={deleteOpen} title="Delete Webhook" confirmLabel="Delete" onConfirm={doDelete} loading={deleting} icon={Trash2}>
	{#snippet description()}
		Are you sure you want to delete this webhook? All delivery history will be lost. This action cannot be undone.
	{/snippet}
</ConfirmDialog>

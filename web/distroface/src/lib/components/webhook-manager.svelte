<script lang="ts">
	import { onMount } from 'svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { relativeTime, webhookEventLabels } from '$lib/utils';
	import { Pager } from '$lib/pager.svelte';
	import { toast } from 'svelte-sonner';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import {
		Table,
		TableBody,
		TableCell,
		TableHead,
		TableHeader,
		TableRow
	} from '$lib/components/ui/table';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import QueryFilterBar from '$lib/components/query-filter.svelte';
	import { QueryFilter } from '$lib/query.svelte';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import type { Webhook as WebhookType, WebhookDelivery } from '$lib/proto/distroface/v1/types_pb';
	import { WebhookScope } from '$lib/proto/distroface/v1/types_pb';
	import {
		Webhook,
		Plus,
		Pencil,
		Trash2,
		RotateCw,
		CircleCheck,
		CircleX,
		ChevronDown
	} from '@lucide/svelte';

	let {
		scope,
		scopeId,
		newHref,
		editHref,
    hideHeaderText = false,
    hideCreateButton = false,
		emptyDescription = 'Add a webhook to get notified of push, pull, and delete events.',
		createDescription = 'Receive HTTP POST notifications for events.'
	}: {
		scope: WebhookScope;
		scopeId: string;
		newHref: string;
		editHref: (id: string) => string;
    hideHeaderText?: boolean;
    hideCreateButton?: boolean;
		emptyDescription?: string;
		createDescription?: string;
	} = $props();

	let webhooks = $state<WebhookType[]>([]);
	let loading = $state(false);
	let loaded = $state(false);
	const pager = new Pager(20);
	const filter = new QueryFilter([
		{ key: 'url', label: 'URL' },
		{ key: 'events', label: 'Events' },
		{ key: 'created_by', label: 'Created By' }
	]);

	let deleteOpen = $state(false);
	let deleteTarget = $state<WebhookType | null>(null);
	let deleting = $state(false);

	let expandedId = $state<string | null>(null);
	let deliveries = $state<WebhookDelivery[]>([]);
	let deliveriesLoading = $state(false);
	let redelivering = $state<string | null>(null);

	async function load() {
		loading = true;
		try {
			const listReq: Record<string, unknown> = {
				page: pager.request(filter.request())
			};
			if (scope === WebhookScope.ORGANIZATION) listReq.orgId = scopeId;
			else listReq.repoId = scopeId;

			const resp = await rpcClient.webhook.listWebhooks(listReq);
			webhooks = resp.webhooks;
			pager.apply(resp.page);
		} catch {
			webhooks = [];
		} finally {
			loading = false;
			loaded = true;
		}
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
		} catch {
			/* error interceptor */
		} finally {
			deleting = false;
		}
	}

	async function toggleExpand(whId: string) {
		if (expandedId === whId) {
			expandedId = null;
			return;
		}
		expandedId = whId;
		deliveriesLoading = true;
		try {
			const resp = await rpcClient.webhook.listWebhookDeliveries({
				page: { pageSize: 10 },
				webhookId: whId
			});
			deliveries = resp.deliveries;
		} catch {
			deliveries = [];
		} finally {
			deliveriesLoading = false;
		}
	}

	async function redeliver(deliveryId: string) {
		redelivering = deliveryId;
		try {
			await rpcClient.webhook.redeliverWebhook({ deliveryId });
			toast.success('Redelivery triggered');
			if (expandedId) {
				const resp = await rpcClient.webhook.listWebhookDeliveries({
					page: { pageSize: 10 },
					webhookId: expandedId
				});
				deliveries = resp.deliveries;
			}
		} catch {
			/* error interceptor */
		} finally {
			redelivering = null;
		}
	}

	function filterChanged() {
		pager.reset();
		load();
	}

	onMount(() => {
		load();
	});
</script>

<div class="space-y-4">
	<div class="section-header">
    {#if !hideHeaderText}
      <div class="min-w-0">
        <h2 class="section-title">Webhooks</h2>
        <p class="section-subtitle">{createDescription}</p>
      </div>
    {/if}
    {#if !hideCreateButton}
      <Button class="shrink-0" href={newHref}>
        <Plus />
        Add webhook
      </Button>
    {/if}
	</div>

	<div class="max-w-md">
		<QueryFilterBar {filter} placeholder="Search webhooks" onchange={filterChanged} />
	</div>

	{#if !loaded}
		<div class="box divide-y">
			{#each { length: 2 }, i (i)}
				<div class="flex items-center gap-3 px-4 py-3">
					<Skeleton class="size-3.5" />
					<Skeleton class="h-4 flex-1" />
					<Skeleton class="h-5 w-16" />
					<Skeleton class="h-5 w-14" />
				</div>
			{/each}
		</div>
	{:else if webhooks.length === 0}
		<EmptyState
			icon={Webhook}
			message={filter.active ? 'No matching webhooks' : 'No webhooks yet'}
			description={filter.active ? 'No results match the current filter' : emptyDescription}
		>
			{#snippet actions()}
				{#if !filter.active}
					<Button variant="outline" href={newHref}>
						<Plus />
						Add webhook
					</Button>
				{/if}
			{/snippet}
		</EmptyState>
	{:else}
		<div class="data-table transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
			<Table class="table-fixed">
				<TableHeader>
					<TableRow>
						<TableHead class="w-10"></TableHead>
						<TableHead>URL</TableHead>
						<TableHead class="w-40">Events</TableHead>
						<TableHead class="w-20 text-center">Status</TableHead>
						<TableHead class="w-24 text-right">Actions</TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{#each webhooks as wh (wh.id)}
						<TableRow class="cursor-pointer" onclick={() => toggleExpand(wh.id)}>
							<TableCell>
								<ChevronDown
									class="size-3.5 text-muted-foreground transition-transform {expandedId === wh.id
										? 'rotate-180'
										: ''}"
								/>
							</TableCell>
							<TableCell>
								<span class="block truncate font-mono text-sm">{wh.url}</span>
							</TableCell>
							<TableCell>
								<div class="flex flex-wrap gap-1">
									{#each wh.events as ev (ev)}
										<Badge variant="outline">{webhookEventLabels[ev] ?? 'unknown'}</Badge>
									{/each}
								</div>
							</TableCell>
							<TableCell class="text-center">
								{#if wh.active}
									<Badge variant="success">Active</Badge>
								{:else}
									<Badge variant="secondary">Inactive</Badge>
								{/if}
							</TableCell>
							<TableCell class="text-right" onclick={(e: MouseEvent) => e.stopPropagation()}>
								<div class="flex items-center justify-end gap-1">
									<Button
										variant="ghost-muted"
										size="icon-sm"
										href={editHref(wh.id)}
										aria-label="Edit webhook"
									>
										<Pencil />
									</Button>
									<Button
										variant="ghost-muted"
										size="icon-sm"
										class="hover:text-destructive"
										onclick={() => confirmDelete(wh)}
										aria-label="Delete webhook"
									>
										<Trash2 />
									</Button>
								</div>
							</TableCell>
						</TableRow>

						{#if expandedId === wh.id}
							<TableRow>
								<TableCell colspan={5} class="p-0">
									<div class="bg-muted/40 px-5 py-4">
										<p class="eyebrow mb-3">Recent deliveries</p>
										{#if deliveriesLoading}
											<div class="box divide-y">
												{#each { length: 2 }, i (i)}
													<div class="flex items-center gap-3 px-3 py-2">
														<Skeleton class="size-3.5" />
														<Skeleton class="h-4 w-full" />
													</div>
												{/each}
											</div>
										{:else if deliveries.length === 0}
											<p class="py-2 text-xs text-muted-foreground">No deliveries yet</p>
										{:else}
											<div class="box divide-y">
												{#each deliveries as d (d.id)}
													<div class="flex items-center gap-3 px-3 py-2 text-xs">
														{#if d.success}
															<CircleCheck class="size-3.5 shrink-0 text-success" />
														{:else}
															<CircleX class="size-3.5 shrink-0 text-destructive" />
														{/if}
														<Badge variant="outline" class="font-mono"
															>{webhookEventLabels[d.event] ?? 'unknown'}</Badge
														>
														<span class="font-mono text-muted-foreground"
															>{d.statusCode || '---'}</span
														>
														<span class="text-muted-foreground tabular-nums">{d.durationMs}ms</span>
														{#if d.deliveredAt}
															<span class="ml-auto text-muted-foreground">
																{relativeTime(timestampDate(d.deliveredAt))}
															</span>
														{/if}
														<Button
															variant="ghost-muted"
															size="icon-xs"
															class="shrink-0"
															disabled={redelivering === d.id}
															onclick={(e: MouseEvent) => {
																e.stopPropagation();
																redeliver(d.id);
															}}
															aria-label="Redeliver"
														>
															<RotateCw class={redelivering === d.id ? 'animate-spin' : ''} />
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
			<DataPagination attached {pager} onChange={load} />
		</div>
	{/if}
</div>

<ConfirmDialog
	bind:open={deleteOpen}
	title="Delete webhook"
	confirmLabel="Delete"
	onConfirm={doDelete}
	loading={deleting}
	icon={Trash2}
>
	{#snippet description()}
		This permanently deletes <strong>{deleteTarget?.url}</strong> and its delivery history.
	{/snippet}
</ConfirmDialog>

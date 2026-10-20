<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge, type BadgeVariant } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import {
		Table,
		TableBody,
		TableCell,
		TableHead,
		TableHeader,
		TableRow
	} from '$lib/components/ui/table';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import MailSettingsCard from '$lib/components/git/mail-settings-card.svelte';
	import { Mail, RotateCw, Send } from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { toast } from 'svelte-sonner';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime } from '$lib/utils';
	import { Pager } from '$lib/pager.svelte';
	import { systemScope } from '$lib/settings-utils';
	import { MailStatus, type MailDelivery } from '$lib/proto/distroface/v1/mail_pb';

	const statusOptions = [
		{ value: MailStatus.UNSPECIFIED, label: 'All statuses' },
		{ value: MailStatus.QUEUED, label: 'Queued' },
		{ value: MailStatus.SENT, label: 'Sent' },
		{ value: MailStatus.FAILED, label: 'Failed' }
	];

	let deliveries = $state<MailDelivery[]>([]);
	const pager = new Pager(20);
	let loading = $state(true);
	let loaded = $state(false);
	let status = $state<MailStatus>(MailStatus.UNSPECIFIED);
	let retrying = $state<Record<string, boolean>>({});

	const canEdit = $derived(authStore.canUpdateSettings);
	const statusLabel = $derived(
		statusOptions.find((o) => o.value === status)?.label ?? 'All statuses'
	);

	async function loadDeliveries() {
		loading = true;
		try {
			const resp = await rpcClient.mail.listMailDeliveries({
				page: pager.request(),
				scope: systemScope,
				status
			});
			deliveries = resp.deliveries;
			pager.apply(resp.page);
		} catch {
			deliveries = [];
		} finally {
			loading = false;
			loaded = true;
		}
	}

	function changeStatus(v: string) {
		status = Number(v) as MailStatus;
		pager.reset();
		loadDeliveries();
	}

	async function retry(d: MailDelivery) {
		retrying[d.id] = true;
		try {
			await rpcClient.mail.retryMailDelivery({ id: d.id });
			toast.success('Message requeued');
			await loadDeliveries();
		} catch {
			// error interceptor
		} finally {
			delete retrying[d.id];
		}
	}

	function badgeVariant(s: MailStatus): BadgeVariant {
		if (s === MailStatus.SENT) return 'success';
		if (s === MailStatus.FAILED) return 'destructive';
		return 'secondary';
	}

	function statusText(s: MailStatus): string {
		return statusOptions.find((o) => o.value === s)?.label ?? 'Unknown';
	}

	onMount(() => {
		if (!authStore.hasPermission('settings', 'read')) {
			goto(resolve('/admin'));
			return;
		}
		loadDeliveries();
	});
</script>

<div class="space-y-10">
	<FormPage
		level="section"
		title="Mail"
		description="The relay notifications leave through and the mailbox replies come back to."
	>
		<MailSettingsCard scope={systemScope} {canEdit} />
	</FormPage>

	<div class="space-y-4 border-t pt-8">
		<div class="section-header flex-wrap">
			<div class="min-w-0">
				<h2 class="section-title">Deliveries</h2>
				<p class="section-subtitle">Queued, sent, and failed messages, newest first.</p>
			</div>
			<div class="flex shrink-0 items-center gap-2">
				<Select type="single" value={String(status)} onValueChange={changeStatus}>
					<SelectTrigger class="w-40" aria-label="Status">{statusLabel}</SelectTrigger>
					<SelectContent>
						{#each statusOptions as o (o.value)}
							<SelectItem value={String(o.value)}>{o.label}</SelectItem>
						{/each}
					</SelectContent>
				</Select>
				<Button
					variant="ghost-muted"
					size="icon"
					onclick={loadDeliveries}
					disabled={loading}
					aria-label="Refresh"
				>
					<RotateCw class={loading ? 'animate-spin' : ''} />
				</Button>
			</div>
		</div>

		{#if !loaded}
			<div class="box divide-y">
				{#each { length: 3 }, i (i)}
					<div class="flex items-center gap-4 px-4 py-3">
						<div class="flex-1 space-y-2">
							<Skeleton class="h-4 w-56" />
							<Skeleton class="h-3 w-40" />
						</div>
						<Skeleton class="h-5 w-16 rounded-full" />
					</div>
				{/each}
			</div>
		{:else if deliveries.length === 0}
			<EmptyState
				icon={Mail}
				message="No deliveries yet"
				description="Messages appear here once the mailer queues them."
			/>
		{:else}
			<div class="data-table transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
				<Table>
					<TableHeader>
						<TableRow>
							<TableHead>Message</TableHead>
							<TableHead class="w-24">Status</TableHead>
							<TableHead class="hidden w-20 md:table-cell">Attempts</TableHead>
							<TableHead class="hidden w-28 sm:table-cell">Created</TableHead>
							<TableHead class="hidden w-28 md:table-cell">Sent</TableHead>
							{#if canEdit}
								<TableHead class="w-20"></TableHead>
							{/if}
						</TableRow>
					</TableHeader>
					<TableBody>
						{#each deliveries as d (d.id)}
							<TableRow>
								<TableCell>
									<p class="max-w-56 truncate font-medium sm:max-w-md">
										{d.subject || '(no subject)'}
									</p>
									<p class="mt-0.5 max-w-56 truncate text-xs text-muted-foreground sm:max-w-md">
										to {d.to}
										{#if d.scope && d.scope.scopeId}
											<span class="mx-1 text-muted-foreground/50">/</span> scope {d.scope.scopeId}
										{/if}
									</p>
									{#if d.lastError}
										<p class="mt-1 text-xs break-all whitespace-normal text-destructive">
											{d.lastError}
										</p>
									{/if}
								</TableCell>
								<TableCell>
									<Badge variant={badgeVariant(d.status)}>{statusText(d.status)}</Badge>
								</TableCell>
								<TableCell class="hidden text-sm text-muted-foreground tabular-nums md:table-cell"
									>{d.attempts}</TableCell
								>
								<TableCell class="hidden text-sm text-muted-foreground sm:table-cell">
									{d.createdAt ? relativeTime(timestampDate(d.createdAt)) : '-'}
								</TableCell>
								<TableCell class="hidden text-sm text-muted-foreground md:table-cell">
									{d.sentAt ? relativeTime(timestampDate(d.sentAt)) : '-'}
								</TableCell>
								{#if canEdit}
									<TableCell class="text-right">
										{#if d.status === MailStatus.FAILED}
											<Button
												variant="ghost"
												size="sm"
												disabled={!!retrying[d.id]}
												onclick={() => retry(d)}
											>
												<Send />
												Retry
											</Button>
										{/if}
									</TableCell>
								{/if}
							</TableRow>
						{/each}
					</TableBody>
				</Table>
				<DataPagination attached {pager} onChange={loadDeliveries} />
			</div>
		{/if}
	</div>
</div>

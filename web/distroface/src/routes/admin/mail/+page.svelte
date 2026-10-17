<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import {
		Table, TableBody, TableCell, TableHead, TableHeader, TableRow
	} from '$lib/components/ui/table';
	import {
		Select, SelectContent, SelectItem, SelectTrigger
	} from '$lib/components/ui/select';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
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
	const statusLabel = $derived(statusOptions.find((o) => o.value === status)?.label ?? 'All statuses');

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

	function badgeVariant(s: MailStatus): 'default' | 'secondary' | 'destructive' | 'outline' {
		if (s === MailStatus.SENT) return 'default';
		if (s === MailStatus.FAILED) return 'destructive';
		return 'secondary';
	}

	function statusText(s: MailStatus): string {
		return statusOptions.find((o) => o.value === s)?.label ?? 'Unknown';
	}

	onMount(() => {
		if (!authStore.hasPermission('settings', 'read')) { goto(resolve('/admin')); return; }
		loadDeliveries();
	});
</script>

<div class="space-y-8">
	<MailSettingsCard scope={systemScope} {canEdit} />

	<div class="space-y-4">
		<div class="section-header flex-wrap">
			<div class="min-w-0">
				<h2 class="section-title">Deliveries</h2>
				<p class="section-subtitle">Queued, sent, and failed messages, newest first.</p>
			</div>
			<div class="flex items-center gap-2 shrink-0">
				<Select type="single" value={String(status)} onValueChange={changeStatus}>
					<SelectTrigger class="w-40 h-8" size="sm" aria-label="Status">{statusLabel}</SelectTrigger>
					<SelectContent>
						{#each statusOptions as o (o.value)}
							<SelectItem value={String(o.value)}>{o.label}</SelectItem>
						{/each}
					</SelectContent>
				</Select>
				<Button variant="ghost" size="icon" class="h-8 w-8" onclick={loadDeliveries} disabled={loading} aria-label="Refresh">
					<RotateCw class="h-3.5 w-3.5 {loading ? 'animate-spin' : ''}" />
				</Button>
			</div>
		</div>

		{#if !loaded}
			<Skeleton class="h-32 w-full rounded-lg" />
		{:else if deliveries.length === 0}
			<EmptyState icon={Mail} message="No deliveries" description="Messages appear here once the mailer queues them." />
		{:else}
			<div class="data-table transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
				<Table>
					<TableHeader>
						<TableRow class="bg-muted/30 hover:bg-muted/30">
							<TableHead class="th">Message</TableHead>
							<TableHead class="th w-24">Status</TableHead>
							<TableHead class="th w-20 hidden md:table-cell">Attempts</TableHead>
							<TableHead class="th w-28 hidden sm:table-cell">Created</TableHead>
							<TableHead class="th w-28 hidden md:table-cell">Sent</TableHead>
							{#if canEdit}
								<TableHead class="th w-20"></TableHead>
							{/if}
						</TableRow>
					</TableHeader>
					<TableBody>
						{#each deliveries as d (d.id)}
							<TableRow>
								<TableCell class="py-3 px-3">
									<p class="font-medium truncate max-w-56 sm:max-w-md">{d.subject || '(no subject)'}</p>
									<p class="text-[12px] text-muted-foreground mt-0.5 truncate max-w-56 sm:max-w-md">
										to {d.to}
										{#if d.scope && d.scope.scopeId}
											<span class="mx-1">/</span> scope {d.scope.scopeId}
										{/if}
									</p>
									{#if d.lastError}
										<p class="text-[12px] text-destructive mt-1 break-all">{d.lastError}</p>
									{/if}
								</TableCell>
								<TableCell class="py-3 px-3">
									<Badge variant={badgeVariant(d.status)} class="text-[11px]">{statusText(d.status)}</Badge>
								</TableCell>
								<TableCell class="text-sm text-muted-foreground py-3 px-3 tabular-nums hidden md:table-cell">{d.attempts}</TableCell>
								<TableCell class="text-sm text-muted-foreground py-3 px-3 hidden sm:table-cell">
									{d.createdAt ? relativeTime(timestampDate(d.createdAt)) : '-'}
								</TableCell>
								<TableCell class="text-sm text-muted-foreground py-3 px-3 hidden md:table-cell">
									{d.sentAt ? relativeTime(timestampDate(d.sentAt)) : '-'}
								</TableCell>
								{#if canEdit}
									<TableCell class="text-right py-3 px-3">
										{#if d.status === MailStatus.FAILED}
											<Button variant="ghost" size="sm" class="h-7" disabled={!!retrying[d.id]} onclick={() => retry(d)}>
												<Send class="h-3.5 w-3.5 mr-1.5" />
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

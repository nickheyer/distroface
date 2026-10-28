<script lang="ts">
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
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
	import { Key, Plus, Trash, ShieldCheck } from '$lib/icons';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import { toast } from 'svelte-sonner';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime } from '$lib/utils';
	import { Pager } from '$lib/pager.svelte';
	import { QueryFilter } from '$lib/query.svelte';
	import type { APIToken } from '$lib/proto/distroface/v1/types_pb';

	let tokens = $state<APIToken[]>([]);
	let loading = $state(true);
	let loaded = $state(false);
	const pager = new Pager(20);
	const filter = new QueryFilter([{ key: 'name', label: 'Name' }]);
	const newHref = resolve('/settings/tokens/new');

	let revokeDialogOpen = $state(false);
	let revokeTarget = $state<APIToken | null>(null);
	let revoking = $state(false);

	async function loadTokens() {
		loading = true;
		try {
			const resp = await rpcClient.token.listAPITokens({
				page: pager.request(filter.request())
			});
			tokens = resp.tokens;
			pager.apply(resp.page);
		} catch {
			// error interceptor
		} finally {
			loading = false;
			loaded = true;
		}
	}

	function filterChanged() {
		pager.reset();
		loadTokens();
	}

	function openRevoke(token: APIToken) {
		revokeTarget = token;
		revokeDialogOpen = true;
	}

	async function confirmRevoke() {
		if (!revokeTarget) return;
		revoking = true;
		try {
			await rpcClient.token.deleteAPIToken({ id: revokeTarget.id });
			toast.success('Token revoked');
			revokeDialogOpen = false;
			await loadTokens();
		} catch {
			// error interceptor
		} finally {
			revoking = false;
		}
	}

	onMount(loadTokens);
</script>

<div class="space-y-6">
	<div class="section-header flex-wrap">
		<div class="min-w-0">
			<h2 class="section-title">API tokens</h2>
			<p class="section-subtitle">
				Personal access tokens for API and Docker registry authentication.
			</p>
		</div>
		<div class="flex shrink-0 items-center gap-2">
			<div class="w-80">
				<QueryFilterBar {filter} placeholder="Search tokens" onchange={filterChanged} />
			</div>
			<PermissionGate resource="tokens" action="create">
				<Button href={newHref}>
					<Plus />
					Create token
				</Button>
			</PermissionGate>
		</div>
	</div>

	{#if !loaded}
		<div class="box divide-y">
			{#each { length: 3 }, i (i)}
				<div class="flex items-center gap-4 px-4 py-3">
					<Skeleton class="h-4 w-40" />
					<Skeleton class="h-4 w-24" />
					<Skeleton class="h-4 w-20 rounded-full" />
					<Skeleton class="ml-auto h-4 w-16" />
				</div>
			{/each}
		</div>
	{:else if tokens.length === 0}
		<EmptyState
			icon={Key}
			message={filter.active ? 'No matching tokens' : 'No API tokens yet'}
			description={filter.active
				? 'Search matches the token name.'
				: 'Create a token to authenticate with the API and Docker registry.'}
		>
			{#snippet actions()}
				<PermissionGate resource="tokens" action="create">
					<Button variant="outline" href={newHref}>
						<Plus />
						Create token
					</Button>
				</PermissionGate>
			{/snippet}
		</EmptyState>
	{:else}
		<div class="data-table transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
			<Table>
				<TableHeader>
					<TableRow>
						<TableHead>Name</TableHead>
						<TableHead>Created</TableHead>
						<TableHead>Expires</TableHead>
						<TableHead>Last used</TableHead>
						<TableHead class="w-16"></TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{#each tokens as token (token.id)}
						<TableRow>
							<TableCell class="font-medium">
								<div class="flex items-center gap-2">
									{token.name}
									{#if token.createdBy && token.createdBy !== authStore.user?.id}
										<span class="flex items-center gap-1 text-xs text-muted-foreground">
											<ShieldCheck class="size-3.5" />Managed
										</span>
									{/if}
								</div>
							</TableCell>
							<TableCell class="text-sm text-muted-foreground">
								{token.createdAt ? relativeTime(timestampDate(token.createdAt)) : '-'}
							</TableCell>
							<TableCell class="text-sm">
								{#if token.expiresAt}
									{@const expires = timestampDate(token.expiresAt)}
									{@const isExpired = expires < new Date()}
									<Badge variant={isExpired ? 'destructive' : 'outline'}>
										{isExpired ? 'Expired' : relativeTime(expires).replace(' ago', '')}
									</Badge>
								{:else}
									<Badge variant="secondary">Never</Badge>
								{/if}
							</TableCell>
							<TableCell class="text-sm text-muted-foreground">
								{token.lastUsedAt ? relativeTime(timestampDate(token.lastUsedAt)) : 'Never'}
							</TableCell>
							<PermissionGate resource="tokens" action="delete">
								<TableCell class="text-right">
									<Button
										variant="ghost-muted"
										size="icon-sm"
										class="text-destructive hover:text-destructive"
										aria-label="Revoke {token.name}"
										onclick={() => openRevoke(token)}
									>
										<Trash />
									</Button>
								</TableCell>
							</PermissionGate>
						</TableRow>
					{/each}
				</TableBody>
			</Table>
			<DataPagination attached {pager} onChange={loadTokens} />
		</div>
	{/if}
</div>

<ConfirmDialog
	bind:open={revokeDialogOpen}
	title="Revoke token"
	confirmLabel="Revoke"
	onConfirm={confirmRevoke}
	loading={revoking}
>
	{#snippet description()}
		Are you sure you want to revoke <strong>{revokeTarget?.name}</strong>? Any applications using
		this token will lose access immediately.
	{/snippet}
</ConfirmDialog>

<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { toast } from 'svelte-sonner';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime } from '$lib/utils';
	import { Pager } from '$lib/pager.svelte';
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
	import { Key, Plus, Trash, Lock, PenLine } from '$lib/icons';
	import type { APIToken } from '$lib/proto/distroface/v1/types_pb';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const fullName = $derived(`${namespace}/${name}`);
	const repo = $derived(gitRepoStore.repo);
	const newHref = $derived(resolve('/[namespace]/[name]/settings/tokens/new', { namespace, name }));

	let tokens = $state<APIToken[]>([]);
	let loading = $state(false);
	let loaded = $state(false);
	let loadedFor = $state('');
	const pager = new Pager(20);

	let revokeOpen = $state(false);
	let revokeTarget = $state<APIToken | null>(null);
	let revoking = $state(false);

	$effect(() => {
		if (repo && repo.fullName !== loadedFor) {
			loadedFor = repo.fullName;
			pager.reset();
			void load();
		}
	});

	async function load() {
		loading = true;
		try {
			const resp = await rpcClient.token.listAPITokens(
				{ page: pager.request(), gitRepo: fullName },
				silentCallOptions
			);
			tokens = resp.tokens;
			pager.apply(resp.page);
		} catch {
			tokens = [];
		} finally {
			loading = false;
			loaded = true;
		}
	}

	async function revoke() {
		if (!revokeTarget) return;
		revoking = true;
		try {
			await rpcClient.token.deleteAPIToken({ id: revokeTarget.id });
			toast.success('Token revoked');
			revokeOpen = false;
			await load();
		} catch {
			// Error interceptor already toasted
		} finally {
			revoking = false;
		}
	}
</script>

<div class="space-y-6">
	<div class="section-header">
		<div class="min-w-0">
			<h2 class="section-title">Deploy tokens</h2>
			<p class="section-subtitle">
				Tokens limited to this repository and its linked packages, owned by you.
			</p>
		</div>
		<Button class="shrink-0" href={newHref} disabled={!repo}>
			<Plus />Create token
		</Button>
	</div>

	{#if !loaded}
		<div class="box divide-y">
			{#each { length: 2 }, i (i)}
				<div class="space-y-2 px-4 py-3">
					<Skeleton class="h-4 w-40" />
					<Skeleton class="h-3 w-64" />
				</div>
			{/each}
		</div>
	{:else if tokens.length === 0}
		<EmptyState
			icon={Key}
			message="No deploy tokens"
			description="External CI uses one to clone, push, and publish images or artifacts for this repository."
		>
			{#snippet actions()}
				<Button variant="outline" href={newHref}>
					<Plus />Create token
				</Button>
			{/snippet}
		</EmptyState>
	{:else}
		<div class="data-table transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
			<Table>
				<TableHeader>
					<TableRow>
						<TableHead>Name</TableHead>
						<TableHead>Access</TableHead>
						<TableHead class="hidden md:table-cell">Expires</TableHead>
						<TableHead class="hidden md:table-cell">Last used</TableHead>
						<TableHead class="w-12"></TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{#each tokens as token (token.id)}
						<TableRow>
							<TableCell class="text-sm font-medium">{token.name}</TableCell>
							<TableCell>
								{#if token.scope?.readOnly}
									<Badge variant="outline"><Lock />Read only</Badge>
								{:else}
									<Badge variant="warning"><PenLine />Read and write</Badge>
								{/if}
							</TableCell>
							<TableCell class="hidden text-sm md:table-cell">
								{#if token.expiresAt}
									{@const d = timestampDate(token.expiresAt)}
									<Badge variant={d < new Date() ? 'destructive' : 'outline'}>
										{d < new Date() ? 'Expired' : d.toLocaleDateString()}
									</Badge>
								{:else}
									<Badge variant="secondary">Never</Badge>
								{/if}
							</TableCell>
							<TableCell class="hidden text-sm text-muted-foreground md:table-cell">
								{token.lastUsedAt ? relativeTime(timestampDate(token.lastUsedAt)) : 'Never'}
							</TableCell>
							<TableCell class="text-right">
								<Button
									variant="ghost-muted"
									size="icon-sm"
									class="text-destructive hover:text-destructive"
									aria-label="Revoke {token.name}"
									onclick={() => {
										revokeTarget = token;
										revokeOpen = true;
									}}
								>
									<Trash />
								</Button>
							</TableCell>
						</TableRow>
					{/each}
				</TableBody>
			</Table>
			<DataPagination attached {pager} onChange={load} />
		</div>
	{/if}
</div>

<ConfirmDialog
	bind:open={revokeOpen}
	title="Revoke token"
	confirmLabel="Revoke"
	onConfirm={revoke}
	loading={revoking}
	icon={Trash}
>
	{#snippet description()}
		Anything using <strong>{revokeTarget?.name}</strong> loses access immediately.
	{/snippet}
</ConfirmDialog>

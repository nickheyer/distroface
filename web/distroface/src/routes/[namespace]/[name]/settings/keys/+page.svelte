<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { toast } from 'svelte-sonner';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime } from '$lib/utils';
	import { Pager } from '$lib/pager.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
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
	import { Key as KeyRound, Plus, Trash, Lock, PenLine, Info } from '$lib/icons';
	import type { SSHKey } from '$lib/proto/distroface/v1/keys_pb';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);
	const newHref = $derived(resolve('/[namespace]/[name]/settings/keys/new', { namespace, name }));

	let keys = $state<SSHKey[]>([]);
	let loading = $state(false);
	let loaded = $state(false);
	let loadedFor = $state('');
	const pager = new Pager(20);

	let deleteOpen = $state(false);
	let deleteTarget = $state<SSHKey | null>(null);
	let deleting = $state(false);

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
			const resp = await rpcClient.key.listSSHKeys(
				{ namespace, name, page: pager.request() },
				silentCallOptions
			);
			keys = resp.keys;
			pager.apply(resp.page);
		} catch {
			keys = [];
		} finally {
			loading = false;
			loaded = true;
		}
	}

	async function remove() {
		if (!deleteTarget) return;
		deleting = true;
		try {
			await rpcClient.key.deleteSSHKey({ namespace, name, id: deleteTarget.id });
			toast.success('Deploy key removed');
			deleteOpen = false;
			await load();
		} catch {
			// Error interceptor already toasted
		} finally {
			deleting = false;
		}
	}
</script>

<div class="space-y-6">
	<div class="section-header">
		<div class="min-w-0">
			<h2 class="section-title">Deploy keys</h2>
			<p class="section-subtitle">
				SSH keys that reach only this repository, read only unless you allow pushes.
			</p>
		</div>
		<Button class="shrink-0" href={newHref} disabled={!repo}>
			<Plus />Add key
		</Button>
	</div>

	{#if !configStore.sshEnabled}
		<Alert>
			<Info />
			<AlertDescription
				>SSH is disabled on this instance, deploy keys cannot be used until an admin enables it.</AlertDescription
			>
		</Alert>
	{/if}

	{#if !loaded}
		<div class="box divide-y">
			{#each { length: 2 }, i (i)}
				<div class="space-y-2 px-4 py-3">
					<Skeleton class="h-4 w-40" />
					<Skeleton class="h-3 w-64" />
				</div>
			{/each}
		</div>
	{:else if keys.length === 0}
		<EmptyState
			icon={KeyRound}
			message="No deploy keys"
			description="Give a server or CI job clone access without a user account."
		>
			{#snippet actions()}
				<Button variant="outline" href={newHref}>
					<Plus />Add key
				</Button>
			{/snippet}
		</EmptyState>
	{:else}
		<div class="data-table transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
			<Table>
				<TableHeader>
					<TableRow>
						<TableHead>Key</TableHead>
						<TableHead>Access</TableHead>
						<TableHead class="hidden md:table-cell">Last used</TableHead>
						<TableHead class="hidden md:table-cell">Expires</TableHead>
						<TableHead class="w-12"></TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{#each keys as key (key.id)}
						<TableRow>
							<TableCell>
								<div class="min-w-0">
									<p class="truncate text-sm font-medium">{key.title}</p>
									<p class="truncate font-mono text-xs text-muted-foreground">
										{key.keyType}
										{key.fingerprint}
									</p>
								</div>
							</TableCell>
							<TableCell>
								{#if key.readOnly}
									<Badge variant="outline"><Lock />Read only</Badge>
								{:else}
									<Badge variant="warning"><PenLine />Read and write</Badge>
								{/if}
							</TableCell>
							<TableCell class="hidden text-sm text-muted-foreground md:table-cell">
								{key.lastUsedAt ? relativeTime(timestampDate(key.lastUsedAt)) : 'Never'}
							</TableCell>
							<TableCell class="hidden text-sm md:table-cell">
								{#if key.expiresAt}
									{@const d = timestampDate(key.expiresAt)}
									<Badge variant={d < new Date() ? 'destructive' : 'outline'}>
										{d < new Date() ? 'Expired' : d.toLocaleDateString()}
									</Badge>
								{:else}
									<Badge variant="secondary">Never</Badge>
								{/if}
							</TableCell>
							<TableCell class="text-right">
								<Button
									variant="ghost-muted"
									size="icon-sm"
									class="text-destructive hover:text-destructive"
									aria-label="Remove {key.title}"
									onclick={() => {
										deleteTarget = key;
										deleteOpen = true;
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
	bind:open={deleteOpen}
	title="Remove deploy key"
	confirmLabel="Remove"
	onConfirm={remove}
	loading={deleting}
	icon={Trash}
>
	{#snippet description()}
		<strong>{deleteTarget?.title}</strong> stops authenticating immediately.
	{/snippet}
</ConfirmDialog>

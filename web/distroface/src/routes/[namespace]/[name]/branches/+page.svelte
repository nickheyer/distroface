<script lang="ts">
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	import { GitBranch, Plus, Shield, Trash2, Pencil, GitPullRequest, GitCompare, Search } from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import {
		Table, TableBody, TableCell, TableHead, TableHeader, TableRow
	} from '$lib/components/ui/table';
	import EmptyState from '$lib/components/empty-state.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import FormPanel from '$lib/components/form-panel.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import CommitBadge from '$lib/components/git/commit-badge.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { Pager } from '$lib/pager.svelte';
	import { GitUnit } from '$lib/proto/distroface/v1/git_pb';
	import type { GitBranch as GitBranchMsg } from '$lib/proto/distroface/v1/git_pb';
	import { relativeTime } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { toast } from 'svelte-sonner';

	const repo = $derived(gitRepoStore.repo!);
	const namespace = $derived(repo.namespace);
	const name = $derived(repo.name);
	const canWrite = $derived(gitRepoStore.canWrite(GitUnit.CODE) && !repo.isArchived);
	const pullsEnabled = $derived(gitRepoStore.unitEnabled(GitUnit.PULLS));

	let branches = $state<GitBranchMsg[]>([]);
	let loading = $state(true);
	let query = $state('');
	const pager = new Pager(25);
	let debounce: ReturnType<typeof setTimeout> | undefined;

	async function load() {
		loading = true;
		try {
			const resp = await rpcClient.gitRefs.listBranches(
				{ namespace, name, page: pager.request({ text: query.trim(), filters: [] }, 'updated desc') },
				silentCallOptions
			);
			branches = resp.branches;
			pager.apply(resp.page);
		} catch {
			branches = [];
			pager.apply();
		} finally {
			loading = false;
		}
	}

	function onQuery() {
		clearTimeout(debounce);
		debounce = setTimeout(() => {
			pager.reset();
			load();
		}, 250);
	}

	onMount(load);

	const defaultBranch = $derived(branches.find((b) => b.isDefault));
	const others = $derived(branches.filter((b) => !b.isDefault));
	const maxAheadBehind = $derived(Math.max(1, ...branches.map((b) => Math.max(b.ahead, b.behind))));

	let createOpen = $state(false);
	let newBranch = $state('');
	let newFrom = $state('');
	let creating = $state(false);

	function openCreate() {
		newBranch = '';
		newFrom = repo.defaultBranch;
		createOpen = true;
	}

	async function createBranch() {
		if (!newBranch.trim() || creating) return;
		creating = true;
		try {
			await rpcClient.gitRefs.createBranch({ namespace, name, branch: newBranch.trim(), from: newFrom.trim() });
			toast.success('Branch created');
			createOpen = false;
			gitRepoStore.reload();
			await load();
		} catch {
			// Error interceptor already toasted
		} finally {
			creating = false;
		}
	}

	let renameOpen = $state(false);
	let renameTarget = $state('');
	let renameValue = $state('');
	let renaming = $state(false);

	function openRename(b: GitBranchMsg) {
		renameTarget = b.name;
		renameValue = b.name;
		renameOpen = true;
	}

	async function renameBranch() {
		if (!renameValue.trim() || renameValue.trim() === renameTarget || renaming) return;
		renaming = true;
		try {
			await rpcClient.gitRefs.renameBranch({ namespace, name, branch: renameTarget, newName: renameValue.trim() });
			toast.success('Branch renamed');
			renameOpen = false;
			gitRepoStore.reload();
			await load();
		} catch {
			// Error interceptor already toasted
		} finally {
			renaming = false;
		}
	}

	let deleteOpen = $state(false);
	let deleteTarget = $state('');
	let deleting = $state(false);

	function openDelete(b: GitBranchMsg) {
		deleteTarget = b.name;
		deleteOpen = true;
	}

	async function deleteBranch() {
		deleting = true;
		try {
			await rpcClient.gitRefs.deleteBranch({ namespace, name, branch: deleteTarget });
			toast.success('Branch deleted');
			deleteOpen = false;
			gitRepoStore.reload();
			await load();
		} catch {
			// Error interceptor already toasted
		} finally {
			deleting = false;
		}
	}

	function commitTitle(b: GitBranchMsg): string {
		return b.commit?.message.split('\n')[0] ?? '';
	}

	function commitDate(b: GitBranchMsg): Date | null {
		const ts = b.commit?.committer?.at ?? b.commit?.author?.at;
		return ts ? timestampDate(ts) : null;
	}
</script>

{#snippet row(b: GitBranchMsg)}
	{@const date = commitDate(b)}
	<TableRow>
		<TableCell class="py-2.5 px-3">
			<div class="flex items-center gap-2 min-w-0">
				<a href={resolve('/[namespace]/[name]/tree/[ref]/[...path]', { namespace, name, ref: encodeURIComponent(b.name), path: '' })} class="font-mono text-[13px] font-medium hover:text-primary truncate">{b.name}</a>
				{#if b.isDefault}
					<Badge variant="outline" class="text-[10px] h-4.5 py-0 border-primary/30 text-primary/80">Default</Badge>
				{/if}
				{#if b.isProtected}
					<Badge variant="outline" class="text-[10px] h-4.5 py-0 gap-0.5 text-muted-foreground" title="Protected"><Shield class="h-2.5 w-2.5" />Protected</Badge>
				{/if}
			</div>
			<div class="text-[12px] text-muted-foreground mt-0.5 flex items-center gap-1.5 min-w-0">
				{#if b.commit}
					<CommitBadge sha={b.sha} {namespace} {name} copy={false} />
					<span class="truncate" title={commitTitle(b)}>{commitTitle(b)}</span>
				{/if}
			</div>
		</TableCell>
		<TableCell class="py-2.5 px-3 hidden md:table-cell text-[12.5px] text-muted-foreground whitespace-nowrap">
			{#if b.commit?.author}
				<span class="font-medium text-foreground/80">{b.commit.author.username || b.commit.author.name}</span>
			{/if}
			{#if date}
				<span title={date.toLocaleString()}>, {relativeTime(date)}</span>
			{/if}
		</TableCell>
		<TableCell class="py-2.5 px-3 hidden lg:table-cell">
			{#if !b.isDefault}
				<div class="flex items-center gap-2 text-[11px] tabular-nums w-44">
					<div class="flex-1 flex justify-end items-center gap-1">
						<span class="text-muted-foreground">{b.behind}</span>
						<div class="h-1.5 bg-destructive/50 rounded-l" style="width: {Math.round((b.behind / maxAheadBehind) * 100)}%"></div>
					</div>
					<div class="w-px h-4 bg-border"></div>
					<div class="flex-1 flex items-center gap-1">
						<div class="h-1.5 bg-success/60 rounded-r" style="width: {Math.round((b.ahead / maxAheadBehind) * 100)}%"></div>
						<span class="text-muted-foreground">{b.ahead}</span>
					</div>
				</div>
			{/if}
		</TableCell>
		<TableCell class="py-2.5 px-3 text-right">
			<div class="flex items-center justify-end gap-1">
				{#if !b.isDefault}
					{#if pullsEnabled}
						<Button variant="ghost" size="sm" class="h-7 px-2 text-[12px]" href={resolve(`${resolve('/[namespace]/[name]/pulls/new', { namespace, name })}?base=${encodeURIComponent(repo.defaultBranch)}&head=${encodeURIComponent(b.name)}` as `/${string}`)} title="New pull request">
							<GitPullRequest class="h-3.5 w-3.5" />
						</Button>
					{/if}
					<Button variant="ghost" size="sm" class="h-7 px-2 text-[12px]" href={resolve('/[namespace]/[name]/compare/[range]', { namespace, name, range: `${encodeURIComponent(repo.defaultBranch)}...${encodeURIComponent(b.name)}` })} title="Compare">
						<GitCompare class="h-3.5 w-3.5" />
					</Button>
				{/if}
				<PermissionGate allowed={canWrite}>
					<Button variant="ghost" size="sm" class="h-7 px-2" onclick={() => openRename(b)} title="Rename">
						<Pencil class="h-3.5 w-3.5" />
					</Button>
					{#if !b.isDefault}
						<Button variant="ghost" size="sm" class="h-7 px-2 text-destructive hover:text-destructive" onclick={() => openDelete(b)} title="Delete" disabled={b.isProtected}>
							<Trash2 class="h-3.5 w-3.5" />
						</Button>
					{/if}
				</PermissionGate>
			</div>
		</TableCell>
	</TableRow>
{/snippet}

<div class="space-y-4">
	<div class="flex items-center gap-2 flex-wrap">
		<div class="relative w-full sm:w-64">
			<Search class="h-3.5 w-3.5 text-muted-foreground absolute left-2.5 top-1/2 -translate-y-1/2" />
			<Input bind:value={query} oninput={onQuery} placeholder="Search branches" class="h-8 pl-8 text-[13px]" />
		</div>
		<span class="text-[12.5px] text-muted-foreground tabular-nums">{pager.totalCount || branches.length} branch{(pager.totalCount || branches.length) !== 1 ? 'es' : ''}</span>
		<div class="ml-auto">
			<PermissionGate allowed={canWrite}>
				<Button size="sm" class="h-8" onclick={openCreate}>
					<Plus class="h-3.5 w-3.5 mr-1.5" />New Branch
				</Button>
			</PermissionGate>
		</div>
	</div>

	{#if loading && branches.length === 0}
		<Skeleton class="h-64 w-full rounded-xl" />
	{:else if branches.length === 0}
		<EmptyState icon={GitBranch} message={query ? 'No branches match' : 'No branches yet'} />
	{:else}
		<div class="data-table transition-opacity {loading ? 'opacity-60' : ''}">
			<Table class="table-fixed">
				<TableHeader>
					<TableRow>
						<TableHead class="th">Branch</TableHead>
						<TableHead class="th w-56 hidden md:table-cell">Updated</TableHead>
						<TableHead class="th w-52 hidden lg:table-cell">Behind / Ahead</TableHead>
						<TableHead class="th w-40 text-right"></TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{#if defaultBranch}
						{@render row(defaultBranch)}
					{/if}
					{#each others as b (b.name)}
						{@render row(b)}
					{/each}
				</TableBody>
			</Table>
			<DataPagination attached {pager} onChange={load} />
		</div>
	{/if}
</div>

<FormPanel open={createOpen} onOpenChange={(v) => (createOpen = v)} title="New branch" icon={GitBranch}>
	<div class="space-y-3">
		<FormField label="Branch name" id="new-branch-name" required>
			<Input id="new-branch-name" bind:value={newBranch} placeholder="feature/my-change" class="font-mono text-sm" />
		</FormField>
		<FormField label="Create from" id="new-branch-from" help="Branch, tag, or commit sha">
			<Input id="new-branch-from" bind:value={newFrom} class="font-mono text-sm" />
		</FormField>
	</div>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (createOpen = false)}>Cancel</Button>
		<Button onclick={createBranch} disabled={creating || !newBranch.trim()}>{creating ? 'Creating...' : 'Create Branch'}</Button>
	{/snippet}
</FormPanel>

<FormPanel open={renameOpen} onOpenChange={(v) => (renameOpen = v)} title="Rename branch" description="Renaming {renameTarget}" icon={Pencil}>
	<FormField label="New name" id="rename-branch-name" required>
		<Input id="rename-branch-name" bind:value={renameValue} class="font-mono text-sm" />
	</FormField>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (renameOpen = false)}>Cancel</Button>
		<Button onclick={renameBranch} disabled={renaming || !renameValue.trim() || renameValue.trim() === renameTarget}>{renaming ? 'Renaming...' : 'Rename'}</Button>
	{/snippet}
</FormPanel>

<ConfirmDialog bind:open={deleteOpen} title="Delete branch" confirmLabel="Delete" onConfirm={deleteBranch} loading={deleting} icon={Trash2}>
	{#snippet description()}
		This deletes <strong class="font-mono">{deleteTarget}</strong>. Commits not reachable from another ref become unreachable.
	{/snippet}
</ConfirmDialog>

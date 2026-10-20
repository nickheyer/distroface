<script lang="ts">
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	import {
		GitBranch,
		Plus,
		Shield,
		Trash2,
		Pencil,
		GitPullRequest,
		GitCompare,
		Search,
		Loader2
	} from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
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
	import EmptyState from '$lib/components/empty-state.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
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
				{
					namespace,
					name,
					page: pager.request({ text: query.trim(), filters: [] }, 'updated desc')
				},
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

	const newHref = $derived(resolve('/[namespace]/[name]/branches/new', { namespace, name }));

	// Renaming swaps the row for an inline form
	let renameTarget = $state('');
	let renameValue = $state('');
	let renaming = $state(false);
	const renameValid = $derived(!!renameValue.trim() && renameValue.trim() !== renameTarget);

	function openRename(b: GitBranchMsg) {
		renameTarget = b.name;
		renameValue = b.name;
	}

	function closeRename() {
		renameTarget = '';
		renameValue = '';
	}

	async function renameBranch() {
		if (!renameValid || renaming) return;
		renaming = true;
		try {
			await rpcClient.gitRefs.renameBranch({
				namespace,
				name,
				branch: renameTarget,
				newName: renameValue.trim()
			});
			toast.success('Branch renamed');
			closeRename();
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

{#snippet renameRow(b: GitBranchMsg)}
	<TableRow class="bg-muted/30 hover:[&>td]:bg-transparent">
		<TableCell colspan={4} class="px-4 py-4 whitespace-normal">
			<div class="form-fields">
				<FormField
					label="Rename {b.name}"
					id="rename-branch-name"
					help="Open pull requests and protections follow the new name"
					span="half"
				>
					<Input
						id="rename-branch-name"
						bind:value={renameValue}
						class="font-mono"
						autocomplete="off"
						spellcheck="false"
						onkeydown={(e) => {
							if (e.key === 'Enter') renameBranch();
							if (e.key === 'Escape') closeRename();
						}}
					/>
				</FormField>
				<div class="flex flex-wrap items-center gap-2">
					<Button onclick={renameBranch} disabled={renaming || !renameValid}>
						{#if renaming}<Loader2 class="animate-spin" />{/if}
						{renaming ? 'Renaming' : 'Rename branch'}
					</Button>
					<Button variant="ghost-muted" onclick={closeRename} disabled={renaming}>Cancel</Button>
				</div>
			</div>
		</TableCell>
	</TableRow>
{/snippet}

{#snippet row(b: GitBranchMsg)}
	{@const date = commitDate(b)}
	<TableRow>
		<TableCell>
			<div class="flex min-w-0 items-center gap-2">
				<a
					href={resolve('/[namespace]/[name]/tree/[ref]/[...path]', {
						namespace,
						name,
						ref: encodeURIComponent(b.name),
						path: ''
					})}
					class="truncate font-mono text-sm font-medium hover:text-primary">{b.name}</a
				>
				{#if b.isDefault}
					<Badge>Default</Badge>
				{/if}
				{#if b.isProtected}
					<Badge variant="outline" title="Protected"><Shield />Protected</Badge>
				{/if}
			</div>
			<div class="mt-0.5 flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
				{#if b.commit}
					<CommitBadge sha={b.sha} {namespace} {name} copy={false} />
					<span class="truncate" title={commitTitle(b)}>{commitTitle(b)}</span>
				{/if}
			</div>
		</TableCell>
		<TableCell class="hidden text-sm whitespace-nowrap text-muted-foreground md:table-cell">
			{#if b.commit?.author}<span class="font-medium text-foreground"
					>{b.commit.author.username || b.commit.author.name}</span
				>{/if}{#if b.commit?.author && date},
			{/if}{#if date}<span title={date.toLocaleString()}>{relativeTime(date)}</span>{/if}
		</TableCell>
		<TableCell class="hidden lg:table-cell">
			{#if !b.isDefault}
				<div class="flex w-44 items-center gap-2 text-2xs tabular-nums">
					<div class="flex flex-1 items-center justify-end gap-1">
						<span class="text-muted-foreground">{b.behind}</span>
						<div
							class="h-1.5 rounded-l bg-destructive/50"
							style="width: {Math.round((b.behind / maxAheadBehind) * 100)}%"
						></div>
					</div>
					<div class="h-4 w-px bg-border"></div>
					<div class="flex flex-1 items-center gap-1">
						<div
							class="h-1.5 rounded-r bg-success/60"
							style="width: {Math.round((b.ahead / maxAheadBehind) * 100)}%"
						></div>
						<span class="text-muted-foreground">{b.ahead}</span>
					</div>
				</div>
			{/if}
		</TableCell>
		<TableCell class="text-right">
			<div class="flex items-center justify-end gap-1">
				{#if !b.isDefault}
					{#if pullsEnabled}
						<Button
							variant="ghost-muted"
							size="icon-sm"
							href={resolve(
								`${resolve('/[namespace]/[name]/pulls/new', { namespace, name })}?base=${encodeURIComponent(repo.defaultBranch)}&head=${encodeURIComponent(b.name)}` as `/${string}`
							)}
							title="New pull request"
						>
							<GitPullRequest />
						</Button>
					{/if}
					<Button
						variant="ghost-muted"
						size="icon-sm"
						href={resolve('/[namespace]/[name]/compare/[range]', {
							namespace,
							name,
							range: `${encodeURIComponent(repo.defaultBranch)}...${encodeURIComponent(b.name)}`
						})}
						title="Compare"
					>
						<GitCompare />
					</Button>
				{/if}
				<PermissionGate allowed={canWrite}>
					<Button variant="ghost-muted" size="icon-sm" onclick={() => openRename(b)} title="Rename">
						<Pencil />
					</Button>
					{#if !b.isDefault}
						<Button
							variant="ghost-muted"
							size="icon-sm"
							class="text-destructive hover:text-destructive"
							onclick={() => openDelete(b)}
							title="Delete"
							disabled={b.isProtected}
						>
							<Trash2 />
						</Button>
					{/if}
				</PermissionGate>
			</div>
		</TableCell>
	</TableRow>
{/snippet}

<div class="space-y-4">
	<div class="flex flex-wrap items-center gap-2">
		<div class="relative w-full sm:w-64">
			<Search
				class="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground"
			/>
			<Input bind:value={query} oninput={onQuery} placeholder="Search branches" class="pl-8" />
		</div>
		<span class="text-xs text-muted-foreground tabular-nums"
			>{pager.totalCount || branches.length} branch{(pager.totalCount || branches.length) !== 1
				? 'es'
				: ''}</span
		>
		<div class="ml-auto">
			<PermissionGate allowed={canWrite}>
				<Button href={newHref}>
					<Plus />New branch
				</Button>
			</PermissionGate>
		</div>
	</div>

	{#if loading && branches.length === 0}
		<Skeleton class="h-64 w-full rounded-lg" />
	{:else if branches.length === 0}
		<EmptyState icon={GitBranch} message={query ? 'No branches match' : 'No branches yet'} />
	{:else}
		<div class="data-table transition-opacity {loading ? 'opacity-60' : ''}">
			<Table class="table-fixed">
				<TableHeader>
					<TableRow>
						<TableHead>Branch</TableHead>
						<TableHead class="hidden w-56 md:table-cell">Updated</TableHead>
						<TableHead class="hidden w-52 lg:table-cell">Behind / Ahead</TableHead>
						<TableHead class="w-40 text-right"></TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{#if defaultBranch}
						{#if renameTarget === defaultBranch.name}
							{@render renameRow(defaultBranch)}
						{:else}
							{@render row(defaultBranch)}
						{/if}
					{/if}
					{#each others as b (b.name)}
						{#if renameTarget === b.name}
							{@render renameRow(b)}
						{:else}
							{@render row(b)}
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
	title="Delete branch"
	confirmLabel="Delete"
	onConfirm={deleteBranch}
	loading={deleting}
	icon={Trash2}
>
	{#snippet description()}
		This deletes <strong class="font-mono">{deleteTarget}</strong>. Commits not reachable from
		another ref become unreachable.
	{/snippet}
</ConfirmDialog>

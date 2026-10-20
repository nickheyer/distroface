<script lang="ts">
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	import { Tag, Plus, Trash2, Search, Rocket } from '@lucide/svelte';
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
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import CommitBadge from '$lib/components/git/commit-badge.svelte';
	import VerificationBadge from '$lib/components/git/verification-badge.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { Pager } from '$lib/pager.svelte';
	import { GitUnit } from '$lib/proto/distroface/v1/git_pb';
	import type { GitTag } from '$lib/proto/distroface/v1/git_pb';
	import { relativeTime } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { toast } from 'svelte-sonner';

	const repo = $derived(gitRepoStore.repo!);
	const namespace = $derived(repo.namespace);
	const name = $derived(repo.name);
	const canWrite = $derived(gitRepoStore.canWrite(GitUnit.CODE) && !repo.isArchived);
	const releasesEnabled = $derived(gitRepoStore.unitEnabled(GitUnit.RELEASES));

	let tags = $state<GitTag[]>([]);
	let loading = $state(true);
	let query = $state('');
	const pager = new Pager(25);
	let debounce: ReturnType<typeof setTimeout> | undefined;

	async function load() {
		loading = true;
		try {
			const resp = await rpcClient.gitRefs.listGitTags(
				{ namespace, name, page: pager.request({ text: query.trim(), filters: [] }) },
				silentCallOptions
			);
			tags = resp.tags;
			pager.apply(resp.page);
		} catch {
			tags = [];
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

	const newHref = $derived(resolve('/[namespace]/[name]/tags/new', { namespace, name }));

	let deleteOpen = $state(false);
	let deleteTarget = $state('');
	let deleting = $state(false);

	function openDelete(t: GitTag) {
		deleteTarget = t.name;
		deleteOpen = true;
	}

	async function deleteTag() {
		deleting = true;
		try {
			await rpcClient.gitRefs.deleteTag({ namespace, name, tag: deleteTarget });
			toast.success('Tag deleted');
			deleteOpen = false;
			gitRepoStore.reload();
			await load();
		} catch {
			// Error interceptor already toasted
		} finally {
			deleting = false;
		}
	}

	function tagDate(t: GitTag): Date | null {
		const ts = t.tagger?.at ?? t.commit?.committer?.at ?? t.commit?.author?.at;
		return ts ? timestampDate(ts) : null;
	}

	function who(t: GitTag): string {
		const s = t.tagger ?? t.commit?.author;
		return s?.username || s?.name || '';
	}
</script>

<div class="space-y-4">
	<div class="flex flex-wrap items-center gap-2">
		<div class="relative w-full sm:w-64">
			<Search
				class="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground"
			/>
			<Input bind:value={query} oninput={onQuery} placeholder="Search tags" class="pl-8" />
		</div>
		<span class="text-xs text-muted-foreground tabular-nums"
			>{pager.totalCount || tags.length} tag{(pager.totalCount || tags.length) !== 1
				? 's'
				: ''}</span
		>
		<div class="ml-auto flex items-center gap-2">
			{#if releasesEnabled}
				<Button
					variant="outline"
					href={resolve('/[namespace]/[name]/releases', { namespace, name })}
				>
					<Rocket />Releases
				</Button>
			{/if}
			<PermissionGate allowed={canWrite}>
				<Button href={newHref}>
					<Plus />New tag
				</Button>
			</PermissionGate>
		</div>
	</div>

	{#if loading && tags.length === 0}
		<Skeleton class="h-64 w-full rounded-lg" />
	{:else if tags.length === 0}
		<EmptyState
			icon={Tag}
			message={query ? 'No tags match' : 'No tags yet'}
			description={query ? undefined : 'Tags mark release points in history.'}
		/>
	{:else}
		<div class="data-table transition-opacity {loading ? 'opacity-60' : ''}">
			<Table class="table-fixed">
				<TableHeader>
					<TableRow>
						<TableHead>Tag</TableHead>
						<TableHead class="hidden w-40 sm:table-cell">Commit</TableHead>
						<TableHead class="hidden w-56 md:table-cell">Tagged</TableHead>
						<TableHead class="w-24 text-right"></TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{#each tags as t (t.name)}
						{@const date = tagDate(t)}
						<TableRow>
							<TableCell>
								<div class="flex min-w-0 flex-wrap items-center gap-2">
									<a
										href={resolve('/[namespace]/[name]/tree/[ref]/[...path]', {
											namespace,
											name,
											ref: encodeURIComponent(t.name),
											path: ''
										})}
										class="truncate font-mono text-sm font-medium hover:text-primary">{t.name}</a
									>
									{#if t.tagSha}
										<Badge variant="outline">Annotated</Badge>
									{/if}
									<VerificationBadge verification={t.verification} />
									{#if t.hasRelease && releasesEnabled}
										<a
											href={resolve('/[namespace]/[name]/releases/[tag]', {
												namespace,
												name,
												tag: encodeURIComponent(t.name)
											})}
										>
											<Badge variant="success"><Rocket />Release</Badge>
										</a>
									{/if}
								</div>
								{#if t.message}
									<p class="mt-0.5 truncate text-xs text-muted-foreground" title={t.message}>
										{t.message.split('\n')[0]}
									</p>
								{/if}
							</TableCell>
							<TableCell class="hidden sm:table-cell">
								<CommitBadge sha={t.sha} {namespace} {name} />
							</TableCell>
							<TableCell
								class="hidden text-sm whitespace-nowrap text-muted-foreground md:table-cell"
							>
								{#if who(t)}
									<span class="font-medium text-foreground">{who(t)}</span>
								{/if}
								{#if date}
									<span title={date.toLocaleString()}>, {relativeTime(date)}</span>
								{/if}
							</TableCell>
							<TableCell class="text-right">
								<PermissionGate allowed={canWrite}>
									<Button
										variant="ghost-muted"
										size="icon-sm"
										class="text-destructive hover:text-destructive"
										onclick={() => openDelete(t)}
										title="Delete tag"
									>
										<Trash2 />
									</Button>
								</PermissionGate>
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
	title="Delete tag"
	confirmLabel="Delete"
	onConfirm={deleteTag}
	loading={deleting}
	icon={Trash2}
>
	{#snippet description()}
		This deletes <strong class="font-mono">{deleteTarget}</strong>. Releases pointing at it keep
		their assets but lose the tag.
	{/snippet}
</ConfirmDialog>

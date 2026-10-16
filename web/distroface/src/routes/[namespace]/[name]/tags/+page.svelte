<script lang="ts">
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	import { Tag, Plus, Trash2, Search, Rocket } from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
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

	let createOpen = $state(false);
	let newTag = $state('');
	let newTarget = $state('');
	let newMessage = $state('');
	let creating = $state(false);

	function openCreate() {
		newTag = '';
		newTarget = repo.defaultBranch;
		newMessage = '';
		createOpen = true;
	}

	async function createTag() {
		if (!newTag.trim() || creating) return;
		creating = true;
		try {
			await rpcClient.gitRefs.createTag({ namespace, name, tag: newTag.trim(), target: newTarget.trim(), message: newMessage.trim() });
			toast.success('Tag created');
			createOpen = false;
			gitRepoStore.reload();
			await load();
		} catch {
			// Error interceptor already toasted
		} finally {
			creating = false;
		}
	}

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
	<div class="flex items-center gap-2 flex-wrap">
		<div class="relative w-full sm:w-64">
			<Search class="h-3.5 w-3.5 text-muted-foreground absolute left-2.5 top-1/2 -translate-y-1/2" />
			<Input bind:value={query} oninput={onQuery} placeholder="Search tags" class="h-8 pl-8 text-[13px]" />
		</div>
		<span class="text-[12.5px] text-muted-foreground tabular-nums">{pager.totalCount || tags.length} tag{(pager.totalCount || tags.length) !== 1 ? 's' : ''}</span>
		<div class="ml-auto flex items-center gap-2">
			{#if releasesEnabled}
				<Button variant="outline" size="sm" class="h-8" href={resolve('/[namespace]/[name]/releases', { namespace, name })}>
					<Rocket class="h-3.5 w-3.5 mr-1.5" />Releases
				</Button>
			{/if}
			<PermissionGate allowed={canWrite}>
				<Button size="sm" class="h-8" onclick={openCreate}>
					<Plus class="h-3.5 w-3.5 mr-1.5" />New Tag
				</Button>
			</PermissionGate>
		</div>
	</div>

	{#if loading && tags.length === 0}
		<Skeleton class="h-64 w-full rounded-xl" />
	{:else if tags.length === 0}
		<EmptyState icon={Tag} message={query ? 'No tags match' : 'No tags yet'} description={query ? undefined : 'Tags mark release points in history.'} />
	{:else}
		<div class="data-table transition-opacity {loading ? 'opacity-60' : ''}">
			<Table class="table-fixed">
				<TableHeader>
					<TableRow>
						<TableHead class="th">Tag</TableHead>
						<TableHead class="th w-40 hidden sm:table-cell">Commit</TableHead>
						<TableHead class="th w-56 hidden md:table-cell">Tagged</TableHead>
						<TableHead class="th w-24 text-right"></TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{#each tags as t (t.name)}
						{@const date = tagDate(t)}
						<TableRow>
							<TableCell class="py-2.5 px-3">
								<div class="flex items-center gap-2 min-w-0 flex-wrap">
									<a href={resolve('/[namespace]/[name]/tree/[ref]/[...path]', { namespace, name, ref: encodeURIComponent(t.name), path: '' })} class="font-mono text-[13px] font-medium hover:text-primary truncate">{t.name}</a>
									{#if t.tagSha}
										<Badge variant="outline" class="text-[10px] h-4.5 py-0 text-muted-foreground">Annotated</Badge>
									{/if}
									<VerificationBadge verification={t.verification} />
									{#if t.hasRelease && releasesEnabled}
										<a href={resolve('/[namespace]/[name]/releases/[tag]', { namespace, name, tag: encodeURIComponent(t.name) })}>
											<Badge variant="outline" class="text-[10px] h-4.5 py-0 gap-0.5 border-emerald-500/30 text-emerald-600 dark:text-emerald-400 hover:bg-emerald-500/10">
												<Rocket class="h-2.5 w-2.5" />Release
											</Badge>
										</a>
									{/if}
								</div>
								{#if t.message}
									<p class="text-[12px] text-muted-foreground mt-0.5 truncate" title={t.message}>{t.message.split('\n')[0]}</p>
								{/if}
							</TableCell>
							<TableCell class="py-2.5 px-3 hidden sm:table-cell">
								<CommitBadge sha={t.sha} {namespace} {name} />
							</TableCell>
							<TableCell class="py-2.5 px-3 hidden md:table-cell text-[12.5px] text-muted-foreground whitespace-nowrap">
								{#if who(t)}
									<span class="font-medium text-foreground/80">{who(t)}</span>
								{/if}
								{#if date}
									<span title={date.toLocaleString()}>, {relativeTime(date)}</span>
								{/if}
							</TableCell>
							<TableCell class="py-2.5 px-3 text-right">
								<PermissionGate allowed={canWrite}>
									<Button variant="ghost" size="sm" class="h-7 px-2 text-destructive hover:text-destructive" onclick={() => openDelete(t)} title="Delete tag">
										<Trash2 class="h-3.5 w-3.5" />
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

<FormPanel open={createOpen} onOpenChange={(v) => (createOpen = v)} title="New tag" icon={Tag}>
	<div class="space-y-3">
		<FormField label="Tag name" id="new-tag-name" required>
			<Input id="new-tag-name" bind:value={newTag} placeholder="v1.2.0" class="font-mono text-sm" />
		</FormField>
		<FormField label="Target" id="new-tag-target" help="Branch, tag, or commit sha">
			<Input id="new-tag-target" bind:value={newTarget} class="font-mono text-sm" />
		</FormField>
		<FormField label="Message" id="new-tag-message" help="Creates an annotated tag when set">
			<Textarea id="new-tag-message" bind:value={newMessage} rows={3} placeholder="Release notes or a short description" />
		</FormField>
	</div>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (createOpen = false)}>Cancel</Button>
		<Button onclick={createTag} disabled={creating || !newTag.trim()}>{creating ? 'Creating...' : 'Create Tag'}</Button>
	{/snippet}
</FormPanel>

<ConfirmDialog bind:open={deleteOpen} title="Delete tag" confirmLabel="Delete" onConfirm={deleteTag} loading={deleting} icon={Trash2}>
	{#snippet description()}
		This deletes <strong class="font-mono">{deleteTarget}</strong>. Releases pointing at it keep their assets but lose the tag.
	{/snippet}
</ConfirmDialog>

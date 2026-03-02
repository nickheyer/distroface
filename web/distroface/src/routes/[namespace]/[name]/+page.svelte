<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { onMount } from 'svelte';
	import {
		Package, ArrowDown, ArrowUp, Eye, Lock, Pencil, Check, X,
		Layers, Cpu, Monitor, Trash2, MoreHorizontal, EyeOff
	} from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { formatBytes, pageToToken, truncateDigest, relativeTime } from '$lib/utils';
	import { toast } from 'svelte-sonner';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Separator } from '$lib/components/ui/separator';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import {
		DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger
	} from '$lib/components/ui/dropdown-menu';
	import {
		Table, TableBody, TableCell, TableHead, TableHeader, TableRow
	} from '$lib/components/ui/table';
	import {
		Sheet, SheetContent, SheetHeader, SheetTitle, SheetDescription
	} from '$lib/components/ui/sheet';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import CopyButton from '$lib/components/copy-button.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import type { Repository, Tag, TagDetail } from '$lib/proto/distroface/v1/types_pb';
	import { Visibility } from '$lib/proto/distroface/v1/types_pb';

	const namespace = $derived(page.params.namespace);
	const name = $derived(page.params.name);

	let repo = $state<Repository | undefined>(undefined);
	let loading = $state(true);
	let tags = $state<Tag[]>([]);
	let tagsLoading = $state(true);
	let tagsTotalCount = $state(0);
	let tagsPage = $state(1);
	const tagsPageSize = 20;

	let editingDescription = $state(false);
	let descriptionDraft = $state('');
	let savingDescription = $state(false);

	let sheetOpen = $state(false);
	let selectedTagDetail = $state<TagDetail | undefined>(undefined);
	let detailLoading = $state(false);

	let deleteRepoOpen = $state(false);
	let deletingRepo = $state(false);

	const registryHost = $derived(configStore.get('registryHost', 'localhost:8080') as string);

	const isOwner = $derived(
		authStore.user &&
			(authStore.user.username === namespace ||
				authStore.hasPermission('repositories', 'update', `${namespace}/${name}`))
	);

	const canDelete = $derived(
		authStore.user &&
			authStore.hasPermission('repositories', 'delete', `${namespace}/${name}`)
	);

	const canManage = $derived(isOwner || canDelete);
	const pullCommand = $derived(`${registryHost}/${namespace}/${name}`);

	async function loadRepo() {
		loading = true;
		try {
			const resp = await rpcClient.repository.getRepository({ namespace, name });
			repo = resp.repository;
		} catch {
			// error interceptor
		} finally {
			loading = false;
		}
	}

	async function loadTags() {
		tagsLoading = true;
		try {
			const resp = await rpcClient.repository.listTags({
				namespace, name,
				pageSize: tagsPageSize,
				pageToken: pageToToken(tagsPage, tagsPageSize)
			});
			tags = resp.tags;
			tagsTotalCount = resp.totalCount;
		} catch {
			tags = [];
		} finally {
			tagsLoading = false;
		}
	}

	async function openTagDetail(tagName: string) {
		sheetOpen = true;
		detailLoading = true;
		selectedTagDetail = undefined;
		try {
			const resp = await rpcClient.repository.getTagDetail({ namespace, name, tag: tagName });
			selectedTagDetail = resp.detail;
		} catch {
			sheetOpen = false;
		} finally {
			detailLoading = false;
		}
	}

	function startEditDescription() {
		descriptionDraft = repo?.description ?? '';
		editingDescription = true;
	}

	async function saveDescription() {
		savingDescription = true;
		try {
			const resp = await rpcClient.repository.updateRepository({
				namespace, name, description: descriptionDraft
			});
			repo = resp.repository;
			editingDescription = false;
			toast.success('Description updated');
		} catch {
			// error interceptor
		} finally {
			savingDescription = false;
		}
	}

	async function toggleVisibility() {
		if (!repo) return;
		const newVisibility = repo.visibility === Visibility.PRIVATE ? Visibility.PUBLIC : Visibility.PRIVATE;
		try {
			const resp = await rpcClient.repository.updateRepository({ namespace, name, visibility: newVisibility });
			repo = resp.repository;
			toast.success(`Repository is now ${newVisibility === Visibility.PRIVATE ? 'private' : 'public'}`);
		} catch {
			// error interceptor
		}
	}

	async function confirmDeleteRepo() {
		deletingRepo = true;
		try {
			await rpcClient.repository.deleteRepository({ namespace, name });
			toast.success('Repository deleted');
			goto(`/${namespace}`);
		} catch {
			// error interceptor
		} finally {
			deletingRepo = false;
		}
	}

	onMount(() => { loadRepo(); loadTags(); });
</script>

<div class="space-y-6">
	{#if loading}
		<div class="space-y-3">
			<Skeleton class="h-5 w-48" />
			<Skeleton class="h-8 w-64" />
			<Skeleton class="h-4 w-32" />
		</div>
	{:else if repo}
		<nav class="flex items-center gap-1.5 text-sm text-muted-foreground">
			<a href="/" class="hover:text-foreground transition-colors">Explore</a>
			<span>/</span>
			<a href="/{namespace}" class="hover:text-foreground transition-colors">{namespace}</a>
			<span>/</span>
			<span class="text-foreground font-medium">{name}</span>
		</nav>

		<div class="flex items-start justify-between gap-4">
			<div class="space-y-2">
				<div class="flex items-center gap-3">
					<h1 class="text-2xl font-bold tracking-tight">{namespace}/{name}</h1>
					<Badge
						variant="outline"
						class="text-xs {repo.visibility === Visibility.PRIVATE
							? 'border-amber-500/30 text-amber-600 dark:text-amber-400' : ''}"
					>
						{#if repo.visibility === Visibility.PRIVATE}
							<Lock class="h-3 w-3 mr-1" />Private
						{:else}
							<Eye class="h-3 w-3 mr-1" />Public
						{/if}
					</Badge>
				</div>
				<div class="flex items-center gap-4 text-sm text-muted-foreground">
					{#if repo.pullCount > 0}
						<span class="flex items-center gap-1 tabular-nums">
							<ArrowDown class="h-3.5 w-3.5" />{repo.pullCount} pulls
						</span>
					{/if}
					{#if repo.pushCount > 0}
						<span class="flex items-center gap-1 tabular-nums">
							<ArrowUp class="h-3.5 w-3.5" />{repo.pushCount} pushes
						</span>
					{/if}
					{#if repo.lastPushedAt}
						<span>Updated {relativeTime(timestampDate(repo.lastPushedAt))}</span>
					{/if}
				</div>
			</div>
			{#if canManage}
				<DropdownMenu>
					<DropdownMenuTrigger>
						{#snippet child({ props })}
							<Button {...props} variant="outline" size="icon" class="h-8 w-8 shrink-0">
								<MoreHorizontal class="h-4 w-4" />
							</Button>
						{/snippet}
					</DropdownMenuTrigger>
					<DropdownMenuContent align="end">
						{#if isOwner}
							<DropdownMenuItem onclick={toggleVisibility}>
								{#if repo.visibility === Visibility.PRIVATE}
									<Eye class="h-4 w-4 mr-2" />Make Public
								{:else}
									<EyeOff class="h-4 w-4 mr-2" />Make Private
								{/if}
							</DropdownMenuItem>
						{/if}
						{#if canDelete}
							<DropdownMenuSeparator />
							<DropdownMenuItem class="text-destructive focus:text-destructive" onclick={() => (deleteRepoOpen = true)}>
								<Trash2 class="h-4 w-4 mr-2" />Delete Repository
							</DropdownMenuItem>
						{/if}
					</DropdownMenuContent>
				</DropdownMenu>
			{/if}
		</div>

		<div>
			{#if editingDescription}
				<div class="flex gap-2">
					<Input bind:value={descriptionDraft} placeholder="Add a description..." class="flex-1" />
					<Button size="sm" onclick={saveDescription} disabled={savingDescription}>
						<Check class="h-4 w-4" />
					</Button>
					<Button size="sm" variant="ghost" onclick={() => (editingDescription = false)}>
						<X class="h-4 w-4" />
					</Button>
				</div>
			{:else}
				<div class="flex items-center gap-1.5">
					<p class="text-[13px] {repo.description ? '' : 'text-muted-foreground italic'}">
						{repo.description || 'No description'}
					</p>
					{#if isOwner}
						<Button variant="ghost" size="icon" class="h-6 w-6" onclick={startEditDescription}>
							<Pencil class="h-3 w-3" />
						</Button>
					{/if}
				</div>
			{/if}
		</div>

		<div class="flex items-center gap-2">
			<code class="code-inline">docker pull {pullCommand}:latest</code>
			<CopyButton text="docker pull {pullCommand}:latest" label="Copied!" />
		</div>

		<Separator />

		<div class="space-y-4">
			<div class="section-header">
				<h2 class="section-title">Tags</h2>
				{#if tagsTotalCount > 0}
					<span class="text-[13px] text-muted-foreground">{tagsTotalCount} tag{tagsTotalCount !== 1 ? 's' : ''}</span>
				{/if}
			</div>

			{#if tagsLoading}
				<div class="space-y-2">
					{#each Array(3) as _}
						<Skeleton class="h-14 w-full rounded-xl" />
					{/each}
				</div>
			{:else if tags.length === 0}
				<EmptyState icon={Package} message="No tags yet" description="Push an image to create your first tag.">
					{#snippet actions()}
						<code class="code-inline text-xs">docker push {pullCommand}:latest</code>
					{/snippet}
				</EmptyState>
			{:else}
				<div class="data-table">
					<Table class="table-fixed">
						<TableHeader>
							<TableRow class="bg-muted/30 hover:bg-muted/30">
								<TableHead class="th w-36">Tag</TableHead>
								<TableHead class="th">Digest</TableHead>
								<TableHead class="th text-right w-24">Size</TableHead>
								<TableHead class="th text-right w-20">Pull</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{#each tags as tag}
								<TableRow class="cursor-pointer" onclick={() => openTagDetail(tag.name)}>
									<TableCell class="font-medium py-3 px-3">{tag.name}</TableCell>
									<TableCell class="py-3 px-3">
										<div class="flex items-center gap-1">
											<span class="font-mono text-xs text-muted-foreground block truncate">
												{truncateDigest(tag.digest)}
											</span>
											<CopyButton text="docker pull {pullCommand}@{tag.digest}" label="Digest pull copied!" />
										</div>
									</TableCell>
									<TableCell class="text-right text-sm py-3 px-3 tabular-nums">
										{formatBytes(Number(tag.sizeBytes))}
									</TableCell>
									<TableCell class="text-right py-3 px-3" onclick={(e: MouseEvent) => e.stopPropagation()}>
										<CopyButton text="docker pull {pullCommand}:{tag.name}" label="Pull command copied!" />
									</TableCell>
								</TableRow>
							{/each}
						</TableBody>
					</Table>
				</div>

				<DataPagination
					page={tagsPage} pageSize={tagsPageSize} totalCount={tagsTotalCount}
					onPrev={() => { if (tagsPage > 1) { tagsPage--; loadTags(); } }}
					onNext={() => { if (tagsPage * tagsPageSize < tagsTotalCount) { tagsPage++; loadTags(); } }}
				/>
			{/if}
		</div>
	{:else}
		<div class="text-center py-12">
			<div class="h-12 w-12 rounded-xl bg-muted/50 flex items-center justify-center mx-auto mb-4">
				<Package class="h-6 w-6 text-muted-foreground/50" />
			</div>
			<h2 class="text-lg font-semibold">Repository not found</h2>
			<p class="text-[13px] text-muted-foreground mt-1">
				{namespace}/{name} does not exist or you don't have access.
			</p>
			<Button variant="outline" class="mt-4" onclick={() => goto('/')}>Back to Explore</Button>
		</div>
	{/if}
</div>

<!-- Tag Detail Sheet -->
<Sheet bind:open={sheetOpen}>
	<SheetContent side="right" class="w-full sm:max-w-lg overflow-y-auto p-0">
		<div class="px-6 py-5 border-b border-border/40 bg-muted/20">
			<SheetTitle class="text-lg font-semibold tracking-tight">
				{#if selectedTagDetail}
					{namespace}/{name}:{selectedTagDetail.name}
				{:else}
					Tag Detail
				{/if}
			</SheetTitle>
			<SheetDescription class="text-[13px] text-muted-foreground mt-1">Image manifest details</SheetDescription>
		</div>

		{#if detailLoading}
			<div class="space-y-4 p-6">
				<Skeleton class="h-6 w-full" />
				<Skeleton class="h-6 w-3/4" />
				<Skeleton class="h-6 w-1/2" />
			</div>
		{:else if selectedTagDetail}
			<div class="p-6 space-y-6">
				<div class="space-y-3">
					<div class="detail-row">
						<span class="detail-label">Digest</span>
						<div class="detail-value flex items-center gap-1 min-w-0">
							<code class="text-xs font-mono truncate">{selectedTagDetail.digest}</code>
							<CopyButton text={selectedTagDetail.digest} label="Digest copied!" />
						</div>
					</div>
					<div class="detail-row">
						<span class="detail-label">Media Type</span>
						<code class="detail-value text-xs font-mono truncate">{selectedTagDetail.mediaType}</code>
					</div>
					<div class="detail-row">
						<span class="detail-label">Size</span>
						<span class="detail-value tabular-nums">{formatBytes(Number(selectedTagDetail.sizeBytes))}</span>
					</div>
					{#if selectedTagDetail.architecture}
						<div class="detail-row">
							<span class="detail-label">Architecture</span>
							<span class="detail-value flex items-center gap-1">
								<Cpu class="h-3.5 w-3.5 text-muted-foreground" />{selectedTagDetail.architecture}
							</span>
						</div>
					{/if}
					{#if selectedTagDetail.os}
						<div class="detail-row">
							<span class="detail-label">OS</span>
							<span class="detail-value flex items-center gap-1">
								<Monitor class="h-3.5 w-3.5 text-muted-foreground" />{selectedTagDetail.os}
							</span>
						</div>
					{/if}
				</div>

				{#if selectedTagDetail.layers.length > 0}
					<Separator />
					<div class="space-y-3">
						<div class="flex items-center gap-2">
							<Layers class="h-4 w-4 text-muted-foreground" />
							<h4 class="text-sm font-semibold">Layers ({selectedTagDetail.layers.length})</h4>
						</div>
						<div class="rounded-xl border overflow-hidden">
							<Table class="table-fixed">
								<TableHeader>
									<TableRow class="bg-muted/30 hover:bg-muted/30">
										<TableHead class="th">Digest</TableHead>
										<TableHead class="th text-right w-24">Size</TableHead>
									</TableRow>
								</TableHeader>
								<TableBody>
									{#each selectedTagDetail.layers as layer}
										<TableRow>
											<TableCell class="font-mono text-xs py-2.5 px-3">
												<span class="block truncate">{layer.digest}</span>
											</TableCell>
											<TableCell class="text-right text-sm py-2.5 px-3 tabular-nums">
												{formatBytes(Number(layer.sizeBytes))}
											</TableCell>
										</TableRow>
									{/each}
								</TableBody>
							</Table>
						</div>
					</div>
				{/if}
			</div>
		{/if}
	</SheetContent>
</Sheet>

<!-- Delete Repo -->
<ConfirmDialog bind:open={deleteRepoOpen} title="Delete Repository" confirmLabel="Delete" onConfirm={confirmDeleteRepo} loading={deletingRepo} icon={Trash2}>
	{#snippet description()}
		Are you sure you want to delete <strong>{namespace}/{name}</strong>? This will permanently
		remove all tags and images. This action cannot be undone.
	{/snippet}
</ConfirmDialog>

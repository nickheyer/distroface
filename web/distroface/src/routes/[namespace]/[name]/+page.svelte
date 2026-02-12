<script lang="ts">
	import { page } from '$app/state';
	import { onMount } from 'svelte';
	import {
		Package,
		ArrowLeft,
		ArrowDown,
		ArrowUp,
		Eye,
		Lock,
		Pencil,
		Check,
		X,
		Layers,
		Cpu,
		Monitor
	} from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { formatBytes, pageToToken } from '$lib/utils';
	import { toast } from 'svelte-sonner';
	import { Card, CardContent } from '$lib/components/ui/card';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Separator } from '$lib/components/ui/separator';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import {
		Table,
		TableBody,
		TableCell,
		TableHead,
		TableHeader,
		TableRow
	} from '$lib/components/ui/table';
	import {
		Sheet,
		SheetContent,
		SheetHeader,
		SheetTitle,
		SheetDescription
	} from '$lib/components/ui/sheet';
	import CopyButton from '$lib/components/copy-button.svelte';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import type { Repository, Tag, TagDetail } from '$lib/proto/distroface/v1/types_pb';

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

	const isOwner = $derived(
		authStore.user && (authStore.user.username === namespace || authStore.user.role === 2)
	);

	const pullCommand = $derived(`docker pull ${configStore.get('registryHost', 'localhost:8080')}/${namespace}/${name}`);

	async function loadRepo() {
		loading = true;
		try {
			const resp = await rpcClient.repository.getRepository({ namespace, name });
			repo = resp.repository;
		} catch (err: any) {
			toast.error(err.message || 'Failed to load repository');
		} finally {
			loading = false;
		}
	}

	async function loadTags() {
		tagsLoading = true;
		try {
			const resp = await rpcClient.repository.listTags({
				namespace,
				name,
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
			const resp = await rpcClient.repository.getTagDetail({
				namespace,
				name,
				tag: tagName
			});
			selectedTagDetail = resp.detail;
		} catch (err: any) {
			toast.error(err.message || 'Failed to load tag detail');
			sheetOpen = false;
		} finally {
			detailLoading = false;
		}
	}

	function startEditDescription() {
		descriptionDraft = repo?.description ?? '';
		editingDescription = true;
	}

	function cancelEditDescription() {
		editingDescription = false;
	}

	async function saveDescription() {
		savingDescription = true;
		try {
			const resp = await rpcClient.repository.updateRepository({
				namespace,
				name,
				description: descriptionDraft
			});
			repo = resp.repository;
			editingDescription = false;
			toast.success('Description updated');
		} catch (err: any) {
			toast.error(err.message || 'Failed to update description');
		} finally {
			savingDescription = false;
		}
	}

	function prevPage() {
		if (tagsPage > 1) {
			tagsPage--;
			loadTags();
		}
	}

	function nextPage() {
		if (tagsPage * tagsPageSize < tagsTotalCount) {
			tagsPage++;
			loadTags();
		}
	}

	onMount(() => {
		loadRepo();
		loadTags();
	});
</script>

<div class="flex-1 space-y-6 h-full p-6">
	<!-- Header -->
	<div class="flex items-center gap-4 pb-4 border-b border-border/40">
		<a href="/{namespace}" class="text-muted-foreground hover:text-foreground transition-colors">
			<ArrowLeft class="h-5 w-5" />
		</a>
		<div class="h-14 w-14 rounded-2xl bg-linear-to-br from-primary/20 to-primary/10 flex items-center justify-center shadow-lg">
			<Package class="h-7 w-7 text-primary" />
		</div>
		<div class="space-y-1 flex-1">
			{#if loading}
				<Skeleton class="h-8 w-64" />
				<Skeleton class="h-4 w-32" />
			{:else if repo}
				<div class="flex items-center gap-3">
					<h2 class="text-3xl font-bold tracking-tight">
						<a href="/{namespace}" class="text-muted-foreground hover:text-foreground transition-colors">{namespace}</a>
						<span class="text-muted-foreground">/</span>
						{name}
					</h2>
					<Badge variant="outline" class="text-xs">
						{#if repo.visibility === 2}
							<Lock class="h-3 w-3 mr-1" />private
						{:else}
							<Eye class="h-3 w-3 mr-1" />public
						{/if}
					</Badge>
				</div>
				<div class="flex items-center gap-4 text-sm text-muted-foreground">
					{#if repo.pushCount > 0}
						<span class="flex items-center gap-1"><ArrowUp class="h-3.5 w-3.5" />{repo.pushCount} pushes</span>
					{/if}
					{#if repo.pullCount > 0}
						<span class="flex items-center gap-1"><ArrowDown class="h-3.5 w-3.5" />{repo.pullCount} pulls</span>
					{/if}
					{#if repo.lastPushedAt}
						<span>Last pushed {timestampDate(repo.lastPushedAt).toLocaleDateString()}</span>
					{/if}
				</div>
			{/if}
		</div>
	</div>

	{#if !loading && repo}
		<!-- Description -->
		<div class="space-y-2">
			<div class="flex items-center gap-2">
				<h3 class="text-sm font-medium text-muted-foreground">Description</h3>
				{#if isOwner && !editingDescription}
					<Button variant="ghost" size="icon" class="h-6 w-6" onclick={startEditDescription}>
						<Pencil class="h-3 w-3" />
					</Button>
				{/if}
			</div>
			{#if editingDescription}
				<div class="flex gap-2">
					<Input
						bind:value={descriptionDraft}
						placeholder="Add a description..."
						class="flex-1"
					/>
					<Button size="sm" onclick={saveDescription} disabled={savingDescription}>
						<Check class="h-4 w-4" />
					</Button>
					<Button size="sm" variant="ghost" onclick={cancelEditDescription}>
						<X class="h-4 w-4" />
					</Button>
				</div>
			{:else}
				<p class="text-sm {repo.description ? '' : 'text-muted-foreground italic'}">
					{repo.description || 'No description'}
				</p>
			{/if}
		</div>

		<!-- Pull Command -->
		<Card class="border-border/50">
			<CardContent class="flex items-center gap-2 py-3">
				<code class="flex-1 text-sm bg-muted px-3 py-1.5 rounded font-mono">{pullCommand}</code>
				<CopyButton text={pullCommand} label="Pull command copied!" />
			</CardContent>
		</Card>

		<Separator />

		<!-- Tags -->
		<div class="space-y-4">
			<div class="flex items-center justify-between">
				<h3 class="text-lg font-semibold">Tags</h3>
				{#if tagsTotalCount > 0}
					<span class="text-sm text-muted-foreground">{tagsTotalCount} total</span>
				{/if}
			</div>

			{#if tagsLoading}
				<div class="space-y-2">
					{#each Array(3) as _}
						<Skeleton class="h-12 w-full" />
					{/each}
				</div>
			{:else if tags.length === 0}
				<Card class="border-dashed">
					<CardContent class="flex flex-col items-center justify-center py-12 text-center">
						<Package class="h-12 w-12 text-muted-foreground/50 mb-4" />
						<p class="text-muted-foreground">No tags yet</p>
						<p class="text-sm text-muted-foreground mt-1">
							Push an image: <code class="bg-muted px-1.5 py-0.5 rounded text-xs">{pullCommand}:latest</code>
						</p>
					</CardContent>
				</Card>
			{:else}
				<div class="rounded-md border overflow-hidden">
					<Table class="table-fixed">
						<TableHeader>
							<TableRow>
								<TableHead class="w-32">Tag</TableHead>
								<TableHead>Digest</TableHead>
								<TableHead class="text-right w-24">Size</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{#each tags as tag}
								<TableRow
									class="cursor-pointer hover:bg-muted/50"
									onclick={() => openTagDetail(tag.name)}
								>
									<TableCell class="font-medium">{tag.name}</TableCell>
									<TableCell class="font-mono text-xs text-muted-foreground">
										<span class="block truncate">{tag.digest}</span>
									</TableCell>
									<TableCell class="text-right">{formatBytes(Number(tag.sizeBytes))}</TableCell>
								</TableRow>
							{/each}
						</TableBody>
					</Table>
				</div>

				<!-- Pagination -->
				{#if tagsTotalCount > tagsPageSize}
					<div class="flex items-center justify-between">
						<span class="text-sm text-muted-foreground">
							Page {tagsPage} of {Math.ceil(tagsTotalCount / tagsPageSize)}
						</span>
						<div class="flex gap-2">
							<Button variant="outline" size="sm" disabled={tagsPage <= 1} onclick={prevPage}>
								Previous
							</Button>
							<Button
								variant="outline"
								size="sm"
								disabled={tagsPage * tagsPageSize >= tagsTotalCount}
								onclick={nextPage}
							>
								Next
							</Button>
						</div>
					</div>
				{/if}
			{/if}
		</div>
	{/if}
</div>

<!-- Tag Detail Sheet -->
<Sheet bind:open={sheetOpen}>
	<SheetContent side="right" class="w-full sm:max-w-lg overflow-y-auto p-6">
		<SheetHeader>
			<SheetTitle>
				{#if selectedTagDetail}
					{namespace}/{name}:{selectedTagDetail.name}
				{:else}
					Tag Detail
				{/if}
			</SheetTitle>
			<SheetDescription>
				Image manifest details
			</SheetDescription>
		</SheetHeader>

		{#if detailLoading}
			<div class="space-y-4 mt-6">
				<Skeleton class="h-6 w-full" />
				<Skeleton class="h-6 w-3/4" />
				<Skeleton class="h-6 w-1/2" />
			</div>
		{:else if selectedTagDetail}
			<div class="space-y-6 mt-6">
				<!-- Overview -->
				<div class="space-y-3">
					<div class="flex items-center gap-2 min-w-0">
						<span class="text-sm font-medium text-muted-foreground w-24 shrink-0">Digest</span>
						<div class="flex items-center gap-1 flex-1 min-w-0">
							<code class="text-xs font-mono truncate">{selectedTagDetail.digest}</code>
							<CopyButton text={selectedTagDetail.digest} label="Digest copied!" />
						</div>
					</div>
					<div class="flex items-center gap-2 min-w-0">
						<span class="text-sm font-medium text-muted-foreground w-24 shrink-0">Media Type</span>
						<code class="text-xs font-mono truncate">{selectedTagDetail.mediaType}</code>
					</div>
					<div class="flex items-center gap-2">
						<span class="text-sm font-medium text-muted-foreground w-24">Size</span>
						<span class="text-sm">{formatBytes(Number(selectedTagDetail.sizeBytes))}</span>
					</div>
					{#if selectedTagDetail.architecture}
						<div class="flex items-center gap-2">
							<span class="text-sm font-medium text-muted-foreground w-24">Architecture</span>
							<span class="text-sm flex items-center gap-1">
								<Cpu class="h-3.5 w-3.5" />{selectedTagDetail.architecture}
							</span>
						</div>
					{/if}
					{#if selectedTagDetail.os}
						<div class="flex items-center gap-2">
							<span class="text-sm font-medium text-muted-foreground w-24">OS</span>
							<span class="text-sm flex items-center gap-1">
								<Monitor class="h-3.5 w-3.5" />{selectedTagDetail.os}
							</span>
						</div>
					{/if}
				</div>

				<Separator />

				<!-- Layers -->
				{#if selectedTagDetail.layers.length > 0}
					<div class="space-y-3">
						<div class="flex items-center gap-2">
							<Layers class="h-4 w-4 text-muted-foreground" />
							<h4 class="text-sm font-medium">Layers ({selectedTagDetail.layers.length})</h4>
						</div>
						<div class="rounded-md border overflow-hidden">
							<Table class="table-fixed">
								<TableHeader>
									<TableRow>
										<TableHead>Digest</TableHead>
										<TableHead class="text-right w-24">Size</TableHead>
									</TableRow>
								</TableHeader>
								<TableBody>
									{#each selectedTagDetail.layers as layer}
										<TableRow>
											<TableCell class="font-mono text-xs">
												<span class="block truncate">{layer.digest}</span>
											</TableCell>
											<TableCell class="text-right text-sm">
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

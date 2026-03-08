<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { onMount, tick } from 'svelte';
	import {
		Package, ArrowDown, ArrowUp, Eye, Lock, Pencil, Check, X,
		Trash2, MoreHorizontal, EyeOff, ChevronRight,
		Tags, Clock, Terminal
	} from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import { formatBytes, pageToToken, truncateDigest, relativeTime } from '$lib/utils';
	import { toast } from 'svelte-sonner';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Skeleton } from '$lib/components/ui/skeleton';

	import {
		DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger
	} from '$lib/components/ui/dropdown-menu';
	import {
		Table, TableBody, TableCell, TableHead, TableHeader, TableRow
	} from '$lib/components/ui/table';
	import {
		Sheet, SheetContent, SheetTitle, SheetDescription
	} from '$lib/components/ui/sheet';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import CopyButton from '$lib/components/copy-button.svelte';
	import DescriptorPanel from '$lib/components/descriptor-panel.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import WebhookManager from '$lib/components/webhook-manager.svelte';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import type { Repository, Tag, Descriptor, HistoryEntry } from '$lib/proto/distroface/v1/types_pb';
	import { Visibility, WebhookScope } from '$lib/proto/distroface/v1/types_pb';
	import { resolve } from '$app/paths';

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
	let selectedTagName = $state('');

	interface PanelEntry {
		descriptor?: Descriptor;
		loading: boolean;
		label: string;
		digest: string;
		historyEntry?: HistoryEntry;
	}
	let panelStack = $state<PanelEntry[]>([]);
	let panelScroll = $state<HTMLElement | null>(null);

	let deleteRepoOpen = $state(false);
	let deletingRepo = $state(false);

	const registryHost = $derived(configStore.get('registryHost', 'localhost:8080') as string);

	const namespaceHref = $derived(repo?.isOrgNamespace ? `orgs/${namespace}` : `${namespace}`);
	const isNamespaceMember = $derived(authStore.user?.username === namespace);

	const canUpdateRepo = $derived(
		authStore.user !== null &&
			(isNamespaceMember && authStore.hasPermission('repositories', 'update', `${namespace}/${name}`) ||
				authStore.hasPermission('repositories', 'manage', `${namespace}/${name}`))
	);

	const canDeleteRepo = $derived(
		authStore.user !== null &&
			(isNamespaceMember && authStore.hasPermission('repositories', 'delete', `${namespace}/${name}`) ||
				authStore.hasPermission('repositories', 'manage', `${namespace}/${name}`))
	);

	const canManage = $derived(canUpdateRepo || canDeleteRepo);
	const pullCommand = $derived(`${registryHost}/${namespace}/${name}`);
	const isPrivate = $derived(repo?.visibility === Visibility.PRIVATE);
	const initials = $derived((namespace ?? '').slice(0, 2).toUpperCase());

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
		selectedTagName = tagName;
		panelStack = [{ loading: true, label: tagName, digest: '' }];
		try {
			const resp = await rpcClient.repository.resolveTag({ namespace, name, tag: tagName });
			if (resp.descriptor) {
				panelStack = [{ descriptor: resp.descriptor, loading: false, label: tagName, digest: resp.descriptor.digest }];
			}
		} catch {
			sheetOpen = false;
			panelStack = [];
		}
	}

	function expandToPanel(panelIndex: number, child: Descriptor) {
		if (panelStack[panelIndex + 1]?.digest === child.digest) {
			panelStack = panelStack.slice(0, panelIndex + 1);
			return;
		}

		// Correlate layer children to their parent manifest's build history
		const parentDesc = panelStack[panelIndex].descriptor;
		let historyEntry: HistoryEntry | undefined;
		if (parentDesc?.imageConfig?.history.length) {
			let layerIdx = -1;
			for (let i = 1; i < parentDesc.children.length; i++) {
				if (parentDesc.children[i].digest === child.digest) {
					layerIdx = i - 1;
					break;
				}
			}
			if (layerIdx >= 0) {
				let count = 0;
				for (const h of parentDesc.imageConfig.history) {
					if (!h.emptyLayer) {
						if (count === layerIdx) { historyEntry = h; break; }
						count++;
					}
				}
			}
		}

		const label = truncateDigest(child.digest, 12);
		panelStack = [...panelStack.slice(0, panelIndex + 1), { descriptor: child, loading: false, label, digest: child.digest, historyEntry }];
		tick().then(() => panelScroll?.scrollTo({ left: panelScroll.scrollWidth, behavior: 'smooth' }));
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
			goto(resolve(`/${namespaceHref}`));
		} catch {
			// error interceptor
		} finally {
			deletingRepo = false;
		}
	}

	onMount(() => {
		loadRepo();
		loadTags();
	});
</script>

<div class="space-y-6">
	{#if loading}
		<div class="space-y-3">
			<Skeleton class="h-5 w-48" />
			<div class="flex items-start gap-4">
				<Skeleton class="h-14 w-14 rounded-xl" />
				<div class="space-y-2 flex-1">
					<Skeleton class="h-7 w-64" />
					<Skeleton class="h-4 w-96" />
				</div>
			</div>
		</div>
	{:else if repo}
		<!-- Breadcrumb -->
		<nav class="flex items-center gap-1.5 text-sm text-muted-foreground">
			<a href={resolve('/')} class="hover:text-foreground transition-colors">Explore</a>
			<span class="text-muted-foreground/30">/</span>
			<a href={resolve(`/${namespaceHref}`)} class="hover:text-foreground transition-colors">{namespace}</a>
			<span class="text-muted-foreground/30">/</span>
			<span class="text-foreground font-medium">{name}</span>
		</nav>

		<!-- Header card -->
		<div class="rounded-xl border border-border/60 bg-card overflow-hidden">
			<div class="p-5">
				<div class="flex items-start gap-4">
					<!-- Avatar -->
					<div class="h-14 w-14 rounded-xl bg-linear-to-br from-primary/15 to-primary/5 flex items-center justify-center shrink-0 border border-primary/10">
						<span class="text-base font-bold text-primary/70 uppercase tracking-wide">{initials}</span>
					</div>

					<!-- Info -->
					<div class="flex-1 min-w-0 space-y-1">
						<div class="flex items-center gap-3">
							<h1 class="text-xl font-bold tracking-tight">
								<span class="text-muted-foreground font-normal">{namespace}/</span>{name}
							</h1>
							<Badge
								variant="outline"
								class="text-[10px] shrink-0 gap-0.5 py-0 h-4.5 {isPrivate
									? 'border-amber-500/30 text-amber-600 dark:text-amber-400'
									: 'border-primary/20 text-primary/60 dark:text-primary/70'}"
							>
								{#if isPrivate}
									<Lock class="h-2.5 w-2.5" />Private
								{:else}
									<Eye class="h-2.5 w-2.5" />Public
								{/if}
							</Badge>
						</div>

						<!-- Description -->
						<div>
							{#if editingDescription}
								<div class="flex gap-2 max-w-lg">
									<Input bind:value={descriptionDraft} placeholder="Add a description..." class="flex-1 h-8 text-sm" />
									<Button size="sm" class="h-8 px-2.5" onclick={saveDescription} disabled={savingDescription}>
										<Check class="h-3.5 w-3.5" />
									</Button>
									<Button size="sm" variant="ghost" class="h-8 px-2.5" onclick={() => (editingDescription = false)}>
										<X class="h-3.5 w-3.5" />
									</Button>
								</div>
							{:else}
								<div class="flex items-center gap-1">
									<p class="text-[13px] {repo.description ? 'text-muted-foreground' : 'text-muted-foreground/50 italic'}">
										{repo.description || 'No description'}
									</p>
									<PermissionGate allowed={canUpdateRepo}>
										<Button variant="ghost" size="icon" class="h-6 w-6" onclick={startEditDescription}>
											<Pencil class="h-3 w-3 text-muted-foreground/50" />
										</Button>
									</PermissionGate>
								</div>
							{/if}
						</div>

						<!-- Stats row -->
						<div class="flex items-center gap-4 text-[12px] text-muted-foreground/60 pt-0.5">
							{#if repo.tagCount > 0}
								<span class="flex items-center gap-1">
									<Tags class="h-3 w-3" />{repo.tagCount} tag{repo.tagCount !== 1 ? 's' : ''}
								</span>
							{/if}
							{#if Number(repo.sizeBytes) > 0}
								<span class="tabular-nums">{formatBytes(Number(repo.sizeBytes))}</span>
							{/if}
							{#if repo.pullCount > 0n}
								<span class="flex items-center gap-1 tabular-nums">
									<ArrowDown class="h-3 w-3" />{repo.pullCount.toLocaleString()} pull{repo.pullCount !== 1n ? 's' : ''}
								</span>
							{/if}
							{#if repo.pushCount > 0n}
								<span class="flex items-center gap-1 tabular-nums">
									<ArrowUp class="h-3 w-3" />{repo.pushCount.toLocaleString()} push{repo.pushCount !== 1n ? 'es' : ''}
								</span>
							{/if}
							{#if repo.lastPushedAt}
								<span class="flex items-center gap-1">
									<Clock class="h-3 w-3" />Updated {relativeTime(timestampDate(repo.lastPushedAt))}
								</span>
							{/if}
						</div>
					</div>

					<!-- Actions -->
					<PermissionGate allowed={canManage}>
						<DropdownMenu>
							<DropdownMenuTrigger>
								{#snippet child({ props })}
									<Button {...props} variant="outline" size="icon" class="h-8 w-8 shrink-0">
										<MoreHorizontal class="h-4 w-4" />
									</Button>
								{/snippet}
							</DropdownMenuTrigger>
							<DropdownMenuContent align="end">
								<PermissionGate allowed={canUpdateRepo}>
									<DropdownMenuItem onclick={toggleVisibility}>
										{#if isPrivate}
											<Eye class="h-4 w-4 mr-2" />Make Public
										{:else}
											<EyeOff class="h-4 w-4 mr-2" />Make Private
										{/if}
									</DropdownMenuItem>
								</PermissionGate>
								<PermissionGate allowed={canDeleteRepo}>
									<DropdownMenuSeparator />
									<DropdownMenuItem class="text-destructive focus:text-destructive" onclick={() => (deleteRepoOpen = true)}>
										<Trash2 class="h-4 w-4 mr-2" />Delete Repository
									</DropdownMenuItem>
								</PermissionGate>
							</DropdownMenuContent>
						</DropdownMenu>
					</PermissionGate>
				</div>
			</div>

			<!-- Pull command bar -->
			<div class="border-t border-border/40 bg-muted/20 px-5 py-3 flex items-center gap-3">
				<Terminal class="h-3.5 w-3.5 text-muted-foreground/50 shrink-0" />
				<code class="text-[13px] font-mono text-muted-foreground flex-1 min-w-0 truncate select-all">docker pull {pullCommand}:latest</code>
				<CopyButton text="docker pull {pullCommand}:latest" label="Pull command copied!" />
			</div>
		</div>

		<!-- Tags section -->
		<div class="space-y-4">
			<div class="section-header">
				<h2 class="section-title">Tags</h2>
				{#if tagsTotalCount > 0}
					<span class="text-[12px] text-muted-foreground/60 tabular-nums">{tagsTotalCount} tag{tagsTotalCount !== 1 ? 's' : ''}</span>
				{/if}
			</div>

			{#if tagsLoading}
				<div class="space-y-2">
					{#each [0, 1, 2] as i (i)}
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
							<TableRow>
								<TableHead class="th w-40">Tag</TableHead>
								<TableHead class="th">Digest</TableHead>
								<TableHead class="th w-32 hidden md:table-cell">Platform</TableHead>
								<TableHead class="th text-right w-24">Size</TableHead>
								<TableHead class="th w-10"></TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{#each tags as tag (tag.name)}
								<TableRow class="cursor-pointer group/row" onclick={() => openTagDetail(tag.name)}>
									<TableCell class="py-3 px-3">
										<Badge variant="secondary" class="font-mono text-xs font-medium px-2 py-0.5">{tag.name}</Badge>
									</TableCell>
									<TableCell class="py-3 px-3">
										<span class="font-mono text-xs text-muted-foreground/70 block truncate">
											{truncateDigest(tag.digest)}
										</span>
									</TableCell>
									<TableCell class="py-3 px-3 hidden md:table-cell">
										{#if tag.platforms.length > 0}
											<div class="flex flex-wrap gap-1">
												{#each tag.platforms as p, pi (pi)}
													<Badge variant="outline" class="text-[10px] font-mono py-0 h-4.5 text-muted-foreground/70">
														{p.os ?? ''}{#if p.os && p.architecture}/{/if}{p.architecture ?? ''}{#if p.variant}/{p.variant}{/if}
													</Badge>
												{/each}
											</div>
										{/if}
									</TableCell>
									<TableCell class="text-right text-[13px] py-3 px-3 tabular-nums text-muted-foreground">
										{formatBytes(Number(tag.sizeBytes))}
									</TableCell>
									<TableCell class="py-3 px-3" onclick={(e: MouseEvent) => e.stopPropagation()}>
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
		<!-- Webhooks section (visible to repo managers) -->
		{#if repo && canUpdateRepo}
			<PermissionGate allowed={canUpdateRepo}>
				<WebhookManager
					scope={WebhookScope.REPOSITORY}
					scopeId={repo.id}
					createDescription="Receive HTTP POST notifications for repository events."
				/>
			</PermissionGate>
		{/if}

	{:else}
		<div class="text-center py-16">
			<div class="h-14 w-14 rounded-xl bg-muted/50 flex items-center justify-center mx-auto mb-4">
				<Package class="h-7 w-7 text-muted-foreground/40" />
			</div>
			<h2 class="text-lg font-semibold">Repository not found</h2>
			<p class="text-[13px] text-muted-foreground mt-1">
				{namespace}/{name} does not exist or you don't have access.
			</p>
			<Button variant="outline" class="mt-4" onclick={() => goto(resolve('/'))}>Back to Explore</Button>
		</div>
	{/if}
</div>

<!-- Tag Detail Sheet -->
<Sheet bind:open={sheetOpen}>
	<SheetContent
		side="right"
		class="w-full overflow-hidden p-0 flex flex-col"
		style="max-width: min({panelStack.length * 32}rem, 85vw); transition: max-width 200ms ease-in-out;"
	>
		<div class="px-6 py-5 border-b border-border/40 bg-muted/20 shrink-0 space-y-3">
			<div>
				<SheetTitle class="text-lg font-semibold tracking-tight flex items-baseline gap-1.5 flex-wrap">
					<span>
						<span class="text-muted-foreground font-normal">{namespace}/{name}:</span><span 
							class="transition-colors {panelStack.length > 1 ? 'cursor-pointer hover:text-muted-foreground' : ''}"
							onclick={() => { if (panelStack.length > 1) panelStack = panelStack.slice(0, 1); }}>{panelStack[0]?.label ?? selectedTagName}
						</span>
					</span>
					{#each panelStack as panel, i (panel.digest + i)}
						{#if i > 0}
							<ChevronRight class="h-3 w-3 shrink-0 text-muted-foreground/30 self-center" />
							<span
								class="text-sm font-normal font-mono transition-colors
									{i === panelStack.length - 1 ? 'text-foreground' : 'text-muted-foreground cursor-pointer hover:text-foreground'}"
								onclick={() => { if (i < panelStack.length - 1) panelStack = panelStack.slice(0, i + 1); }}
							>{panel.label}</span>
						{/if}
					{/each}
				</SheetTitle>
				<SheetDescription class="text-[13px] text-muted-foreground mt-1">Image manifest and layer details</SheetDescription>
			</div>

			<div class="rounded-lg bg-muted/30 border border-border/40 px-3.5 py-2.5 flex items-center gap-2.5">
				<Terminal class="h-3.5 w-3.5 text-muted-foreground/50 shrink-0" />
				<code class="text-[13px] font-mono text-muted-foreground flex-1 min-w-0 truncate select-all">docker pull {pullCommand}:{selectedTagName}</code>
				<CopyButton text="docker pull {pullCommand}:{selectedTagName}" label="Pull command copied!" />
			</div>

		</div>

		<div bind:this={panelScroll} class="flex-1 flex overflow-x-hidden overflow-y-auto min-h-0">
			{#each panelStack as panel, i (panel.digest + i)}
				<div class="{panelStack.length === 1 ? 'flex-1' : 'w-lg shrink-0'} border-r border-border/30 overflow-y-auto last:border-r-0">
					<DescriptorPanel
						descriptor={panel.descriptor}
						loading={panel.loading}
						selectedDigest={panelStack[i + 1]?.digest}
						onSelectChild={(child: Descriptor) => expandToPanel(i, child)}
						historyEntry={panel.historyEntry}
					/>
				</div>
			{/each}
		</div>
	</SheetContent>
</Sheet>

<!-- Delete Repo -->
<ConfirmDialog bind:open={deleteRepoOpen} title="Delete Repository" confirmLabel="Delete" onConfirm={confirmDeleteRepo} loading={deletingRepo} icon={Trash2}>
	{#snippet description()}
		Are you sure you want to delete <strong>{namespace}/{name}</strong>? This will permanently
		remove all tags and images. This action cannot be undone.
	{/snippet}
</ConfirmDialog>


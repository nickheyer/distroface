<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { onMount, tick } from 'svelte';
	import {
		Package, ArrowDown, ArrowUp, ArrowUpDown, Eye, Lock, Pencil, Check, X,
		Trash2, MoreHorizontal, EyeOff, ChevronRight,
		Tags, Clock, Terminal, Star, HardDriveDownload, RefreshCw, Square
	} from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { portalStore } from '$lib/stores/portal.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import { formatBytes, truncateDigest, relativeTime } from '$lib/utils';
	import { Pager } from '$lib/pager.svelte';
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
	import FormPanel from '$lib/components/form-panel.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import MirrorBadge from '$lib/components/mirror-badge.svelte';
	import MirrorConfigFields, {
		emptyMirrorForm, mirrorFormFrom, mirrorInit
	} from '$lib/components/mirror-config-fields.svelte';
	import DescriptorPanel from '$lib/components/descriptor-panel.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import WebhookManager from '$lib/components/webhook-manager.svelte';
	import { mirrorSyncStore } from '$lib/stores/mirror-sync.svelte';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import type { Repository, Tag, Descriptor, HistoryEntry } from '$lib/proto/distroface/v1/types_pb';
	import { RepositoryType, Visibility, WebhookScope } from '$lib/proto/distroface/v1/types_pb';
	import { resolve } from '$app/paths';

	const namespace = $derived(page.params.namespace);
	const name = $derived(page.params.name);

	let repo = $state<Repository | undefined>(undefined);
	let loading = $state(true);
	let tags = $state<Tag[]>([]);
	let tagsLoading = $state(true);
	const tagsPager = new Pager(20);
	let tagSort = $state('version desc');

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
	let starPending = $state(false);

	// Mirror settings
	let mirrorPanelOpen = $state(false);
	let mirrorForm = $state(emptyMirrorForm());
	let savingMirror = $state(false);
	let syncingMirror = $state(false);
	let stoppingMirror = $state(false);
	const repoIsMirror = $derived(repo?.type === RepositoryType.MIRROR);
	const mirrorSyncActive = $derived(
		!!repo && (repo.mirrorSyncing || mirrorSyncStore.syncing('image', repo.id))
	);

	async function syncMirrorNow() {
		if (syncingMirror) return;
		syncingMirror = true;
		try {
			await rpcClient.repository.syncRepository({ namespace, name });
			toast.success('Sync started');
		} catch {
			// Error interceptor already toasted
		} finally {
			syncingMirror = false;
		}
	}

	async function stopMirrorSync() {
		if (stoppingMirror) return;
		stoppingMirror = true;
		try {
			await rpcClient.repository.stopRepositorySync({ namespace, name });
			toast.success('Sync stopped');
		} catch {
			// Error interceptor already toasted
		} finally {
			stoppingMirror = false;
		}
	}

	// Sync finish events refresh the page live
	let syncSeqSeen = 0;
	$effect(() => {
		const seq = mirrorSyncStore.finishedSeq;
		const ev = mirrorSyncStore.lastFinished;
		if (seq === syncSeqSeen) return;
		syncSeqSeen = seq;
		if (ev?.kind === 'image' && repo && ev.repoId === repo.id) {
			loadRepo();
			loadTags();
		}
	});

	function openMirrorSettings() {
		if (!repo) return;
		mirrorForm = mirrorFormFrom(repo.mirror);
		mirrorPanelOpen = true;
	}

	async function saveMirrorSettings() {
		if (!mirrorForm.upstream.trim()) return;
		savingMirror = true;
		try {
			const resp = await rpcClient.repository.updateRepository({
				namespace,
				name,
				mirror: mirrorInit(mirrorForm)
			});
			repo = resp.repository ?? repo;
			toast.success('Mirror settings updated');
			mirrorPanelOpen = false;
		} catch {
			// Error interceptor already toasted
		} finally {
			savingMirror = false;
		}
	}

	const registryHost = $derived(
		portalStore.host(configStore.publicHostname)
	);

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
	const pullCommand = $derived(
		`${registryHost}/${portalStore.imageRef(namespace ?? '', name ?? '')}`
	);
	let pullTag = $state<string|null>(null);
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
				page: tagsPager.request(undefined, tagSort),
				namespace, name
			});
			tags = resp.tags;
			tagsPager.apply(resp.page);
		} catch {
			tags = [];
		} finally {
			tagsLoading = false;
		}
	}

	function toggleTagSort(col: string) {
		tagSort = tagSort === `${col} desc` ? `${col} asc` : `${col} desc`;
		tagsPager.reset();
		loadTags();
	}

	// Latest or newest version names the pull command
	async function loadPullTag() {
		try {
			const resp = await rpcClient.repository.listTags({
				page: { pageSize: 1, orderBy: 'version desc' },
				namespace, name
			});
			pullTag = resp.tags[0]?.name ?? null;
		} catch {
			pullTag = null;
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

	async function toggleStar() {
		if (!repo || starPending) return;
		starPending = true;
		try {
			if (repo.isStarred) {
				const resp = await rpcClient.repository.unstarRepository({ namespace, name });
				repo.isStarred = false;
				repo.starCount = resp.starCount;
			} else {
				const resp = await rpcClient.repository.starRepository({ namespace, name });
				repo.isStarred = true;
				repo.starCount = resp.starCount;
			}
		} catch {
			// error interceptor
		} finally {
			starPending = false;
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
		mirrorSyncStore.ensure();
		loadRepo();
		loadTags();
		loadPullTag();
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
			<a href={resolve('/')} class="hover:text-foreground transition-colors">Images</a>
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
							{#if repoIsMirror}
								<MirrorBadge
									label="Mirror"
									error={repo.mirrorLastError}
									title={repo.mirror?.upstream ?? ''}
									syncing={repo.mirrorSyncing || mirrorSyncStore.syncing('image', repo.id)}
								/>
							{/if}
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
					{#if authStore.isAuthenticated}
						<Button variant="outline" size="sm" class="h-8 shrink-0 gap-1.5" onclick={toggleStar} disabled={starPending}>
							<Star class="h-3.5 w-3.5 {repo.isStarred ? 'fill-amber-400 text-amber-400' : ''}" />
							{repo.isStarred ? 'Starred' : 'Star'}
							{#if repo.starCount > 0n}
								<span class="tabular-nums text-muted-foreground">{repo.starCount.toLocaleString()}</span>
							{/if}
						</Button>
					{:else if repo.starCount > 0n}
						<span class="flex items-center gap-1 text-[12px] text-muted-foreground/60 tabular-nums shrink-0 mt-2">
							<Star class="h-3 w-3" />{repo.starCount.toLocaleString()}
						</span>
					{/if}
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
									{#if repoIsMirror}
										<DropdownMenuItem onclick={openMirrorSettings}>
											<HardDriveDownload class="h-4 w-4 mr-2" />Mirror Settings
										</DropdownMenuItem>
										{#if mirrorSyncActive}
											<DropdownMenuItem onclick={stopMirrorSync} disabled={stoppingMirror}>
												<Square class="h-4 w-4 mr-2" />Stop Sync
											</DropdownMenuItem>
										{:else}
											<DropdownMenuItem onclick={syncMirrorNow} disabled={syncingMirror}>
												<RefreshCw class="h-4 w-4 mr-2 {syncingMirror ? 'animate-spin' : ''}" />Sync Now
											</DropdownMenuItem>
										{/if}
									{/if}
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
			{#if pullTag !== null}
				<div class="border-t border-border/40 bg-muted/20 px-5 py-3 flex items-center gap-3">
					<Terminal class="h-3.5 w-3.5 text-muted-foreground/50 shrink-0" />
					<code class="text-[13px] font-mono text-muted-foreground flex-1 min-w-0 truncate select-all">docker pull {pullCommand}:{pullTag}</code>
					<CopyButton text="docker pull {pullCommand}:{pullTag}" label="Pull command copied!" />
				</div>
			{/if}
		</div>

		{#snippet tagSortHeader(label: string, col: string)}
			<button type="button" class="inline-flex items-center gap-1 hover:text-foreground transition-colors" onclick={() => toggleTagSort(col)}>
				{label}
				{#if tagSort === `${col} desc`}
					<ArrowDown class="h-3 w-3" />
				{:else if tagSort === `${col} asc`}
					<ArrowUp class="h-3 w-3" />
				{:else}
					<ArrowUpDown class="h-3 w-3 opacity-40" />
				{/if}
			</button>
		{/snippet}

		<!-- Tags section -->
		<div class="space-y-4">
			<div class="section-header">
				<h2 class="section-title">Tags</h2>
				{#if tagsPager.totalCount > 0}
					<span class="text-[12px] text-muted-foreground/60 tabular-nums">{tagsPager.totalCount} tag{tagsPager.totalCount !== 1 ? 's' : ''}</span>
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
								<TableHead class="th">{@render tagSortHeader('Tag', 'version')}</TableHead>
								<TableHead class="th w-56 hidden sm:table-cell">Digest</TableHead>
								<TableHead class="th w-40 hidden md:table-cell">Platform</TableHead>
								<TableHead class="th text-right w-24">
									<div class="flex justify-end">{@render tagSortHeader('Size', 'size')}</div>
								</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{#each tags as tag (tag.name)}
								<TableRow class="cursor-pointer group/row" onclick={() => openTagDetail(tag.name)}>
									<TableCell class="py-3 px-3">
										<div class="flex items-center gap-1 min-w-0">
											<Badge variant="secondary" class="font-mono text-xs font-medium px-2 py-0.5 min-w-0 shrink">
												<span class="truncate">{tag.name}</span>
											</Badge>
											<CopyButton text="docker pull {pullCommand}:{tag.name}" label="Pull command copied!" />
										</div>
									</TableCell>
									<TableCell class="py-3 px-3 hidden sm:table-cell">
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
								</TableRow>
							{/each}
						</TableBody>
					</Table>
					<DataPagination attached pager={tagsPager} onChange={loadTags} />
				</div>
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
			<Button variant="outline" class="mt-4" onclick={() => goto(resolve('/'))}>Back to Images</Button>
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
						<span class="text-muted-foreground font-normal">{namespace}/{name}:</span><button
							type="button"
							class="transition-colors {panelStack.length > 1 ? 'cursor-pointer hover:text-muted-foreground' : 'cursor-default'}"
							onclick={() => { if (panelStack.length > 1) panelStack = panelStack.slice(0, 1); }}>{panelStack[0]?.label ?? selectedTagName}
						</button>
					</span>
					{#each panelStack as panel, i (panel.digest + i)}
						{#if i > 0}
							<ChevronRight class="h-3 w-3 shrink-0 text-muted-foreground/30 self-center" />
							<button
								type="button"
								class="text-sm font-normal font-mono transition-colors
									{i === panelStack.length - 1 ? 'text-foreground cursor-default' : 'text-muted-foreground cursor-pointer hover:text-foreground'}"
								onclick={() => { if (i < panelStack.length - 1) panelStack = panelStack.slice(0, i + 1); }}
							>{panel.label}</button>
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

		<div bind:this={panelScroll} class="flex-1 flex overflow-x-auto overflow-y-hidden min-h-0">
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

<!-- Mirror settings panel -->
<FormPanel
	open={mirrorPanelOpen}
	onOpenChange={(v) => (mirrorPanelOpen = v)}
	title="Mirror Settings"
	description="Upstream source for {namespace}/{name}."
	icon={HardDriveDownload}
>
	<div class="space-y-6">
		<FormSection title="Mirror Source">
			<MirrorConfigFields
				form={mirrorForm}
				kind="oci"
				tokenSet={repo?.mirror?.authTokenSet ?? false}
				lastSync={repo?.mirrorLastSync}
				lastError={repo?.mirrorLastError ?? ''}
				nextAttempt={repo?.mirrorNextAttempt}
				idPrefix="image-mirror"
			/>
		</FormSection>
	</div>

	{#snippet footer()}
		<Button variant="outline" onclick={() => (mirrorPanelOpen = false)}>Cancel</Button>
		<Button onclick={saveMirrorSettings} disabled={savingMirror || !mirrorForm.upstream.trim()}>
			{savingMirror ? 'Validating...' : 'Save'}
		</Button>
	{/snippet}
</FormPanel>

<!-- Delete Repo -->
<ConfirmDialog bind:open={deleteRepoOpen} title="Delete Repository" confirmLabel="Delete" onConfirm={confirmDeleteRepo} loading={deletingRepo} icon={Trash2}>
	{#snippet description()}
		This permanently deletes <strong>{namespace}/{name}</strong> and all its tags and images.
	{/snippet}
</ConfirmDialog>


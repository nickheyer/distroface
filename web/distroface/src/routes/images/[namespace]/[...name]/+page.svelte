<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { onMount, tick } from 'svelte';
	import {
		Package,
		ArrowDown,
		ArrowUp,
		ArrowUpDown,
		Eye,
		Lock,
		Pencil,
		Check,
		X,
		Trash2,
		MoreHorizontal,
		EyeOff,
		ChevronRight,
		Tags,
		Clock,
		Star,
		HardDriveDownload,
		RefreshCw,
		Square,
		GitBranch,
		Rocket,
		Loader2
	} from '@lucide/svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
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
		DropdownMenu,
		DropdownMenuContent,
		DropdownMenuItem,
		DropdownMenuSeparator,
		DropdownMenuTrigger
	} from '$lib/components/ui/dropdown-menu';
	import {
		Table,
		TableBody,
		TableCell,
		TableHead,
		TableHeader,
		TableRow
	} from '$lib/components/ui/table';
	import { Sheet, SheetContent, SheetTitle, SheetDescription } from '$lib/components/ui/sheet';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import CopyButton from '$lib/components/copy-button.svelte';
	import MirrorBadge from '$lib/components/mirror-badge.svelte';
	import MirrorConfigFields, {
		emptyMirrorForm,
		mirrorFormFrom,
		mirrorInit
	} from '$lib/components/mirror-config-fields.svelte';
	import DescriptorPanel from '$lib/components/descriptor-panel.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import WebhookManager from '$lib/components/webhook-manager.svelte';
	import WebhookForm from '$lib/components/webhook-form.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import { mirrorSyncStore } from '$lib/stores/mirror-sync.svelte';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import type {
		Repository,
		Tag,
		Descriptor,
		HistoryEntry
	} from '$lib/proto/distroface/v1/types_pb';
	import { RepositoryType, Visibility, WebhookScope } from '$lib/proto/distroface/v1/types_pb';
	import type { Webhook as WebhookType } from '$lib/proto/distroface/v1/types_pb';
	import { resolve } from '$app/paths';

	const namespace = $derived(page.params.namespace);
	const name = $derived(page.params.name);
	const imageHref = $derived(
		resolve('/images/[namespace]/[...name]', { namespace: namespace ?? '', name: name ?? '' })
	);
	const webhookParam = $derived(page.url.searchParams.get('webhook'));

	let editWebhook = $state<WebhookType | null>(null);
	let editWebhookLoadedFor = $state('');

	$effect(() => {
		const id = webhookParam;
		const repoId = repo?.id;
		if (!id || id === 'new' || !repoId) return;
		const key = `${repoId}:${id}`;
		if (editWebhookLoadedFor === key) return;
		editWebhookLoadedFor = '';
		rpcClient.webhook
			.getWebhook({ id }, silentCallOptions)
			.then((resp) => {
				const wh = resp.webhook;
				editWebhook =
					wh && wh.scope === WebhookScope.REPOSITORY && wh.repoId === repoId ? wh : null;
			})
			.catch(() => (editWebhook = null))
			.finally(() => (editWebhookLoadedFor = key));
	});

	function leaveWebhookForm() {
		goto(
			resolve('/images/[namespace]/[...name]', { namespace: namespace ?? '', name: name ?? '' })
		);
	}

	let repo = $state<Repository | undefined>(undefined);
	// Namespace and name of the git repository this package belongs to
	const sourceRepo = $derived.by(() => {
		const [namespace = '', name = ''] = (repo?.sourceRepository ?? '').split('/');
		return { namespace, name };
	});
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
	let mirrorSeededFor = '';
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

	$effect(() => {
		if (repo && repo.id !== mirrorSeededFor) {
			mirrorSeededFor = repo.id;
			mirrorForm = mirrorFormFrom(repo.mirror);
		}
	});

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
			mirrorForm = mirrorFormFrom(repo?.mirror);
		} catch {
			// Error interceptor already toasted
		} finally {
			savingMirror = false;
		}
	}

	const registryHost = $derived(portalStore.host(configStore.publicHostname));

	const namespaceHref = $derived(repo?.isOrgNamespace ? `orgs/${namespace}` : `${namespace}`);
	const isNamespaceMember = $derived(authStore.user?.username === namespace);

	const canUpdateRepo = $derived(
		authStore.user !== null &&
			((isNamespaceMember &&
				authStore.hasPermission('repositories', 'update', `${namespace}/${name}`)) ||
				authStore.hasPermission('repositories', 'manage', `${namespace}/${name}`))
	);

	const canDeleteRepo = $derived(
		authStore.user !== null &&
			((isNamespaceMember &&
				authStore.hasPermission('repositories', 'delete', `${namespace}/${name}`)) ||
				authStore.hasPermission('repositories', 'manage', `${namespace}/${name}`))
	);

	const canManage = $derived(canUpdateRepo || canDeleteRepo);
	const pullCommand = $derived(
		`${registryHost}/${portalStore.imageRef(namespace ?? '', name ?? '')}`
	);
	let pullTag = $state<string | null>(null);
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
				namespace,
				name
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
				namespace,
				name
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
				panelStack = [
					{
						descriptor: resp.descriptor,
						loading: false,
						label: tagName,
						digest: resp.descriptor.digest
					}
				];
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
						if (count === layerIdx) {
							historyEntry = h;
							break;
						}
						count++;
					}
				}
			}
		}

		const label = truncateDigest(child.digest, 12);
		panelStack = [
			...panelStack.slice(0, panelIndex + 1),
			{ descriptor: child, loading: false, label, digest: child.digest, historyEntry }
		];
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
				namespace,
				name,
				description: descriptionDraft
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
		const newVisibility =
			repo.visibility === Visibility.PRIVATE ? Visibility.PUBLIC : Visibility.PRIVATE;
		try {
			const resp = await rpcClient.repository.updateRepository({
				namespace,
				name,
				visibility: newVisibility
			});
			repo = resp.repository;
			toast.success(
				`Repository is now ${newVisibility === Visibility.PRIVATE ? 'private' : 'public'}`
			);
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

<svelte:head>
	<title>{namespace}/{name} - Images - Distroface</title>
</svelte:head>

{#snippet tagSortHeader(label: string, col: string)}
	<button
		type="button"
		class="inline-flex items-center gap-1 transition-colors hover:text-foreground"
		onclick={() => toggleTagSort(col)}
	>
		{label}
		{#if tagSort === `${col} desc`}
			<ArrowDown class="size-3" />
		{:else if tagSort === `${col} asc`}
			<ArrowUp class="size-3" />
		{:else}
			<ArrowUpDown class="size-3 opacity-40" />
		{/if}
	</button>
{/snippet}

<div class="space-y-6">
	{#if loading}
		<div class="space-y-6">
			<Skeleton class="h-4 w-48" />
			<div class="flex items-start gap-3 border-b pb-5">
				<Skeleton class="size-9 rounded-md" />
				<div class="flex-1 space-y-2">
					<Skeleton class="h-6 w-64" />
					<Skeleton class="h-4 w-96" />
					<Skeleton class="h-3.5 w-48" />
				</div>
			</div>
		</div>
	{:else if repo}
		<nav class="flex items-center gap-1.5 text-sm text-muted-foreground" aria-label="Breadcrumb">
			<a href={resolve('/images')} class="transition-colors hover:text-foreground">Images</a>
			<span class="text-muted-foreground/50">/</span>
			<a href={resolve(`/${namespaceHref}`)} class="transition-colors hover:text-foreground"
				>{namespace}</a
			>
			<span class="text-muted-foreground/50">/</span>
			<span class="font-medium text-foreground">{name}</span>
		</nav>

		<div class="flex items-start gap-3 border-b pb-5">
			<div
				class="flex size-9 shrink-0 items-center justify-center rounded-md border bg-muted text-muted-foreground"
			>
				<span class="text-xs font-semibold">{initials}</span>
			</div>

			<div class="min-w-0 flex-1">
				<div class="flex flex-wrap items-center gap-x-2 gap-y-1">
					<h1 class="text-xl leading-7 font-semibold tracking-tight">
						<span class="font-normal text-muted-foreground">{namespace}/</span>{name}
					</h1>
					<Badge variant="outline">
						{#if isPrivate}
							<Lock />Private
						{:else}
							<Eye />Public
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

				{#if repo.sourceRepository}
					<p class="mt-0.5 flex items-center gap-1.5 text-sm text-muted-foreground">
						<GitBranch class="size-3.5" />
						Source repository
						<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
						<a href="/{repo.sourceRepository}" class="link font-medium">{repo.sourceRepository}</a>
					</p>
				{/if}

				{#if editingDescription}
					<div class="mt-1.5 flex items-center gap-2 sm:max-w-lg">
						<Input
							bind:value={descriptionDraft}
							placeholder="Add a description"
							class="flex-1"
							aria-label="Description"
						/>
						<Button
							size="icon"
							onclick={saveDescription}
							disabled={savingDescription}
							aria-label="Save description"
						>
							{#if savingDescription}
								<Loader2 class="animate-spin" />
							{:else}
								<Check />
							{/if}
						</Button>
						<Button
							variant="ghost-muted"
							size="icon"
							onclick={() => (editingDescription = false)}
							aria-label="Cancel"
						>
							<X />
						</Button>
					</div>
				{:else}
					<div class="mt-0.5 flex items-center gap-1">
						<p class="text-sm text-muted-foreground {repo.description ? '' : 'italic'}">
							{repo.description || 'No description'}
						</p>
						<PermissionGate allowed={canUpdateRepo}>
							<Button
								variant="ghost-muted"
								size="icon-xs"
								onclick={startEditDescription}
								aria-label="Edit description"
							>
								<Pencil />
							</Button>
						</PermissionGate>
					</div>
				{/if}

				<div
					class="mt-1.5 flex flex-wrap items-center gap-x-3.5 gap-y-1 text-xs text-muted-foreground"
				>
					{#if repo.tagCount > 0}
						<span class="flex items-center gap-1 tabular-nums">
							<Tags class="size-3.5" />{repo.tagCount} tag{repo.tagCount !== 1 ? 's' : ''}
						</span>
					{/if}
					{#if Number(repo.sizeBytes) > 0}
						<span class="tabular-nums">{formatBytes(Number(repo.sizeBytes))}</span>
					{/if}
					{#if repo.pullCount > 0n}
						<span class="flex items-center gap-1 tabular-nums">
							<ArrowDown class="size-3.5" />{repo.pullCount.toLocaleString()} pull{repo.pullCount !==
							1n
								? 's'
								: ''}
						</span>
					{/if}
					{#if repo.pushCount > 0n}
						<span class="flex items-center gap-1 tabular-nums">
							<ArrowUp class="size-3.5" />{repo.pushCount.toLocaleString()} push{repo.pushCount !==
							1n
								? 'es'
								: ''}
						</span>
					{/if}
					{#if repo.lastPushedAt}
						<span class="flex items-center gap-1">
							<Clock class="size-3.5" />Updated {relativeTime(timestampDate(repo.lastPushedAt))}
						</span>
					{/if}
				</div>

				{#if pullTag !== null}
					<div class="mt-3 flex items-center gap-2 sm:max-w-xl">
						<code class="code-inline min-w-0 flex-1 truncate"
							>docker pull {pullCommand}:{pullTag}</code
						>
						<CopyButton text="docker pull {pullCommand}:{pullTag}" label="Pull command copied" />
					</div>
				{/if}
			</div>

			<div class="flex shrink-0 items-center gap-2">
				{#if authStore.isAuthenticated}
					<Button variant="outline" onclick={toggleStar} disabled={starPending}>
						<Star class={repo.isStarred ? 'fill-warning text-warning' : ''} />
						{repo.isStarred ? 'Starred' : 'Star'}
						{#if repo.starCount > 0n}
							<span class="text-muted-foreground tabular-nums"
								>{repo.starCount.toLocaleString()}</span
							>
						{/if}
					</Button>
				{:else if repo.starCount > 0n}
					<span class="flex items-center gap-1 text-xs text-muted-foreground tabular-nums">
						<Star class="size-3.5" />{repo.starCount.toLocaleString()}
					</span>
				{/if}
				<PermissionGate allowed={canManage}>
					<DropdownMenu>
						<DropdownMenuTrigger>
							{#snippet child({ props })}
								<Button {...props} variant="outline" size="icon" aria-label="More actions">
									<MoreHorizontal />
								</Button>
							{/snippet}
						</DropdownMenuTrigger>
						<DropdownMenuContent align="end">
							<PermissionGate allowed={canUpdateRepo}>
								<DropdownMenuItem onclick={toggleVisibility}>
									{#if isPrivate}
										<Eye />Make public
									{:else}
										<EyeOff />Make private
									{/if}
								</DropdownMenuItem>
							</PermissionGate>
							<PermissionGate allowed={canDeleteRepo}>
								<DropdownMenuSeparator />
								<DropdownMenuItem variant="destructive" onclick={() => (deleteRepoOpen = true)}>
									<Trash2 />Delete repository
								</DropdownMenuItem>
							</PermissionGate>
						</DropdownMenuContent>
					</DropdownMenu>
				</PermissionGate>
			</div>
		</div>

		<div class="space-y-4">
			<div class="section-header">
				<h2 class="section-title">Tags</h2>
				{#if tagsPager.totalCount > 0}
					<span class="text-xs text-muted-foreground tabular-nums">
						{tagsPager.totalCount} tag{tagsPager.totalCount !== 1 ? 's' : ''}
					</span>
				{/if}
			</div>

			{#if tagsLoading}
				<div class="box divide-y">
					{#each { length: 3 }, i (i)}
						<div class="flex items-center gap-3 px-4 py-3">
							<Skeleton class="h-5 w-24" />
							<Skeleton class="h-3.5 w-40" />
							<Skeleton class="ml-auto h-3.5 w-12" />
						</div>
					{/each}
				</div>
			{:else if tags.length === 0}
				<EmptyState
					icon={Package}
					message="No tags yet"
					description="Push an image to create your first tag."
				>
					{#snippet actions()}
						<code class="code-inline">docker push {pullCommand}:latest</code>
					{/snippet}
				</EmptyState>
			{:else}
				<div class="data-table">
					<Table class="table-fixed">
						<TableHeader>
							<TableRow>
								<TableHead>{@render tagSortHeader('Tag', 'version')}</TableHead>
								<TableHead class="hidden w-56 sm:table-cell">Digest</TableHead>
								<TableHead class="hidden w-40 md:table-cell">Platform</TableHead>
								<TableHead class="w-24 text-right">
									<div class="flex justify-end">{@render tagSortHeader('Size', 'size')}</div>
								</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{#each tags as tag (tag.name)}
								<TableRow class="cursor-pointer" onclick={() => openTagDetail(tag.name)}>
									<TableCell>
										<div class="flex min-w-0 items-center gap-1">
											<Badge variant="secondary" class="min-w-0 shrink font-mono">
												<span class="truncate">{tag.name}</span>
											</Badge>
											{#if tag.hasRelease && repo.sourceRepository}
												<Badge
													href={resolve('/[namespace]/[name]/releases/[tag]', {
														...sourceRepo,
														tag: tag.name
													})}
													class="shrink-0"
													onclick={(e: MouseEvent) => e.stopPropagation()}
												>
													<Rocket />Release
												</Badge>
											{/if}
											<CopyButton
												text="docker pull {pullCommand}:{tag.name}"
												label="Pull command copied"
											/>
										</div>
									</TableCell>
									<TableCell class="hidden sm:table-cell">
										<span class="block truncate font-mono text-xs text-muted-foreground">
											{truncateDigest(tag.digest)}
										</span>
									</TableCell>
									<TableCell class="hidden md:table-cell">
										{#if tag.platforms.length > 0}
											<div class="flex flex-wrap gap-1">
												{#each tag.platforms as p, pi (pi)}
													<Badge variant="outline" class="font-mono">
														{p.os ?? ''}{#if p.os && p.architecture}/{/if}{p.architecture ??
															''}{#if p.variant}/{p.variant}{/if}
													</Badge>
												{/each}
											</div>
										{/if}
									</TableCell>
									<TableCell class="text-right text-muted-foreground tabular-nums">
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

		{#if repoIsMirror}
			<PermissionGate allowed={canUpdateRepo}>
				<div class="box">
					<div class="box-header">
						<span class="flex items-center gap-2">
							<HardDriveDownload class="size-4 text-muted-foreground" />
							Mirror
						</span>
						{#if mirrorSyncActive}
							<Button
								variant="outline"
								size="sm"
								onclick={stopMirrorSync}
								disabled={stoppingMirror}
							>
								<Square />Stop sync
							</Button>
						{:else}
							<Button variant="outline" size="sm" onclick={syncMirrorNow} disabled={syncingMirror}>
								<RefreshCw class={syncingMirror ? 'animate-spin' : ''} />Sync now
							</Button>
						{/if}
					</div>
					<div class="p-4">
						<MirrorConfigFields
							form={mirrorForm}
							kind="oci"
							tokenSet={repo.mirror?.authTokenSet ?? false}
							lastSync={repo.mirrorLastSync}
							lastError={repo.mirrorLastError}
							nextAttempt={repo.mirrorNextAttempt}
							idPrefix="image-mirror"
						/>
					</div>
					<div class="flex items-center justify-end gap-2 border-t px-4 py-3">
						<Button
							onclick={saveMirrorSettings}
							disabled={savingMirror || !mirrorForm.upstream.trim()}
						>
							{#if savingMirror}<Loader2 class="animate-spin" />{/if}
							{savingMirror ? 'Validating' : 'Save changes'}
						</Button>
					</div>
				</div>
			</PermissionGate>
		{/if}

		{#if repo && canUpdateRepo}
			<PermissionGate allowed={canUpdateRepo}>
				{#if webhookParam === 'new'}
					<FormPage
						level="section"
						title="New webhook"
						description="Receive HTTP POST notifications for repository events."
						backHref={imageHref}
						backLabel="Webhooks"
					>
						<WebhookForm
							scope={WebhookScope.REPOSITORY}
							scopeId={repo.id}
							onSaved={leaveWebhookForm}
							cancelHref={imageHref}
						/>
					</FormPage>
				{:else if webhookParam}
					{#if editWebhookLoadedFor !== `${repo.id}:${webhookParam}`}
						<Skeleton class="h-48 w-full rounded-lg" />
					{:else if editWebhook}
						<FormPage
							level="section"
							title="Edit webhook"
							description="Change where and when this repository posts events."
							backHref={imageHref}
							backLabel="Webhooks"
						>
							<WebhookForm
								scope={WebhookScope.REPOSITORY}
								scopeId={repo.id}
								webhook={editWebhook}
								onSaved={leaveWebhookForm}
								cancelHref={imageHref}
							/>
						</FormPage>
					{:else}
						<EmptyState
							icon={Package}
							message="Webhook not found"
							description="It may have been deleted or belongs to another repository."
						>
							{#snippet actions()}
								<Button variant="outline" href={imageHref}>Back to webhooks</Button>
							{/snippet}
						</EmptyState>
					{/if}
				{:else}
					<WebhookManager
						scope={WebhookScope.REPOSITORY}
						scopeId={repo.id}
						newHref="{imageHref}?webhook=new"
						editHref={(id) => `${imageHref}?webhook=${id}`}
						createDescription="Receive HTTP POST notifications for repository events."
					/>
				{/if}
			</PermissionGate>
		{/if}
	{:else}
		<EmptyState
			icon={Package}
			message="Repository not found"
			description="{namespace}/{name} does not exist or you don't have access."
		>
			{#snippet actions()}
				<Button variant="outline" href={resolve('/images')}>Back to images</Button>
			{/snippet}
		</EmptyState>
	{/if}
</div>

<Sheet bind:open={sheetOpen}>
	<SheetContent
		side="right"
		class="flex w-full flex-col overflow-hidden p-0"
		style="max-width: min({panelStack.length *
			32}rem, 85vw); transition: max-width 200ms ease-in-out;"
	>
		<div class="shrink-0 space-y-3 border-b px-6 py-4 pr-12">
			<div>
				<SheetTitle class="flex flex-wrap items-baseline gap-1.5">
					<span>
						<span class="font-normal text-muted-foreground">{namespace}/{name}:</span><button
							type="button"
							class="transition-colors {panelStack.length > 1
								? 'cursor-pointer hover:text-muted-foreground'
								: 'cursor-default'}"
							onclick={() => {
								if (panelStack.length > 1) panelStack = panelStack.slice(0, 1);
							}}>{panelStack[0]?.label ?? selectedTagName}</button
						>
					</span>
					{#each panelStack as panel, i (panel.digest + i)}
						{#if i > 0}
							<ChevronRight class="size-3 shrink-0 self-center text-muted-foreground/50" />
							<button
								type="button"
								class="font-mono text-sm font-normal transition-colors {i === panelStack.length - 1
									? 'cursor-default text-foreground'
									: 'cursor-pointer text-muted-foreground hover:text-foreground'}"
								onclick={() => {
									if (i < panelStack.length - 1) panelStack = panelStack.slice(0, i + 1);
								}}>{panel.label}</button
							>
						{/if}
					{/each}
				</SheetTitle>
				<SheetDescription class="mt-0.5">Image manifest and layer details</SheetDescription>
			</div>

			<div class="flex items-center gap-2">
				<code class="code-inline min-w-0 flex-1 truncate"
					>docker pull {pullCommand}:{selectedTagName}</code
				>
				<CopyButton
					text="docker pull {pullCommand}:{selectedTagName}"
					label="Pull command copied"
				/>
			</div>
		</div>

		<div bind:this={panelScroll} class="flex min-h-0 flex-1 overflow-x-auto overflow-y-hidden">
			{#each panelStack as panel, i (panel.digest + i)}
				<div
					class="{panelStack.length === 1
						? 'flex-1'
						: 'w-lg shrink-0'} overflow-y-auto border-r last:border-r-0"
				>
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

<ConfirmDialog
	bind:open={deleteRepoOpen}
	title="Delete repository"
	confirmLabel="Delete"
	onConfirm={confirmDeleteRepo}
	loading={deletingRepo}
	icon={Trash2}
>
	{#snippet description()}
		This permanently deletes <strong>{namespace}/{name}</strong> and all its tags and images.
	{/snippet}
</ConfirmDialog>

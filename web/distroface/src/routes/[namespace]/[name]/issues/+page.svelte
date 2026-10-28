<script lang="ts">
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { SvelteSet } from 'svelte/reactivity';
	import {
		CircleDot,
		CheckCircle,
		Plus,
		Tag,
		Milestone as MilestoneIcon,
		MessageSquare,
		Pin,
		ChevronDown,
		Check,
		Search,
		Loader,
		Calendar,
		Users,
		Blocks,
		Lock
	} from '$lib/icons';
	import { Button } from '$lib/components/ui/button';
	import { Checkbox } from '$lib/components/ui/checkbox';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Avatar, AvatarFallback } from '$lib/components/ui/avatar';
	import { Popover, PopoverContent, PopoverTrigger } from '$lib/components/ui/popover';
	import {
		DropdownMenu,
		DropdownMenuCheckboxItem,
		DropdownMenuContent,
		DropdownMenuItem,
		DropdownMenuSeparator,
		DropdownMenuTrigger
	} from '$lib/components/ui/dropdown-menu';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import QueryFilterBar from '$lib/components/query-filter.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import BulkActionBar from '$lib/components/bulk-action-bar.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import LabelChip from '$lib/components/git/label-chip.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { authStore } from '$lib/stores/auth.svelte';
	import { QueryFilter } from '$lib/query.svelte';
	import { Pager } from '$lib/pager.svelte';
	import { GitUnit } from '$lib/proto/distroface/v1/git_pb';
	import {
		IssueState,
		type Issue,
		type Label,
		type Milestone
	} from '$lib/proto/distroface/v1/issue_pb';
	import { relativeTime, cn } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { toast } from 'svelte-sonner';

	const repo = $derived(gitRepoStore.repo!);
	const namespace = $derived(repo.namespace);
	const name = $derived(repo.name);
	const base = $derived(`/${namespace}/${name}`);
	const canWrite = $derived(gitRepoStore.canWrite(GitUnit.ISSUES) && !repo.isArchived);
	const canCreate = $derived(
		authStore.isAuthenticated && gitRepoStore.canRead(GitUnit.ISSUES) && !repo.isArchived
	);

	const params = page.url.searchParams;
	let stateTab = $state<'open' | 'closed'>(params.get('state') === 'closed' ? 'closed' : 'open');
	const filter = new QueryFilter([
		{ key: 'title', label: 'Title' },
		{ key: 'body', label: 'Body' }
	]);
	const pager = new Pager(25);
	let labelFilter = $state<string[]>(
		params.get('labels') ? params.get('labels')!.split(',').filter(Boolean) : []
	);
	let milestoneFilter = $state(params.get('milestone') ?? '');
	let assigneeFilter = $state(params.get('assignee') ?? '');
	let authorFilter = $state(params.get('author') ?? '');
	let mentionedFilter = $state(params.get('mentioned') ?? '');
	let sort = $state('created_at desc');

	const sorts = [
		{ value: 'created_at desc', label: 'Newest' },
		{ value: 'created_at asc', label: 'Oldest' },
		{ value: 'updated_at desc', label: 'Recently updated' },
		{ value: 'comments desc', label: 'Most commented' },
		{ value: 'due_date asc', label: 'Due soon' }
	];

	let issues = $state<Issue[]>([]);
	let openCount = $state(0);
	let closedCount = $state(0);
	let loading = $state(true);
	let labels = $state<Label[]>([]);
	let milestones = $state<Milestone[]>([]);
	const selected = new SvelteSet<bigint>();

	const filtered = $derived(
		filter.active ||
			labelFilter.length > 0 ||
			!!milestoneFilter ||
			!!assigneeFilter ||
			!!authorFilter ||
			!!mentionedFilter
	);
	const pinned = $derived(
		stateTab === 'open' && !filtered && pager.page === 1 ? issues.filter((i) => i.pinned) : []
	);
	const rows = $derived(pinned.length ? issues.filter((i) => !i.pinned) : issues);

	async function load() {
		loading = true;
		try {
			const resp = await rpcClient.issue.listIssues(
				{
					namespace,
					name,
					state: stateTab === 'closed' ? IssueState.CLOSED : IssueState.OPEN,
					labels: labelFilter,
					milestone: milestoneFilter,
					assignee: assigneeFilter,
					author: authorFilter,
					mentioned: mentionedFilter,
					isPull: false,
					page: pager.request(filter.request(), sort)
				},
				silentCallOptions
			);
			issues = resp.issues;
			openCount = Number(resp.openCount);
			closedCount = Number(resp.closedCount);
			pager.apply(resp.page);
		} catch {
			issues = [];
			pager.apply();
		} finally {
			loading = false;
		}
	}

	function refresh() {
		pager.reset();
		selected.clear();
		load();
	}

	async function loadFilters() {
		try {
			const [l, m] = await Promise.all([
				rpcClient.issue.listLabels(
					{ namespace, name, page: { pageSize: 500, pageToken: '', orderBy: '' } },
					silentCallOptions
				),
				rpcClient.issue.listMilestones(
					{
						namespace,
						name,
						state: IssueState.OPEN,
						page: { pageSize: 100, pageToken: '', orderBy: '' }
					},
					silentCallOptions
				)
			]);
			labels = l.labels;
			milestones = m.milestones;
		} catch {
			// Filters stay empty
		}
	}

	onMount(() => {
		gitRepoStore.load(namespace, name);
		load();
		loadFilters();
	});

	function setTab(tab: 'open' | 'closed') {
		if (stateTab === tab) return;
		stateTab = tab;
		refresh();
	}

	function toggleLabelFilter(n: string) {
		labelFilter = labelFilter.includes(n)
			? labelFilter.filter((x) => x !== n)
			: [...labelFilter, n];
		refresh();
	}

	function clearFilters() {
		filter.reset();
		labelFilter = [];
		milestoneFilter = '';
		assigneeFilter = '';
		authorFilter = '';
		mentionedFilter = '';
		refresh();
	}

	// User search shared by the people filters
	type UserOpt = { id: string; username: string };
	let userQuery = $state('');
	let userResults = $state<UserOpt[]>([]);
	let userLoading = $state(false);
	let userDebounce: ReturnType<typeof setTimeout> | undefined;

	async function searchUsers() {
		userLoading = true;
		try {
			const resp = await rpcClient.user.searchUsers(
				{
					page: {
						pageSize: 15,
						pageToken: '',
						orderBy: '',
						query: { text: userQuery.trim(), filters: [] }
					}
				},
				silentCallOptions
			);
			userResults = resp.users.map((u) => ({ id: u.id, username: u.username }));
		} catch {
			userResults = [];
		} finally {
			userLoading = false;
		}
	}

	function onUserQuery() {
		clearTimeout(userDebounce);
		userDebounce = setTimeout(searchUsers, 200);
	}

	// Selection and bulk edits
	const allSelected = $derived(rows.length > 0 && rows.every((i) => selected.has(i.number)));
	function toggleAll() {
		if (allSelected) rows.forEach((i) => selected.delete(i.number));
		else rows.forEach((i) => selected.add(i.number));
	}

	let bulkBusy = $state(false);
	async function bulk(fields: Record<string, unknown>) {
		if (bulkBusy || selected.size === 0) return;
		bulkBusy = true;
		try {
			const resp = await rpcClient.issue.bulkUpdateIssues({
				namespace,
				name,
				numbers: [...selected],
				...fields
			});
			toast.success(`${resp.updatedCount} issue${resp.updatedCount === 1 ? '' : 's'} updated`);
			selected.clear();
			await load();
		} catch {
			// Error interceptor already toasted
		} finally {
			bulkBusy = false;
		}
	}

	// Label is on every selected issue
	function bulkLabelState(l: Label): boolean {
		const picked = issues.filter((i) => selected.has(i.number));
		return picked.length > 0 && picked.every((i) => i.labels.some((x) => x.id === l.id));
	}

	function dueClass(i: Issue): string {
		if (!i.dueDate) return '';
		return timestampDate(i.dueDate).getTime() < Date.now() && i.state === IssueState.OPEN
			? 'text-destructive'
			: '';
	}
</script>

{#snippet userFilter(label: string, value: string, set: (v: string) => void, allowNone: boolean)}
	<Popover
		onOpenChange={(o) => {
			if (o) {
				userQuery = '';
				searchUsers();
			}
		}}
	>
		<PopoverTrigger>
			{#snippet child({ props })}
				<Button
					{...props}
					variant="ghost"
					size="sm"
					class={cn('font-normal', value && 'text-primary')}
				>
					{label}{#if value}: {value}{/if}<ChevronDown class="text-muted-foreground" />
				</Button>
			{/snippet}
		</PopoverTrigger>
		<PopoverContent class="w-64 p-0" align="start">
			<div class="flex items-center gap-2 border-b px-2.5 py-2">
				<Search class="size-3.5 shrink-0 text-muted-foreground" />
				<input
					class="flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
					placeholder="Search users"
					bind:value={userQuery}
					oninput={onUserQuery}
					onkeydown={(e) => {
						if (e.key === 'Enter' && userQuery.trim()) set(userQuery.trim());
					}}
				/>
				{#if userLoading}<Loader class="size-3.5 animate-spin text-muted-foreground" />{/if}
			</div>
			<div class="max-h-64 overflow-y-auto py-1">
				{#if value}
					<button
						type="button"
						class="w-full px-2.5 py-1.5 text-left text-sm text-muted-foreground hover:bg-accent"
						onclick={() => set('')}>Clear</button
					>
				{/if}
				{#if allowNone}
					<button
						type="button"
						class={cn(
							'w-full px-2.5 py-1.5 text-left text-sm hover:bg-accent',
							value === 'none' && 'bg-accent/50'
						)}
						onclick={() => set('none')}>Assigned to nobody</button
					>
				{/if}
				{#each userResults as u (u.id)}
					<button
						type="button"
						class={cn(
							'flex w-full items-center gap-2 px-2.5 py-1.5 text-left text-sm hover:bg-accent',
							value === u.username && 'bg-accent/50'
						)}
						onclick={() => set(u.username)}
					>
						<span class="inline-flex size-4 items-center justify-center"
							>{#if value === u.username}<Check class="size-3.5 text-primary" />{/if}</span
						>
						{u.username}
					</button>
				{/each}
			</div>
		</PopoverContent>
	</Popover>
{/snippet}

{#snippet issueRow(i: Issue)}
	<div class="flex items-start gap-3 px-4 py-3 transition-colors hover:bg-muted/30">
		{#if canWrite}
			<Checkbox
				class="mt-1"
				checked={selected.has(i.number)}
				onCheckedChange={(v) => {
					if (v) selected.add(i.number);
					else selected.delete(i.number);
				}}
				aria-label="Select issue {i.number}"
			/>
		{/if}
		{#if i.state === IssueState.CLOSED}
			<CheckCircle class="mt-0.5 size-4 shrink-0 text-done" />
		{:else}
			<CircleDot class="mt-0.5 size-4 shrink-0 text-success" />
		{/if}
		<div class="min-w-0 flex-1">
			<div class="flex flex-wrap items-center gap-x-2 gap-y-1">
				<a
					href={resolve('/[namespace]/[name]/issues/[number]', {
						namespace,
						name,
						number: String(i.number)
					})}
					class="min-w-0 text-sm leading-snug font-semibold wrap-break-word hover:text-primary"
					>{i.title}</a
				>
				{#each i.labels as l (l.id)}
					<button
						type="button"
						class="inline-flex"
						onclick={() => toggleLabelFilter(l.name)}
						title="Filter by {l.name}"
					>
						<LabelChip name={l.name} color={l.color} description={l.description} size="sm" />
					</button>
				{/each}
				{#if i.locked}<Lock class="size-3 text-muted-foreground" aria-label="Locked" />{/if}
				{#if i.blocked}<span class="inline-flex items-center gap-0.5 text-2xs text-warning"
						><Blocks class="size-3" />blocked</span
					>{/if}
			</div>
			<div
				class="mt-0.5 flex flex-wrap items-center gap-x-1.5 gap-y-0.5 text-xs text-muted-foreground"
			>
				<span class="tabular-nums">#{i.number}</span>
				{#if i.state === IssueState.CLOSED && i.closedAt}
					<span>closed {relativeTime(timestampDate(i.closedAt))}</span>
				{:else if i.createdAt}
					<span>opened {relativeTime(timestampDate(i.createdAt))}</span>
				{/if}
				{#if i.author}
					<span
						>by <a
							href={resolve('/[namespace]', { namespace: i.author.username })}
							class="hover:text-primary">{i.author.username}</a
						></span
					>
				{/if}
				{#if i.milestone}
					<a
						href="{resolve('/[namespace]/[name]/issues', {
							namespace,
							name
						})}?milestone={encodeURIComponent(i.milestone.title)}"
						class="inline-flex items-center gap-0.5 hover:text-primary"
						><MilestoneIcon class="size-3" />{i.milestone.title}</a
					>
				{/if}
				{#if i.dueDate}
					<span class="inline-flex items-center gap-0.5 {dueClass(i)}"
						><Calendar class="size-3" />{timestampDate(i.dueDate).toLocaleDateString()}</span
					>
				{/if}
				{#if i.assignees.length}
					<span class="inline-flex items-center gap-1 sm:hidden"
						><Users class="size-3" />{i.assignees.map((a) => a.username).join(', ')}</span
					>
				{/if}
				{#if i.commentCount > 0}
					<a
						href={resolve('/[namespace]/[name]/issues/[number]', {
							namespace,
							name,
							number: String(i.number)
						})}
						class="inline-flex items-center gap-1 tabular-nums hover:text-primary sm:hidden"
						><MessageSquare class="size-3" />{i.commentCount}</a
					>
				{/if}
			</div>
		</div>
		<div class="hidden shrink-0 items-center gap-3 pt-0.5 text-xs text-muted-foreground sm:flex">
			{#if i.assignees.length}
				<div class="flex -space-x-1.5" title={i.assignees.map((a) => a.username).join(', ')}>
					{#each i.assignees.slice(0, 3) as a (a.id)}
						<Avatar class="size-5 border border-background">
							<AvatarFallback class="text-2xs">{a.username.charAt(0)}</AvatarFallback>
						</Avatar>
					{/each}
					{#if i.assignees.length > 3}
						<Avatar class="size-5 border border-background">
							<AvatarFallback class="text-2xs">+{i.assignees.length - 3}</AvatarFallback>
						</Avatar>
					{/if}
				</div>
			{/if}
			{#if i.commentCount > 0}
				<a
					href={resolve('/[namespace]/[name]/issues/[number]', {
						namespace,
						name,
						number: String(i.number)
					})}
					class="inline-flex items-center gap-1 tabular-nums hover:text-primary"
					><MessageSquare class="size-3.5" />{i.commentCount}</a
				>
			{/if}
		</div>
	</div>
{/snippet}

<div class="space-y-4">
	<div class="flex flex-wrap items-center gap-2">
		<div class="w-full sm:w-auto sm:min-w-64 sm:flex-1">
			<QueryFilterBar {filter} placeholder="Search issues" onchange={refresh} />
		</div>
		<Button variant="outline" href="{base}/labels"><Tag />Labels</Button>
		<Button variant="outline" href="{base}/milestones"><MilestoneIcon />Milestones</Button>
		<PermissionGate allowed={canCreate}>
			<Button href="{base}/issues/new"><Plus />New issue</Button>
		</PermissionGate>
	</div>

	{#if pinned.length}
		<div class="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
			{#each pinned as i (i.id)}
				<a
					href={resolve('/[namespace]/[name]/issues/[number]', {
						namespace,
						name,
						number: String(i.number)
					})}
					class="card-interactive flex flex-col gap-1.5 p-3"
				>
					<span class="flex items-center gap-1.5 text-xs text-muted-foreground"
						><Pin class="size-3" />#{i.number} opened {i.createdAt
							? relativeTime(timestampDate(i.createdAt))
							: ''} by {i.author?.username ?? ''}</span
					>
					<span class="line-clamp-2 text-sm leading-snug font-semibold">{i.title}</span>
					{#if i.labels.length}
						<span class="flex flex-wrap gap-1">
							{#each i.labels.slice(0, 4) as l (l.id)}<LabelChip
									name={l.name}
									color={l.color}
									size="sm"
								/>{/each}
						</span>
					{/if}
				</a>
			{/each}
		</div>
	{/if}

	<div class="box transition-opacity {loading ? 'opacity-60' : ''}">
		<div class="box-header flex-wrap">
			<div class="flex flex-wrap items-center gap-1">
				{#if canWrite && rows.length}
					<Checkbox
						checked={allSelected}
						onCheckedChange={toggleAll}
						aria-label="Select all"
						class="mr-2"
					/>
				{/if}
				<button
					type="button"
					class={cn(
						'inline-flex items-center gap-1.5 rounded-md px-2 py-1 text-sm',
						stateTab === 'open'
							? 'font-semibold text-foreground'
							: 'font-normal text-muted-foreground hover:text-foreground'
					)}
					onclick={() => setTab('open')}
				>
					<CircleDot class="size-3.5" /><span class="tabular-nums">{openCount}</span> Open
				</button>
				<button
					type="button"
					class={cn(
						'inline-flex items-center gap-1.5 rounded-md px-2 py-1 text-sm',
						stateTab === 'closed'
							? 'font-semibold text-foreground'
							: 'font-normal text-muted-foreground hover:text-foreground'
					)}
					onclick={() => setTab('closed')}
				>
					<CheckCircle class="size-3.5" /><span class="tabular-nums">{closedCount}</span> Closed
				</button>
			</div>
			<div class="flex flex-wrap items-center gap-0.5">
				<DropdownMenu>
					<DropdownMenuTrigger>
						{#snippet child({ props })}
							<Button
								{...props}
								variant="ghost"
								size="sm"
								class={cn('font-normal', labelFilter.length && 'text-primary')}
								>Labels{#if labelFilter.length}
									({labelFilter.length}){/if}<ChevronDown class="text-muted-foreground" /></Button
							>
						{/snippet}
					</DropdownMenuTrigger>
					<DropdownMenuContent align="end" class="max-h-80 w-60 overflow-y-auto">
						{#if labels.length === 0}
							<DropdownMenuItem disabled>No labels</DropdownMenuItem>
						{/if}
						{#each labels as l (l.id)}
							<DropdownMenuCheckboxItem
								checked={labelFilter.includes(l.name)}
								onCheckedChange={() => toggleLabelFilter(l.name)}
								closeOnSelect={false}
							>
								<LabelChip name={l.name} color={l.color} size="sm" />
							</DropdownMenuCheckboxItem>
						{/each}
					</DropdownMenuContent>
				</DropdownMenu>
				<DropdownMenu>
					<DropdownMenuTrigger>
						{#snippet child({ props })}
							<Button
								{...props}
								variant="ghost"
								size="sm"
								class={cn('font-normal', milestoneFilter && 'text-primary')}
								>Milestones{#if milestoneFilter}: {milestoneFilter}{/if}<ChevronDown
									class="text-muted-foreground"
								/></Button
							>
						{/snippet}
					</DropdownMenuTrigger>
					<DropdownMenuContent align="end" class="max-h-80 w-60 overflow-y-auto">
						{#if milestoneFilter}
							<DropdownMenuItem
								onclick={() => {
									milestoneFilter = '';
									refresh();
								}}>Clear</DropdownMenuItem
							>
							<DropdownMenuSeparator />
						{/if}
						<DropdownMenuItem
							onclick={() => {
								milestoneFilter = 'none';
								refresh();
							}}>No milestone</DropdownMenuItem
						>
						{#each milestones as m (m.id)}
							<DropdownMenuItem
								onclick={() => {
									milestoneFilter = m.title;
									refresh();
								}}
							>
								<span class="inline-flex size-4 items-center justify-center"
									>{#if milestoneFilter === m.title}<Check
											class="size-3.5 text-primary"
										/>{/if}</span
								>{m.title}
							</DropdownMenuItem>
						{/each}
					</DropdownMenuContent>
				</DropdownMenu>
				{@render userFilter(
					'Assignee',
					assigneeFilter,
					(v) => {
						assigneeFilter = v;
						refresh();
					},
					true
				)}
				{@render userFilter(
					'Author',
					authorFilter,
					(v) => {
						authorFilter = v;
						refresh();
					},
					false
				)}
				{@render userFilter(
					'Mentioned',
					mentionedFilter,
					(v) => {
						mentionedFilter = v;
						refresh();
					},
					false
				)}
				<Select
					type="single"
					value={sort}
					onValueChange={(v) => {
						if (v) {
							sort = v;
							refresh();
						}
					}}
				>
					<SelectTrigger
						class="w-40 border-0 bg-transparent font-normal shadow-none"
						size="sm"
						aria-label="Sort">{sorts.find((s) => s.value === sort)?.label}</SelectTrigger
					>
					<SelectContent>
						{#each sorts as s (s.value)}
							<SelectItem value={s.value}>{s.label}</SelectItem>
						{/each}
					</SelectContent>
				</Select>
			</div>
		</div>

		{#if loading && issues.length === 0}
			<div class="divide-y">
				{#each { length: 5 }, i (i)}
					<div class="flex items-start gap-3 px-4 py-3">
						<Skeleton class="mt-0.5 size-4 rounded-full" />
						<div class="flex-1 space-y-2">
							<Skeleton class="h-4 w-2/3" />
							<Skeleton class="h-3 w-1/3" />
						</div>
					</div>
				{/each}
			</div>
		{:else if rows.length === 0}
			<div class="box-empty">
				<p class="font-semibold text-foreground">
					{filtered
						? 'No issues match these filters'
						: stateTab === 'open'
							? 'No open issues'
							: 'No closed issues'}
				</p>
				{#if filtered}
					<p class="mt-1">Try clearing a filter.</p>
				{:else if canCreate}
					<p class="mt-1">Open the first issue to start tracking work.</p>
				{/if}
				{#if filtered || canCreate}
					<div class="mt-4 flex justify-center gap-2">
						{#if filtered}
							<Button variant="outline" onclick={clearFilters}>Clear filters</Button>
						{:else}
							<Button href="{base}/issues/new"><Plus />New issue</Button>
						{/if}
					</div>
				{/if}
			</div>
		{:else}
			<div class="divide-y">
				{#each rows as i (i.id)}
					{@render issueRow(i)}
				{/each}
			</div>
			<DataPagination attached {pager} onChange={load} />
		{/if}
	</div>
</div>

<BulkActionBar count={selected.size} onClear={() => selected.clear()}>
	<DropdownMenu>
		<DropdownMenuTrigger>
			{#snippet child({ props })}
				<Button {...props} variant="outline" disabled={bulkBusy}><Tag />Labels</Button>
			{/snippet}
		</DropdownMenuTrigger>
		<DropdownMenuContent align="center" class="max-h-80 w-60 overflow-y-auto">
			{#each labels as l (l.id)}
				{@const on = bulkLabelState(l)}
				<DropdownMenuCheckboxItem
					checked={on}
					onCheckedChange={() => bulk(on ? { removeLabelIds: [l.id] } : { addLabelIds: [l.id] })}
				>
					<LabelChip name={l.name} color={l.color} size="sm" />
				</DropdownMenuCheckboxItem>
			{/each}
		</DropdownMenuContent>
	</DropdownMenu>
	<DropdownMenu>
		<DropdownMenuTrigger>
			{#snippet child({ props })}
				<Button {...props} variant="outline" disabled={bulkBusy}><MilestoneIcon />Milestone</Button>
			{/snippet}
		</DropdownMenuTrigger>
		<DropdownMenuContent align="center" class="w-56">
			<DropdownMenuItem onclick={() => bulk({ milestoneId: '' })}>No milestone</DropdownMenuItem>
			{#each milestones as m (m.id)}
				<DropdownMenuItem onclick={() => bulk({ milestoneId: m.id })}>{m.title}</DropdownMenuItem>
			{/each}
		</DropdownMenuContent>
	</DropdownMenu>
	<Popover
		onOpenChange={(o) => {
			if (o) {
				userQuery = '';
				searchUsers();
			}
		}}
	>
		<PopoverTrigger>
			{#snippet child({ props })}
				<Button {...props} variant="outline" disabled={bulkBusy}><Users />Assign</Button>
			{/snippet}
		</PopoverTrigger>
		<PopoverContent class="w-[min(16rem,calc(100vw-2rem))] p-0" align="center" side="top">
			<div class="flex items-center gap-2 border-b px-2.5 py-2">
				<Search class="size-3.5 shrink-0 text-muted-foreground" />
				<input
					class="flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
					placeholder="Search users"
					bind:value={userQuery}
					oninput={onUserQuery}
				/>
			</div>
			<div class="max-h-64 overflow-y-auto py-1">
				{#each userResults as u (u.id)}
					<div class="flex items-center gap-1 px-2 py-1 text-sm">
						<span class="flex-1 truncate">{u.username}</span>
						<Button variant="ghost" size="xs" onclick={() => bulk({ addAssigneeIds: [u.id] })}
							>Add</Button
						>
						<Button variant="ghost" size="xs" onclick={() => bulk({ removeAssigneeIds: [u.id] })}
							>Remove</Button
						>
					</div>
				{/each}
			</div>
		</PopoverContent>
	</Popover>
	{#if stateTab === 'open'}
		<Button variant="outline" disabled={bulkBusy} onclick={() => bulk({ state: IssueState.CLOSED })}
			><CheckCircle />Close</Button
		>
	{:else}
		<Button variant="outline" disabled={bulkBusy} onclick={() => bulk({ state: IssueState.OPEN })}
			><CircleDot />Reopen</Button
		>
	{/if}
</BulkActionBar>

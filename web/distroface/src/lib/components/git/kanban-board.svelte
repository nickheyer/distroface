<script lang="ts">
	import { resolve } from '$app/paths';
	import {
		Plus, MoreHorizontal, Pencil, Trash2, ArrowLeft, ArrowRight, Star, X, CircleDot, CircleCheck, GitPullRequest, Search, Loader2
	} from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from '$lib/components/ui/dropdown-menu';
	import FormPanel from '$lib/components/form-panel.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import type { Project, ProjectCard, ProjectColumn } from '$lib/proto/distroface/v1/project_pb';
	import { IssueState, type Issue } from '$lib/proto/distroface/v1/issue_pb';
	import { normalizeHex } from '$lib/components/git/label-chip.svelte';
	import { cn } from '$lib/utils';
	import { toast } from 'svelte-sonner';

	let {
		project,
		canEdit,
		onChanged,
		searchNamespace = ''
	}: {
		project: Project;
		canEdit: boolean;
		onChanged: () => void;
		// Org boards search issues across this namespace
		searchNamespace?: string;
	} = $props();

	function cardParams(card: ProjectCard): { namespace: string; name: string; number: string } {
		const [ns, nm] = (card.repoFullName || project.repoFullName).split('/');
		return { namespace: ns, name: nm, number: String(card.number) };
	}

	function sortedColumns(p: Project): ProjectColumn[] {
		return p.columns.map((c) => ({ ...c, cards: [...c.cards].sort((a, b) => a.position - b.position) })) as ProjectColumn[];
	}

	let columns = $derived(sortedColumns(project));

	const sorted = $derived([...columns].sort((a, b) => a.position - b.position));
	const repoParts = $derived(project.repoFullName ? project.repoFullName.split('/') : []);

	// Drag state
	let dragging = $state<{ issueId: string; fromColumn: string } | null>(null);
	let overColumn = $state('');
	let overIndex = $state(-1);
	let busy = $state(false);

	function onDragStart(e: DragEvent, card: ProjectCard, column: ProjectColumn) {
		if (!canEdit) return;
		dragging = { issueId: card.issueId, fromColumn: column.id };
		e.dataTransfer?.setData('text/plain', card.issueId);
		if (e.dataTransfer) e.dataTransfer.effectAllowed = 'move';
	}

	function onDragOverColumn(e: DragEvent, column: ProjectColumn) {
		if (!dragging) return;
		e.preventDefault();
		overColumn = column.id;
		if (overIndex < 0 || !(e.target as HTMLElement).closest('[data-card]')) overIndex = column.cards.length;
	}

	function onDragOverCard(e: DragEvent, column: ProjectColumn, index: number) {
		if (!dragging) return;
		e.preventDefault();
		e.stopPropagation();
		const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
		const below = e.clientY > rect.top + rect.height / 2;
		overColumn = column.id;
		overIndex = below ? index + 1 : index;
	}

	function resetDrag() {
		dragging = null;
		overColumn = '';
		overIndex = -1;
	}

	async function onDrop(e: DragEvent, column: ProjectColumn) {
		e.preventDefault();
		if (!dragging || busy) return resetDrag();
		const { issueId, fromColumn } = dragging;
		const from = columns.find((c) => c.id === fromColumn);
		const card = from?.cards.find((c) => c.issueId === issueId);
		let target = overIndex < 0 ? column.cards.length : overIndex;
		if (fromColumn === column.id && card) {
			const currentIndex = column.cards.indexOf(card);
			if (currentIndex < target) target -= 1;
			if (currentIndex === target) return resetDrag();
		}
		resetDrag();
		if (!card || !from) return;
		// Optimistic reorder before the server confirms
		columns = columns.map((c) => {
			let cards = c.cards;
			if (c.id === fromColumn) cards = cards.filter((k) => k.issueId !== issueId);
			if (c.id === column.id) cards = [...cards.slice(0, target), card, ...cards.slice(target)];
			return { ...c, cards } as ProjectColumn;
		});
		busy = true;
		try {
			const resp = await rpcClient.project.moveIssue({ projectId: project.id, issueId, columnId: column.id, position: target });
			if (resp.project) applyProject(resp.project);
			onChanged();
		} catch {
			onChanged();
		} finally {
			busy = false;
		}
	}

	function applyProject(p: Project) {
		columns = sortedColumns(p);
	}

	// Column editing
	let addingColumn = $state(false);
	let newColumnTitle = $state('');
	let columnBusy = $state(false);

	async function createColumn() {
		if (!newColumnTitle.trim() || columnBusy) return;
		columnBusy = true;
		try {
			await rpcClient.project.createColumn({ projectId: project.id, title: newColumnTitle.trim(), color: '' });
			newColumnTitle = '';
			addingColumn = false;
			onChanged();
		} catch {
			// Error interceptor already toasted
		} finally {
			columnBusy = false;
		}
	}

	let renameOpen = $state(false);
	let renameTarget = $state<ProjectColumn | null>(null);
	let renameTitle = $state('');
	let renameColor = $state('');

	function openRename(c: ProjectColumn) {
		renameTarget = c;
		renameTitle = c.title;
		renameColor = c.color ? normalizeHex(c.color) : '';
		renameOpen = true;
	}

	async function saveRename() {
		if (!renameTarget || !renameTitle.trim() || columnBusy) return;
		columnBusy = true;
		try {
			await rpcClient.project.updateColumn({ projectId: project.id, id: renameTarget.id, title: renameTitle.trim(), color: renameColor });
			renameOpen = false;
			onChanged();
		} catch {
			// Error interceptor already toasted
		} finally {
			columnBusy = false;
		}
	}

	async function setDefault(c: ProjectColumn) {
		try {
			await rpcClient.project.updateColumn({ projectId: project.id, id: c.id, isDefault: true });
			onChanged();
		} catch {
			// Error interceptor already toasted
		}
	}

	async function moveColumn(c: ProjectColumn, delta: number) {
		const index = sorted.findIndex((x) => x.id === c.id);
		const target = index + delta;
		if (target < 0 || target >= sorted.length) return;
		try {
			const resp = await rpcClient.project.moveColumn({ projectId: project.id, id: c.id, position: target });
			if (resp.project) applyProject(resp.project);
			onChanged();
		} catch {
			// Error interceptor already toasted
		}
	}

	let deleteColumnOpen = $state(false);
	let deleteColumnTarget = $state<ProjectColumn | null>(null);
	let deletingColumn = $state(false);

	async function deleteColumn() {
		if (!deleteColumnTarget) return;
		deletingColumn = true;
		try {
			await rpcClient.project.deleteColumn({ projectId: project.id, id: deleteColumnTarget.id });
			toast.success('Column deleted');
			deleteColumnOpen = false;
			onChanged();
		} catch {
			// Error interceptor already toasted
		} finally {
			deletingColumn = false;
		}
	}

	// Add issue
	let addOpen = $state(false);
	let addColumn = $state('');
	let addQuery = $state('');
	let addResults = $state<Issue[]>([]);
	let addLoading = $state(false);
	let addDebounce: ReturnType<typeof setTimeout> | undefined;
	let addingIssue = $state('');

	const onBoard = $derived(new Set(columns.flatMap((c) => c.cards.map((k) => k.issueId))));

	function openAdd(columnId: string) {
		addColumn = columnId;
		addQuery = '';
		addResults = [];
		addOpen = true;
		searchIssues();
	}

	async function searchIssues() {
		addLoading = true;
		const page = { pageSize: 20, pageToken: '', orderBy: 'updated_at desc', query: { text: addQuery.trim(), filters: [] } };
		try {
			if (repoParts.length === 2) {
				const resp = await rpcClient.issue.listIssues({ namespace: repoParts[0], name: repoParts[1], state: IssueState.OPEN, page }, silentCallOptions);
				addResults = resp.issues;
			} else {
				const resp = await rpcClient.issue.searchIssues({ page, state: IssueState.OPEN, namespace: searchNamespace }, silentCallOptions);
				addResults = resp.issues;
			}
		} catch {
			addResults = [];
		} finally {
			addLoading = false;
		}
	}

	function onAddQuery() {
		clearTimeout(addDebounce);
		addDebounce = setTimeout(searchIssues, 250);
	}

	async function addIssue(issue: Issue) {
		if (addingIssue) return;
		addingIssue = issue.id;
		try {
			await rpcClient.project.addIssueToProject({ projectId: project.id, issueId: issue.id, columnId: addColumn });
			toast.success(`#${issue.number} added`);
			onChanged();
		} catch {
			// Error interceptor already toasted
		} finally {
			addingIssue = '';
		}
	}

	async function removeCard(card: ProjectCard) {
		try {
			await rpcClient.project.removeIssueFromProject({ projectId: project.id, issueId: card.issueId });
			onChanged();
		} catch {
			// Error interceptor already toasted
		}
	}

	function dotColor(c: ProjectColumn): string {
		return c.color ? normalizeHex(c.color) : '#9ca3af';
	}
</script>

<div class="flex gap-4 overflow-x-auto pb-4 -mx-1 px-1 min-h-[24rem]">
	{#each sorted as column, ci (column.id)}
		<section
			class={cn(
				'w-[min(18rem,calc(100vw-3rem))] shrink-0 rounded-xl border bg-muted/20 flex flex-col max-h-[calc(100vh-14rem)] transition-colors',
				overColumn === column.id && dragging ? 'border-primary/50 bg-primary/5' : 'border-border/60'
			)}
			role="list"
			aria-label={column.title}
			ondragover={(e) => onDragOverColumn(e, column)}
			ondragleave={(e) => { if (!(e.currentTarget as HTMLElement).contains(e.relatedTarget as Node)) { if (overColumn === column.id) { overColumn = ''; overIndex = -1; } } }}
			ondrop={(e) => onDrop(e, column)}
		>
			<header class="flex items-center gap-2 px-3 py-2.5 border-b border-border/50">
				<span class="h-2.5 w-2.5 rounded-full shrink-0" style="background-color: {dotColor(column)}"></span>
				<h3 class="text-[13px] font-semibold truncate">{column.title}</h3>
				<span class="text-[11px] tabular-nums text-muted-foreground rounded-full bg-muted px-1.5">{column.cards.length}</span>
				{#if column.isDefault}
					<Star class="h-3 w-3 text-amber-600 dark:text-amber-400" aria-label="Default column" />
				{/if}
				{#if canEdit}
					<div class="ml-auto flex items-center gap-0.5">
						<Button variant="ghost" size="icon" class="h-6 w-6" title="Add issue" onclick={() => openAdd(column.id)}><Plus class="h-3.5 w-3.5" /></Button>
						<DropdownMenu>
							<DropdownMenuTrigger>
								{#snippet child({ props })}
									<Button {...props} variant="ghost" size="icon" class="h-6 w-6" aria-label="Column actions"><MoreHorizontal class="h-3.5 w-3.5" /></Button>
								{/snippet}
							</DropdownMenuTrigger>
							<DropdownMenuContent align="end" class="w-44">
								<DropdownMenuItem onclick={() => openRename(column)}><Pencil class="h-3.5 w-3.5 mr-2" />Edit column</DropdownMenuItem>
								{#if !column.isDefault}
									<DropdownMenuItem onclick={() => setDefault(column)}><Star class="h-3.5 w-3.5 mr-2" />Make default</DropdownMenuItem>
								{/if}
								<DropdownMenuSeparator />
								<DropdownMenuItem disabled={ci === 0} onclick={() => moveColumn(column, -1)}><ArrowLeft class="h-3.5 w-3.5 mr-2" />Move left</DropdownMenuItem>
								<DropdownMenuItem disabled={ci === sorted.length - 1} onclick={() => moveColumn(column, 1)}><ArrowRight class="h-3.5 w-3.5 mr-2" />Move right</DropdownMenuItem>
								<DropdownMenuSeparator />
								<DropdownMenuItem class="text-destructive" disabled={column.isDefault} onclick={() => { deleteColumnTarget = column; deleteColumnOpen = true; }}><Trash2 class="h-3.5 w-3.5 mr-2" />Delete column</DropdownMenuItem>
							</DropdownMenuContent>
						</DropdownMenu>
					</div>
				{/if}
			</header>
			<div class="flex-1 overflow-y-auto p-2 space-y-2">
				{#each column.cards as card, i (card.issueId)}
					{#if dragging && overColumn === column.id && overIndex === i && dragging.issueId !== card.issueId}
						<div class="h-1 rounded bg-primary/60"></div>
					{/if}
					<article
						data-card
						role="listitem"
						draggable={canEdit}
						ondragstart={(e) => onDragStart(e, card, column)}
						ondragend={resetDrag}
						ondragover={(e) => onDragOverCard(e, column, i)}
						class={cn(
							'group rounded-lg border border-border/60 bg-card px-3 py-2 shadow-xs transition-opacity',
							canEdit && 'cursor-grab active:cursor-grabbing',
							dragging?.issueId === card.issueId && 'opacity-40'
						)}
					>
						<div class="flex items-start gap-2">
							{#if card.isPull}
								<GitPullRequest class="h-3.5 w-3.5 mt-0.5 shrink-0 {card.state === 'closed' || card.state === 'merged' ? 'text-purple-500' : 'text-success'}" />
							{:else if card.state === 'closed'}
								<CircleCheck class="h-3.5 w-3.5 mt-0.5 shrink-0 text-purple-500" />
							{:else}
								<CircleDot class="h-3.5 w-3.5 mt-0.5 shrink-0 text-success" />
							{/if}
							{#if card.isPull}
								<a href={resolve('/[namespace]/[name]/pulls/[number]', cardParams(card))} class="min-w-0 flex-1 text-[13px] font-medium leading-snug hover:text-primary">{card.title}</a>
							{:else}
								<a href={resolve('/[namespace]/[name]/issues/[number]', cardParams(card))} class="min-w-0 flex-1 text-[13px] font-medium leading-snug hover:text-primary">{card.title}</a>
							{/if}
							{#if canEdit}
								<button type="button" class="h-5 w-5 shrink-0 inline-flex items-center justify-center rounded text-muted-foreground opacity-0 group-hover:opacity-100 hover:text-destructive transition-opacity" title="Remove from board" onclick={() => removeCard(card)}>
									<X class="h-3 w-3" />
								</button>
							{/if}
						</div>
						<div class="mt-1.5 flex flex-wrap items-center gap-x-2 gap-y-1 text-[11.5px] text-muted-foreground">
							<span class="tabular-nums">{project.repoFullName ? '' : `${card.repoFullName} `}#{card.number}</span>
							{#each card.labels as l (l)}
								<span class="rounded-full border border-border/60 px-1.5 leading-4">{l}</span>
							{/each}
							{#if card.assignees.length}
								<span class="ml-auto truncate">{card.assignees.join(', ')}</span>
							{/if}
						</div>
					</article>
				{/each}
				{#if dragging && overColumn === column.id && overIndex >= column.cards.length}
					<div class="h-1 rounded bg-primary/60"></div>
				{/if}
				{#if column.cards.length === 0 && !dragging}
					<p class="text-[12px] text-muted-foreground text-center py-6">No cards</p>
				{/if}
			</div>
		</section>
	{/each}

	{#if canEdit}
		<div class="w-[min(18rem,calc(100vw-3rem))] shrink-0">
			{#if addingColumn}
				<div class="rounded-xl border border-border/60 bg-muted/20 p-3 space-y-3">
					<FormField label="Column name" id="new-column-title" bordered={false}>
						<Input id="new-column-title" bind:value={newColumnTitle} placeholder="In review" onkeydown={(e) => { if (e.key === 'Enter') createColumn(); if (e.key === 'Escape') addingColumn = false; }} />
					</FormField>
					<div class="flex items-center justify-end gap-2">
						<Button size="sm" variant="outline" class="h-8" onclick={() => (addingColumn = false)}>Cancel</Button>
						<Button size="sm" class="h-8" onclick={createColumn} disabled={columnBusy || !newColumnTitle.trim()}>Add Column</Button>
					</div>
				</div>
			{:else}
				<button type="button" class="w-full rounded-xl border border-dashed border-border/80 px-3 py-3 text-[13px] text-muted-foreground hover:text-foreground hover:border-primary/40 transition-colors flex items-center justify-center gap-1.5" onclick={() => (addingColumn = true)}>
					<Plus class="h-3.5 w-3.5" />Add column
				</button>
			{/if}
		</div>
	{/if}
</div>

<FormPanel bind:open={renameOpen} title="Edit column" description="Rename the column or give it an accent color." icon={Pencil}>
	<div class="space-y-3">
		<FormField label="Title" id="column-title" required>
			<Input id="column-title" bind:value={renameTitle} />
		</FormField>
		<FormField label="Color" id="column-color" help="Optional accent shown next to the title">
			<div class="flex items-center gap-2">
				<input type="color" value={renameColor || '#9ca3af'} oninput={(e) => (renameColor = (e.currentTarget as HTMLInputElement).value)} class="h-8 w-10 rounded border border-border/60 bg-transparent cursor-pointer" aria-label="Column color" />
				<Input id="column-color" bind:value={renameColor} placeholder="#9ca3af" class="font-mono w-32" />
				{#if renameColor}
					<Button variant="ghost" size="sm" class="h-8" onclick={() => (renameColor = '')}>Clear</Button>
				{/if}
			</div>
		</FormField>
	</div>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (renameOpen = false)}>Cancel</Button>
		<Button onclick={saveRename} disabled={columnBusy || !renameTitle.trim()}>{columnBusy ? 'Saving...' : 'Save Changes'}</Button>
	{/snippet}
</FormPanel>

<FormPanel bind:open={addOpen} title="Add issue to board" description="Open issues not yet on this board" icon={Plus}>
	<div class="space-y-2">
		<div class="relative">
			<Search class="h-3.5 w-3.5 text-muted-foreground absolute left-2.5 top-1/2 -translate-y-1/2" />
			<Input bind:value={addQuery} oninput={onAddQuery} placeholder="Search issues" class="pl-8" aria-label="Search issues" />
			{#if addLoading}<Loader2 class="h-3.5 w-3.5 animate-spin text-muted-foreground absolute right-2.5 top-1/2 -translate-y-1/2" />{/if}
		</div>
		<div class="rounded-lg border border-border/60 divide-y divide-border/40 max-h-80 overflow-y-auto">
			{#if !addLoading && addResults.length === 0}
				<p class="px-3 py-6 text-[12.5px] text-muted-foreground text-center">No issues match</p>
			{/if}
			{#each addResults as issue (issue.id)}
				{@const present = onBoard.has(issue.id)}
				<div class="flex items-center gap-2 px-3 py-2 text-[13px]">
					<span class="text-muted-foreground tabular-nums shrink-0">{project.repoFullName ? '' : `${issue.repoFullName} `}#{issue.number}</span>
					<span class="truncate flex-1">{issue.title}</span>
					<Button size="sm" variant="outline" class="h-8 shrink-0" disabled={present || addingIssue === issue.id} onclick={() => addIssue(issue)}>{present ? 'On board' : 'Add'}</Button>
				</div>
			{/each}
		</div>
	</div>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (addOpen = false)}>Done</Button>
	{/snippet}
</FormPanel>

<ConfirmDialog bind:open={deleteColumnOpen} title="Delete column" confirmLabel="Delete" onConfirm={deleteColumn} loading={deletingColumn} icon={Trash2}>
	{#snippet description()}
		Cards in <strong>{deleteColumnTarget?.title}</strong> move to the default column.
	{/snippet}
</ConfirmDialog>

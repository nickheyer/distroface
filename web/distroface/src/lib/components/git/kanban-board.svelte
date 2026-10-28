<script lang="ts">
	import { resolve } from '$app/paths';
	import {
		Plus,
		Ellipsis,
		Pencil,
		Trash,
		ArrowLeft,
		ArrowRight,
		Star,
		X,
		CircleDot,
		CheckCircle,
		GitPullRequest,
		Search,
		Loader
	} from '$lib/icons';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Input } from '$lib/components/ui/input';
	import {
		DropdownMenu,
		DropdownMenuContent,
		DropdownMenuItem,
		DropdownMenuSeparator,
		DropdownMenuTrigger
	} from '$lib/components/ui/dropdown-menu';
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
		return p.columns.map((c) => ({
			...c,
			cards: [...c.cards].sort((a, b) => a.position - b.position)
		})) as ProjectColumn[];
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
		if (overIndex < 0 || !(e.target as HTMLElement).closest('[data-card]'))
			overIndex = column.cards.length;
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
			const resp = await rpcClient.project.moveIssue({
				projectId: project.id,
				issueId,
				columnId: column.id,
				position: target
			});
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
			await rpcClient.project.createColumn({
				projectId: project.id,
				title: newColumnTitle.trim(),
				color: ''
			});
			newColumnTitle = '';
			addingColumn = false;
			onChanged();
		} catch {
			// Error interceptor already toasted
		} finally {
			columnBusy = false;
		}
	}

	// Editing swaps the column header for an inline form
	let renameTarget = $state<ProjectColumn | null>(null);
	let renameTitle = $state('');
	let renameColor = $state('');

	function openRename(c: ProjectColumn) {
		renameTarget = c;
		renameTitle = c.title;
		renameColor = c.color ? normalizeHex(c.color) : '';
	}

	function closeRename() {
		renameTarget = null;
	}

	async function saveRename() {
		if (!renameTarget || !renameTitle.trim() || columnBusy) return;
		columnBusy = true;
		try {
			await rpcClient.project.updateColumn({
				projectId: project.id,
				id: renameTarget.id,
				title: renameTitle.trim(),
				color: renameColor
			});
			closeRename();
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
			const resp = await rpcClient.project.moveColumn({
				projectId: project.id,
				id: c.id,
				position: target
			});
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

	// Adding opens a search panel at the top of the column
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
		searchIssues();
	}

	function closeAdd() {
		addColumn = '';
		addResults = [];
	}

	async function searchIssues() {
		addLoading = true;
		const page = {
			pageSize: 20,
			pageToken: '',
			orderBy: 'updated_at desc',
			query: { text: addQuery.trim(), filters: [] }
		};
		try {
			if (repoParts.length === 2) {
				const resp = await rpcClient.issue.listIssues(
					{ namespace: repoParts[0], name: repoParts[1], state: IssueState.OPEN, page },
					silentCallOptions
				);
				addResults = resp.issues;
			} else {
				const resp = await rpcClient.issue.searchIssues(
					{ page, state: IssueState.OPEN, namespace: searchNamespace },
					silentCallOptions
				);
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
			await rpcClient.project.addIssueToProject({
				projectId: project.id,
				issueId: issue.id,
				columnId: addColumn
			});
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
			await rpcClient.project.removeIssueFromProject({
				projectId: project.id,
				issueId: card.issueId
			});
			onChanged();
		} catch {
			// Error interceptor already toasted
		}
	}

	function dotColor(c: ProjectColumn): string {
		return c.color ? normalizeHex(c.color) : '#9ca3af';
	}
</script>

<div class="-mx-1 flex min-h-96 gap-4 overflow-x-auto px-1 pb-4">
	{#each sorted as column, ci (column.id)}
		<section
			class={cn(
				'box flex max-h-[calc(100vh-14rem)] w-[min(18rem,calc(100vw-3rem))] shrink-0 flex-col transition-colors',
				overColumn === column.id && dragging && 'border-primary/50'
			)}
			role="list"
			aria-label={column.title}
			ondragover={(e) => onDragOverColumn(e, column)}
			ondragleave={(e) => {
				if (!(e.currentTarget as HTMLElement).contains(e.relatedTarget as Node)) {
					if (overColumn === column.id) {
						overColumn = '';
						overIndex = -1;
					}
				}
			}}
			ondrop={(e) => onDrop(e, column)}
		>
			{#if renameTarget?.id === column.id}
				<header class="space-y-2 border-b bg-muted/40 p-2">
					<FormField label="Title" id="column-title-{column.id}" required>
						<Input
							id="column-title-{column.id}"
							bind:value={renameTitle}
							onkeydown={(e) => {
								if (e.key === 'Enter') saveRename();
								if (e.key === 'Escape') closeRename();
							}}
						/>
					</FormField>
					<FormField
						label="Color"
						id="column-color-{column.id}"
						help="Accent shown next to the title"
					>
						<div class="flex items-center gap-2">
							<input
								type="color"
								value={renameColor || '#9ca3af'}
								oninput={(e) => (renameColor = (e.currentTarget as HTMLInputElement).value)}
								class="h-8 w-10 shrink-0 cursor-pointer rounded-md border bg-transparent"
								aria-label="Column color"
							/>
							<Input
								id="column-color-{column.id}"
								bind:value={renameColor}
								placeholder="#9ca3af"
								class="font-mono"
							/>
							{#if renameColor}
								<Button
									variant="ghost-muted"
									size="icon-xs"
									class="shrink-0"
									title="Clear color"
									aria-label="Clear color"
									onclick={() => (renameColor = '')}><X /></Button
								>
							{/if}
						</div>
					</FormField>
					<div class="flex items-center justify-end gap-2">
						<Button variant="ghost-muted" size="sm" onclick={closeRename} disabled={columnBusy}
							>Cancel</Button
						>
						<Button size="sm" onclick={saveRename} disabled={columnBusy || !renameTitle.trim()}>
							{#if columnBusy}<Loader class="animate-spin" />{/if}
							{columnBusy ? 'Saving' : 'Save'}
						</Button>
					</div>
				</header>
			{:else}
				<header class="box-header">
					<div class="flex min-w-0 items-center gap-2">
						<span
							class="size-2.5 shrink-0 rounded-full"
							style="background-color: {dotColor(column)}"
						></span>
						<h3 class="truncate">{column.title}</h3>
						<span
							class="rounded-full bg-muted px-1.5 text-xs font-normal text-muted-foreground tabular-nums"
							>{column.cards.length}</span
						>
						{#if column.isDefault}
							<Star class="size-3 shrink-0 fill-warning text-warning" aria-label="Default column" />
						{/if}
					</div>
					{#if canEdit}
						<div class="flex shrink-0 items-center gap-0.5">
							<Button
								variant="ghost-muted"
								size="icon-xs"
								title="Add issue"
								aria-label="Add issue"
								onclick={() => (addColumn === column.id ? closeAdd() : openAdd(column.id))}
								><Plus /></Button
							>
							<DropdownMenu>
								<DropdownMenuTrigger>
									{#snippet child({ props })}
										<Button
											{...props}
											variant="ghost-muted"
											size="icon-xs"
											aria-label="Column actions"><Ellipsis /></Button
										>
									{/snippet}
								</DropdownMenuTrigger>
								<DropdownMenuContent align="end" class="w-44">
									<DropdownMenuItem onclick={() => openRename(column)}
										><Pencil />Edit column</DropdownMenuItem
									>
									{#if !column.isDefault}
										<DropdownMenuItem onclick={() => setDefault(column)}
											><Star />Make default</DropdownMenuItem
										>
									{/if}
									<DropdownMenuSeparator />
									<DropdownMenuItem disabled={ci === 0} onclick={() => moveColumn(column, -1)}
										><ArrowLeft />Move left</DropdownMenuItem
									>
									<DropdownMenuItem
										disabled={ci === sorted.length - 1}
										onclick={() => moveColumn(column, 1)}><ArrowRight />Move right</DropdownMenuItem
									>
									<DropdownMenuSeparator />
									<DropdownMenuItem
										variant="destructive"
										disabled={column.isDefault}
										onclick={() => {
											deleteColumnTarget = column;
											deleteColumnOpen = true;
										}}
									>
										<Trash />Delete column
									</DropdownMenuItem>
								</DropdownMenuContent>
							</DropdownMenu>
						</div>
					{/if}
				</header>
			{/if}
			{#if addColumn === column.id}
				<div class="space-y-2 border-b bg-muted/20 p-2">
					<div class="relative">
						<Search
							class="absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground"
						/>
						<Input
							bind:value={addQuery}
							oninput={onAddQuery}
							placeholder="Search open issues"
							class="pl-8"
							aria-label="Search issues"
							onkeydown={(e) => {
								if (e.key === 'Escape') closeAdd();
							}}
						/>
						{#if addLoading}<Loader
								class="absolute top-1/2 right-2.5 size-3.5 -translate-y-1/2 animate-spin text-muted-foreground"
							/>{/if}
					</div>
					<div class="max-h-56 divide-y overflow-y-auto rounded-md border bg-card">
						{#if !addLoading && addResults.length === 0}
							<p class="px-3 py-4 text-center text-sm text-muted-foreground">No issues match</p>
						{/if}
						{#each addResults as issue (issue.id)}
							{@const present = onBoard.has(issue.id)}
							<div class="flex items-center gap-2 px-2.5 py-1.5 text-sm">
								<span class="shrink-0 text-muted-foreground tabular-nums"
									>{project.repoFullName ? '' : `${issue.repoFullName} `}#{issue.number}</span
								>
								<span class="min-w-0 flex-1 truncate" title={issue.title}>{issue.title}</span>
								<Button
									variant="outline"
									size="xs"
									class="shrink-0"
									disabled={present || addingIssue === issue.id}
									onclick={() => addIssue(issue)}>{present ? 'On board' : 'Add'}</Button
								>
							</div>
						{/each}
					</div>
					<div class="flex justify-end">
						<Button variant="ghost-muted" size="sm" onclick={closeAdd}>Done</Button>
					</div>
				</div>
			{/if}
			<div class="flex-1 space-y-2 overflow-y-auto p-2">
				{#each column.cards as card, i (card.issueId)}
					{#if dragging && overColumn === column.id && overIndex === i && dragging.issueId !== card.issueId}
						<div class="h-0.5 rounded bg-primary"></div>
					{/if}
					<article
						data-card
						role="listitem"
						draggable={canEdit}
						ondragstart={(e) => onDragStart(e, card, column)}
						ondragend={resetDrag}
						ondragover={(e) => onDragOverCard(e, column, i)}
						class={cn(
							'card-interactive group p-3',
							canEdit && 'cursor-grab active:cursor-grabbing',
							dragging?.issueId === card.issueId && 'opacity-40'
						)}
					>
						<div class="flex items-start gap-2">
							{#if card.isPull}
								<GitPullRequest
									class="mt-0.5 size-3.5 shrink-0 {card.state === 'closed' ||
									card.state === 'merged'
										? 'text-done'
										: 'text-success'}"
								/>
							{:else if card.state === 'closed'}
								<CheckCircle class="mt-0.5 size-3.5 shrink-0 text-done" />
							{:else}
								<CircleDot class="mt-0.5 size-3.5 shrink-0 text-success" />
							{/if}
							{#if card.isPull}
								<a
									href={resolve('/[namespace]/[name]/pulls/[number]', cardParams(card))}
									class="min-w-0 flex-1 text-sm leading-snug font-medium hover:text-primary"
									>{card.title}</a
								>
							{:else}
								<a
									href={resolve('/[namespace]/[name]/issues/[number]', cardParams(card))}
									class="min-w-0 flex-1 text-sm leading-snug font-medium hover:text-primary"
									>{card.title}</a
								>
							{/if}
							{#if canEdit}
								<Button
									variant="ghost-muted"
									size="icon-xs"
									class="-mt-1 -mr-1 shrink-0 opacity-0 transition-opacity group-hover:opacity-100 hover:text-destructive focus-visible:opacity-100"
									title="Remove from board"
									aria-label="Remove from board"
									onclick={() => removeCard(card)}
								>
									<X />
								</Button>
							{/if}
						</div>
						<div
							class="mt-1.5 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground"
						>
							<span class="tabular-nums"
								>{project.repoFullName ? '' : `${card.repoFullName} `}#{card.number}</span
							>
							{#each card.labels as l (l)}
								<Badge variant="outline">{l}</Badge>
							{/each}
							{#if card.assignees.length}
								<span class="ml-auto truncate">{card.assignees.join(', ')}</span>
							{/if}
						</div>
					</article>
				{/each}
				{#if dragging && overColumn === column.id && overIndex >= column.cards.length}
					<div class="h-0.5 rounded bg-primary"></div>
				{/if}
				{#if column.cards.length === 0 && !dragging}
					<p class="py-6 text-center text-sm text-muted-foreground">No cards</p>
				{/if}
			</div>
		</section>
	{/each}

	{#if canEdit}
		<div class="w-[min(18rem,calc(100vw-3rem))] shrink-0">
			{#if addingColumn}
				<div class="space-y-4 rounded-lg border bg-card p-3">
					<FormField label="Column name" id="new-column-title">
						<Input
							id="new-column-title"
							bind:value={newColumnTitle}
							placeholder="In review"
							onkeydown={(e) => {
								if (e.key === 'Enter') createColumn();
								if (e.key === 'Escape') addingColumn = false;
							}}
						/>
					</FormField>
					<div class="flex items-center justify-end gap-2">
						<Button variant="outline" onclick={() => (addingColumn = false)}>Cancel</Button>
						<Button onclick={createColumn} disabled={columnBusy || !newColumnTitle.trim()}
							>Add column</Button
						>
					</div>
				</div>
			{:else}
				<button
					type="button"
					class="flex w-full items-center justify-center gap-1.5 rounded-lg border border-dashed px-3 py-3 text-sm text-muted-foreground transition-colors hover:border-border-strong hover:text-foreground"
					onclick={() => (addingColumn = true)}
				>
					<Plus class="size-3.5" />Add column
				</button>
			{/if}
		</div>
	{/if}
</div>

<ConfirmDialog
	bind:open={deleteColumnOpen}
	title="Delete column"
	confirmLabel="Delete"
	onConfirm={deleteColumn}
	loading={deletingColumn}
	icon={Trash}
>
	{#snippet description()}
		Cards in <strong>{deleteColumnTarget?.title}</strong> move to the default column.
	{/snippet}
</ConfirmDialog>

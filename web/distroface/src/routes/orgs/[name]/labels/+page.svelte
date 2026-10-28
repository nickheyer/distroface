<script lang="ts">
	import { onMount, getContext } from 'svelte';
	import { Tag, Plus, Pencil, Trash, RotateCw, Loader, X } from '$lib/icons';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Input } from '$lib/components/ui/input';
	import { Switch } from '$lib/components/ui/switch';
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
	import FormField from '$lib/components/form-field.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import LabelChip, { normalizeHex } from '$lib/components/git/label-chip.svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { Pager } from '$lib/pager.svelte';
	import type { Label } from '$lib/proto/distroface/v1/issue_pb';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';
	import { cn } from '$lib/utils';
	import { toast } from 'svelte-sonner';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgId = $derived(ctx.org?.id ?? '');

	const presets = [
		'#d73a4a',
		'#e4e669',
		'#0075ca',
		'#008672',
		'#7057ff',
		'#a2eeef',
		'#e99695',
		'#f9d0c4',
		'#fbca04',
		'#0e8a16',
		'#1d76db',
		'#5319e7',
		'#b60205',
		'#c5def5',
		'#bfd4f2',
		'#6b7280'
	];

	let labels = $state<Label[]>([]);
	const pager = new Pager(50);
	let loading = $state(true);
	let loaded = $state(false);

	async function load() {
		loading = true;
		try {
			const resp = await rpcClient.issue.listLabels({ page: pager.request(), orgId });
			labels = resp.labels;
			pager.apply(resp.page);
		} catch {
			labels = [];
		} finally {
			loading = false;
			loaded = true;
		}
	}

	onMount(load);

	let creating = $state(false);
	let editing = $state<Label | null>(null);
	let fName = $state('');
	let fColor = $state('#0075ca');
	let fDescription = $state('');
	let fExclusive = $state(false);
	let saving = $state(false);

	function randomColor() {
		fColor = presets[Math.floor(Math.random() * presets.length)];
	}

	function openCreate() {
		editing = null;
		fName = '';
		fDescription = '';
		fExclusive = false;
		randomColor();
		creating = true;
	}

	function openEdit(l: Label) {
		fName = l.name;
		fColor = normalizeHex(l.color);
		fDescription = l.description;
		fExclusive = !!l.exclusiveScope;
		creating = false;
		editing = l;
	}

	function closeForm() {
		creating = false;
		editing = null;
	}

	const colorValid = $derived(/^#?[0-9a-fA-F]{6}$/.test(fColor.trim()));
	const previewName = $derived(fName.trim() || 'Label preview');

	async function save() {
		if (!fName.trim() || !colorValid || saving) return;
		saving = true;
		const color = normalizeHex(fColor);
		try {
			if (editing) {
				await rpcClient.issue.updateLabel({
					orgId,
					id: editing.id,
					labelName: fName.trim(),
					color,
					description: fDescription.trim(),
					exclusive: fExclusive
				});
				toast.success('Label updated');
			} else {
				await rpcClient.issue.createLabel({
					orgId,
					labelName: fName.trim(),
					color,
					description: fDescription.trim(),
					exclusive: fExclusive
				});
				toast.success('Label created');
			}
			closeForm();
			await load();
		} catch {
			// error interceptor
		} finally {
			saving = false;
		}
	}

	let deleteOpen = $state(false);
	let deleteTarget = $state<Label | null>(null);
	let deleting = $state(false);

	async function confirmDelete() {
		if (!deleteTarget) return;
		deleting = true;
		try {
			await rpcClient.issue.deleteLabel({ orgId, id: deleteTarget.id });
			toast.success('Label deleted');
			deleteOpen = false;
			await load();
		} catch {
			// error interceptor
		} finally {
			deleting = false;
		}
	}
</script>

{#snippet labelForm()}
	<div class="space-y-4">
		<div class="flex items-center gap-2">
			<span class="eyebrow">Preview</span>
			<LabelChip name={previewName} color={colorValid ? fColor : '#6b7280'} />
		</div>
		<div class="grid gap-4 md:grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)_auto]">
			<FormField
				label="Name"
				id="label-name"
				required
				help="scope/name groups labels, like kind/bug"
			>
				<Input id="label-name" bind:value={fName} placeholder="kind/bug" autocomplete="off" />
			</FormField>
			<FormField label="Description" id="label-desc" help="Shown on hover">
				<Input id="label-desc" bind:value={fDescription} placeholder="Something is not working" />
			</FormField>
			<FormField
				label="Color"
				id="label-color"
				required
				error={fColor && !colorValid ? 'Six hex digits, like #0075ca' : ''}
			>
				<div class="flex items-center gap-1.5">
					<Button variant="outline" size="icon" onclick={randomColor} aria-label="Random color">
						<RotateCw />
					</Button>
					<input
						type="color"
						value={colorValid ? normalizeHex(fColor) : '#6b7280'}
						oninput={(e) => (fColor = (e.currentTarget as HTMLInputElement).value)}
						class="size-8 cursor-pointer rounded-md border border-input bg-field p-0.5"
						aria-label="Pick a color"
					/>
					<Input
						id="label-color"
						bind:value={fColor}
						class="w-28 font-mono"
						placeholder="#0075ca"
						aria-invalid={!colorValid}
					/>
				</div>
			</FormField>
		</div>
		<div class="flex flex-wrap gap-1.5">
			{#each presets as p (p)}
				<button
					type="button"
					class={cn(
						'size-5 rounded-md border border-foreground/10 transition-transform hover:scale-110',
						normalizeHex(fColor) === p && 'ring-2 ring-primary ring-offset-1 ring-offset-background'
					)}
					style="background-color: {p}"
					onclick={() => (fColor = p)}
					aria-label="Use {p}"
				></button>
			{/each}
		</div>
		<div class="flex flex-wrap items-end justify-between gap-4 border-t pt-4">
			<FormField
				label="Exclusive scope"
				id="label-exclusive"
				help="Only one label from this scope/ prefix can be on an issue at a time"
				layout="toggle"
				class="min-w-0 flex-1"
			>
				<Switch id="label-exclusive" bind:checked={fExclusive} disabled={!fName.includes('/')} />
			</FormField>
			<div class="flex items-center gap-2">
				<Button variant="outline" onclick={closeForm}>Cancel</Button>
				<Button onclick={save} disabled={saving || !fName.trim() || !colorValid}>
					{#if saving}<Loader class="animate-spin" />{/if}
					{saving ? 'Saving' : editing ? 'Save changes' : 'Create label'}
				</Button>
			</div>
		</div>
	</div>
{/snippet}

{#snippet labelRow(l: Label)}
	{#if editing?.id === l.id}
		<TableRow class="bg-muted/30 hover:[&>td]:bg-transparent">
			<TableCell colspan={4} class="px-4 py-4 whitespace-normal">
				{@render labelForm()}
			</TableCell>
		</TableRow>
	{:else}
		<TableRow>
			<TableCell>
				<div class="flex flex-wrap items-center gap-2">
					<LabelChip name={l.name} color={l.color} description={l.description} />
					{#if l.exclusiveScope}
						<Badge variant="outline" title="One label per {l.exclusiveScope}/ scope"
							>Exclusive</Badge
						>
					{/if}
				</div>
			</TableCell>
			<TableCell class="hidden text-sm whitespace-normal text-muted-foreground md:table-cell"
				>{l.description || '-'}</TableCell
			>
			<TableCell class="hidden text-sm text-muted-foreground sm:table-cell">
				<span class="tabular-nums">{Number(l.openIssues)} open</span>
				<span class="mx-1 text-muted-foreground/50">/</span>
				<span class="tabular-nums">{Number(l.closedIssues)} closed</span>
			</TableCell>
			<TableCell class="text-right">
				{#if ctx.canAdmin}
					<div class="flex items-center justify-end gap-1">
						<Button
							variant="ghost-muted"
							size="icon-sm"
							onclick={() => openEdit(l)}
							aria-label="Edit {l.name}"><Pencil /></Button
						>
						<Button
							variant="ghost-muted"
							size="icon-sm"
							class="text-destructive hover:text-destructive"
							onclick={() => {
								deleteTarget = l;
								deleteOpen = true;
							}}
							aria-label="Delete {l.name}"><Trash /></Button
						>
					</div>
				{/if}
			</TableCell>
		</TableRow>
	{/if}
{/snippet}

<div class="space-y-4">
	<div class="section-header">
		<div class="min-w-0">
			<h2 class="section-title">Labels</h2>
			<p class="section-subtitle">Shared by every repository in this organization.</p>
		</div>
		{#if ctx.canAdmin}
			{#if creating}
				<Button variant="outline" class="shrink-0" onclick={closeForm}><X />Cancel</Button>
			{:else}
				<Button class="shrink-0" onclick={openCreate}><Plus />New label</Button>
			{/if}
		{/if}
	</div>

	{#if creating}
		<div class="box p-4">
			{@render labelForm()}
		</div>
	{/if}

	{#if !loaded}
		<div class="box divide-y">
			{#each { length: 3 }, i (i)}
				<div class="flex items-center gap-3 px-4 py-3">
					<Skeleton class="h-5 w-20 rounded-full" />
					<Skeleton class="h-4 w-56" />
				</div>
			{/each}
		</div>
	{:else if labels.length === 0}
		{#if !creating}
			<EmptyState
				icon={Tag}
				message="No organization labels"
				description="Labels defined here appear on issues in every repository."
			>
				{#snippet actions()}
					{#if ctx.canAdmin}<Button variant="outline" onclick={openCreate}><Plus />New label</Button
						>{/if}
				{/snippet}
			</EmptyState>
		{/if}
	{:else}
		<div class="data-table transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
			<Table>
				<TableHeader>
					<TableRow>
						<TableHead>Label</TableHead>
						<TableHead class="hidden md:table-cell">Description</TableHead>
						<TableHead class="hidden w-40 sm:table-cell">Issues</TableHead>
						<TableHead class="w-24 text-right"></TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{#each labels as l (l.id)}
						{@render labelRow(l)}
					{/each}
				</TableBody>
			</Table>
			<DataPagination attached {pager} onChange={load} />
		</div>
	{/if}
</div>

<ConfirmDialog
	bind:open={deleteOpen}
	title="Delete label"
	confirmLabel="Delete"
	onConfirm={confirmDelete}
	loading={deleting}
	icon={Trash}
>
	{#snippet description()}
		Delete <strong>{deleteTarget?.name}</strong>? It is removed from every issue in the
		organization.
	{/snippet}
</ConfirmDialog>

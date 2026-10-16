<script lang="ts">
	import { onMount, getContext } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Switch } from '$lib/components/ui/switch';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import {
		Table, TableBody, TableCell, TableHead, TableHeader, TableRow
	} from '$lib/components/ui/table';
	import FormPanel from '$lib/components/form-panel.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import LabelChip, { normalizeHex } from '$lib/components/git/label-chip.svelte';
	import { Tag, Plus, Pencil, Trash2 } from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { toast } from 'svelte-sonner';
	import { Pager } from '$lib/pager.svelte';
	import type { Label } from '$lib/proto/distroface/v1/issue_pb';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgId = $derived(ctx.org?.id ?? '');

	const PALETTE = [
		'#ef4444', '#f97316', '#eab308', '#22c55e', '#14b8a6', '#0ea5e9',
		'#3b82f6', '#8b5cf6', '#ec4899', '#6b7280', '#1f2937', '#d1d5db'
	];

	let labels = $state<Label[]>([]);
	const pager = new Pager(50);
	let loading = $state(true);
	let loaded = $state(false);

	let editorOpen = $state(false);
	let editing = $state<Label | null>(null);
	let formName = $state('');
	let formColor = $state('#3b82f6');
	let formDescription = $state('');
	let formExclusive = $state(false);
	let saving = $state(false);

	let deleteOpen = $state(false);
	let deleteTarget = $state<Label | null>(null);
	let deleting = $state(false);

	const colorValid = $derived(/^#?[0-9a-fA-F]{6}$/.test(formColor.trim()));
	const previewName = $derived(formName.trim() || 'label');

	async function loadLabels() {
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

	function openCreate() {
		editing = null;
		formName = '';
		formColor = PALETTE[Math.floor(Math.random() * PALETTE.length)];
		formDescription = '';
		formExclusive = false;
		editorOpen = true;
	}

	function openEdit(label: Label) {
		editing = label;
		formName = label.name;
		formColor = normalizeHex(label.color);
		formDescription = label.description;
		formExclusive = !!label.exclusiveScope;
		editorOpen = true;
	}

	async function save() {
		if (!formName.trim() || !colorValid) return;
		saving = true;
		try {
			const color = normalizeHex(formColor);
			if (editing) {
				await rpcClient.issue.updateLabel({
					orgId,
					id: editing.id,
					labelName: formName.trim(),
					color,
					description: formDescription.trim(),
					exclusive: formExclusive
				});
				toast.success('Label updated');
			} else {
				await rpcClient.issue.createLabel({
					orgId,
					labelName: formName.trim(),
					color,
					description: formDescription.trim(),
					exclusive: formExclusive
				});
				toast.success('Label created');
			}
			editorOpen = false;
			await loadLabels();
		} catch {
			// error interceptor
		} finally {
			saving = false;
		}
	}

	async function confirmDelete() {
		if (!deleteTarget) return;
		deleting = true;
		try {
			await rpcClient.issue.deleteLabel({ orgId, id: deleteTarget.id });
			toast.success('Label deleted');
			deleteOpen = false;
			await loadLabels();
		} catch {
			// error interceptor
		} finally {
			deleting = false;
		}
	}

	onMount(loadLabels);
</script>

<div class="space-y-4">
	<div class="section-header">
		<div class="min-w-0">
			<h2 class="section-title">Labels</h2>
			<p class="section-subtitle">Shared by every repository in this organization.</p>
		</div>
		{#if ctx.canAdmin}
			<Button size="sm" class="shrink-0" onclick={openCreate}>
				<Plus class="h-4 w-4 mr-1.5" />
				New Label
			</Button>
		{/if}
	</div>

	{#if !loaded}
		<div class="space-y-2">
			{#each { length: 3 }, i (i)}
				<Skeleton class="h-12 w-full rounded-lg" />
			{/each}
		</div>
	{:else if labels.length === 0}
		<EmptyState
			icon={Tag}
			message="No organization labels"
			description="Labels defined here appear on issues in every repository."
		/>
	{:else}
		<div class="data-table transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
			<Table>
				<TableHeader>
					<TableRow class="bg-muted/30 hover:bg-muted/30">
						<TableHead class="th">Label</TableHead>
						<TableHead class="th hidden sm:table-cell">Description</TableHead>
						<TableHead class="th w-28 hidden md:table-cell">Open issues</TableHead>
						{#if ctx.canAdmin}
							<TableHead class="th w-24"></TableHead>
						{/if}
					</TableRow>
				</TableHeader>
				<TableBody>
					{#each labels as label (label.id)}
						<TableRow>
							<TableCell class="py-3 px-3">
								<LabelChip name={label.name} color={label.color} description={label.description} />
							</TableCell>
							<TableCell class="text-sm text-muted-foreground py-3 px-3 hidden sm:table-cell">
								{label.description || '-'}
								{#if label.exclusiveScope}
									<span class="ml-2 text-[11px] uppercase tracking-wide">exclusive</span>
								{/if}
							</TableCell>
							<TableCell class="text-sm text-muted-foreground py-3 px-3 tabular-nums hidden md:table-cell">
								{Number(label.openIssues)}
							</TableCell>
							{#if ctx.canAdmin}
								<TableCell class="text-right py-3 px-3">
									<div class="flex justify-end gap-1">
										<Button variant="ghost" size="icon" class="h-7 w-7" onclick={() => openEdit(label)} aria-label="Edit">
											<Pencil class="h-3.5 w-3.5" />
										</Button>
										<Button
											variant="ghost" size="icon"
											class="h-7 w-7 text-destructive hover:text-destructive"
											onclick={() => { deleteTarget = label; deleteOpen = true; }}
											aria-label="Delete"
										>
											<Trash2 class="h-3.5 w-3.5" />
										</Button>
									</div>
								</TableCell>
							{/if}
						</TableRow>
					{/each}
				</TableBody>
			</Table>
			<DataPagination attached {pager} onChange={loadLabels} />
		</div>
	{/if}
</div>

<FormPanel bind:open={editorOpen} title={editing ? 'Edit Label' : 'New Label'} description="Labels group issues and pull requests." icon={Tag}>
	<div class="space-y-6">
		<FormSection title="Label">
			<div class="space-y-3">
				<FormField label="Name" id="label-name" required help="A slash makes a scope, like priority/high">
					<Input id="label-name" bind:value={formName} placeholder="bug" />
				</FormField>
				<FormField label="Description" id="label-desc">
					<Input id="label-desc" bind:value={formDescription} placeholder="Something is not working" />
				</FormField>
				<FormField label="Color" id="label-color" error={colorValid ? undefined : 'Six digit hex value'}>
					<div class="flex flex-wrap items-center gap-2">
						<input
							type="color"
							value={colorValid ? normalizeHex(formColor) : '#6b7280'}
							oninput={(e) => (formColor = (e.currentTarget as HTMLInputElement).value)}
							class="h-9 w-10 rounded-md border border-input bg-background p-0.5 cursor-pointer"
							aria-label="Pick color"
						/>
						<Input id="label-color" bind:value={formColor} placeholder="#3b82f6" class="w-32 font-mono" />
						<LabelChip name={previewName} color={colorValid ? formColor : '#6b7280'} />
					</div>
					<div class="flex flex-wrap gap-1.5 pt-1">
						{#each PALETTE as c (c)}
							<button
								type="button"
								class="h-6 w-6 rounded-full border border-black/10 dark:border-white/10 transition-transform hover:scale-110 {normalizeHex(formColor) === c ? 'ring-2 ring-ring ring-offset-2 ring-offset-background' : ''}"
								style="background-color: {c};"
								aria-label={c}
								onclick={() => (formColor = c)}
							></button>
						{/each}
					</div>
				</FormField>
				<FormField label="Exclusive" horizontal help="Issues carry only one label from this scope">
					<Switch bind:checked={formExclusive} />
				</FormField>
			</div>
		</FormSection>
	</div>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (editorOpen = false)}>Cancel</Button>
		<Button onclick={save} disabled={saving || !formName.trim() || !colorValid}>
			{saving ? 'Saving...' : editing ? 'Save Changes' : 'Create Label'}
		</Button>
	{/snippet}
</FormPanel>

<ConfirmDialog bind:open={deleteOpen} title="Delete Label" confirmLabel="Delete" onConfirm={confirmDelete} loading={deleting} icon={Trash2}>
	{#snippet description()}
		Delete <strong>{deleteTarget?.name}</strong>? It is removed from every issue in the organization.
	{/snippet}
</ConfirmDialog>

<script lang="ts">
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { Tag, Plus, Pencil, Trash, Building, RotateCw, Loader, X } from '$lib/icons';
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
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import LabelChip, { normalizeHex } from '$lib/components/git/label-chip.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { GitUnit } from '$lib/proto/distroface/v1/git_pb';
	import type { Label } from '$lib/proto/distroface/v1/issue_pb';
	import { cn } from '$lib/utils';
	import { toast } from 'svelte-sonner';

	const repo = $derived(gitRepoStore.repo!);
	const namespace = $derived(repo.namespace);
	const name = $derived(repo.name);
	const base = $derived(`/${namespace}/${name}`);
	const canManage = $derived(gitRepoStore.canWrite(GitUnit.ISSUES) && !repo.isArchived);

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
	let loading = $state(true);
	let query = $state('');

	const repoLabels = $derived(labels.filter((l) => !l.isOrgLabel && matches(l)));
	const orgLabels = $derived(labels.filter((l) => l.isOrgLabel && matches(l)));

	function matches(l: Label): boolean {
		const q = query.trim().toLowerCase();
		return !q || l.name.toLowerCase().includes(q) || l.description.toLowerCase().includes(q);
	}

	async function load() {
		loading = true;
		try {
			const resp = await rpcClient.issue.listLabels(
				{ namespace, name, page: { pageSize: 500, pageToken: '', orderBy: '' } },
				silentCallOptions
			);
			labels = resp.labels;
		} catch {
			labels = [];
		} finally {
			loading = false;
		}
	}

	onMount(() => {
		gitRepoStore.load(namespace, name);
		load();
	});

	// Creating shows the form above the list, editing swaps the row for it
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
		editing = l;
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
					namespace,
					name,
					id: editing.id,
					labelName: fName.trim(),
					color,
					description: fDescription.trim(),
					exclusive: fExclusive
				});
				toast.success('Label updated');
			} else {
				await rpcClient.issue.createLabel({
					namespace,
					name,
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
			// Error interceptor already toasted
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
			await rpcClient.issue.deleteLabel({ namespace, name, id: deleteTarget.id });
			toast.success('Label deleted');
			deleteOpen = false;
			await load();
		} catch {
			// Error interceptor already toasted
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

{#snippet labelRow(l: Label, editable: boolean)}
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
					<LabelChip name={l.name} color={l.color} />
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
				<a
					href="{resolve('/[namespace]/[name]/issues', {
						namespace,
						name
					})}?labels={encodeURIComponent(l.name)}"
					class="tabular-nums hover:text-primary">{Number(l.openIssues)} open</a
				>
				<span class="mx-1 text-muted-foreground/50">/</span>
				<a
					href="{resolve('/[namespace]/[name]/issues', {
						namespace,
						name
					})}?labels={encodeURIComponent(l.name)}&state=closed"
					class="tabular-nums hover:text-primary">{Number(l.closedIssues)} closed</a
				>
			</TableCell>
			<TableCell class="text-right">
				{#if editable}
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
	<div class="flex flex-wrap items-center gap-2">
		<Input
			bind:value={query}
			placeholder="Filter labels"
			class="w-full sm:w-64"
			aria-label="Filter labels"
		/>
		<span class="text-xs text-muted-foreground tabular-nums"
			>{labels.length} label{labels.length === 1 ? '' : 's'}</span
		>
		<div class="ml-auto flex items-center gap-2">
			<Button variant="outline" href="{base}/milestones">Milestones</Button>
			<PermissionGate allowed={canManage}>
				{#if creating}
					<Button variant="outline" onclick={closeForm}><X />Cancel</Button>
				{:else}
					<Button onclick={openCreate}><Plus />New label</Button>
				{/if}
			</PermissionGate>
		</div>
	</div>

	{#if creating}
		<div class="box p-4">
			{@render labelForm()}
		</div>
	{/if}

	{#if loading && labels.length === 0}
		<Skeleton class="h-48 w-full rounded-lg" />
	{:else if labels.length === 0}
		{#if !creating}
			<EmptyState
				icon={Tag}
				message="No labels yet"
				description="Labels group issues by kind, area, or priority."
			>
				{#snippet actions()}
					{#if canManage}<Button variant="outline" onclick={openCreate}><Plus />New label</Button
						>{/if}
				{/snippet}
			</EmptyState>
		{/if}
	{:else}
		<div class="data-table">
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
					{#if repoLabels.length === 0}
						<TableRow
							><TableCell colspan={4} class="py-6 text-center text-sm text-muted-foreground"
								>No repository labels{query ? ' match' : ''}</TableCell
							></TableRow
						>
					{/if}
					{#each repoLabels as l (l.id)}
						{@render labelRow(l, canManage)}
					{/each}
				</TableBody>
			</Table>
		</div>

		{#if orgLabels.length}
			<div class="space-y-2">
				<div class="flex items-center gap-2 text-sm text-muted-foreground">
					<Building class="size-3.5" />
					<span>Organization labels, managed at</span>
					<a href={resolve('/orgs/[name]/labels', { name: namespace })} class="link">{namespace}</a>
				</div>
				<div class="data-table">
					<Table>
						<TableBody>
							{#each orgLabels as l (l.id)}
								{@render labelRow(l, false)}
							{/each}
						</TableBody>
					</Table>
				</div>
			</div>
		{/if}
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
		This removes <strong>{deleteTarget?.name}</strong> from every issue and pull request.
	{/snippet}
</ConfirmDialog>

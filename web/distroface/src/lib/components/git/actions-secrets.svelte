<script lang="ts">
	import { onMount } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import {
		Table,
		TableBody,
		TableCell,
		TableHead,
		TableHeader,
		TableRow
	} from '$lib/components/ui/table';
	import FormField from '$lib/components/form-field.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import { Key, Variable as VariableIcon, Plus, Pencil, Trash, Loader, X } from '$lib/icons';
	import { rpcClient } from '$lib/api/rpc-client';
	import { toast } from 'svelte-sonner';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime } from '$lib/utils';
	import type { Secret, Variable } from '$lib/proto/distroface/v1/actions_pb';

	let {
		scope,
		canManage = true
	}: {
		scope: { kind: string; id: string };
		canManage?: boolean;
	} = $props();

	const NAME_RE = /^[A-Za-z_][A-Za-z0-9_]*$/;

	let secrets = $state<Secret[]>([]);
	let variables = $state<Variable[]>([]);
	let loaded = $state(false);

	type Kind = 'secret' | 'variable';
	let creating = $state<Kind | null>(null);
	let editing = $state<{ kind: Kind; name: string } | null>(null);
	let formName = $state('');
	let formValue = $state('');
	let saving = $state(false);

	let deleteOpen = $state(false);
	let deleteKind = $state<Kind>('secret');
	let deleteName = $state('');
	let deleting = $state(false);

	const nameValid = $derived(NAME_RE.test(formName.trim()));

	async function load() {
		try {
			const [s, v] = await Promise.all([
				rpcClient.actions.listSecrets({ page: { pageSize: 200 }, scope }),
				rpcClient.actions.listVariables({ page: { pageSize: 200 }, scope })
			]);
			secrets = s.secrets;
			variables = v.variables;
		} catch {
			secrets = [];
			variables = [];
		} finally {
			loaded = true;
		}
	}

	function openCreate(kind: Kind) {
		editing = null;
		formName = '';
		formValue = '';
		creating = kind;
	}

	function openEdit(kind: Kind, existing: { name: string; value?: string }) {
		creating = null;
		formName = existing.name;
		formValue = existing.value ?? '';
		editing = { kind, name: existing.name };
	}

	function closeForm() {
		creating = null;
		editing = null;
	}

	async function save(kind: Kind) {
		if (!nameValid || !formValue || saving) return;
		saving = true;
		const name = formName.trim().toUpperCase();
		const call =
			kind === 'secret'
				? rpcClient.actions.setSecret({ scope, secretName: name, value: formValue })
				: rpcClient.actions.setVariable({ scope, variableName: name, value: formValue });
		const ok = await call.then(
			() => true,
			() => false
		);
		saving = false;
		if (!ok) return;
		toast.success(kind === 'secret' ? 'Secret saved' : 'Variable saved');
		closeForm();
		await load();
	}

	function openDelete(kind: Kind, name: string) {
		deleteKind = kind;
		deleteName = name;
		deleteOpen = true;
	}

	async function confirmDelete() {
		deleting = true;
		try {
			if (deleteKind === 'secret') {
				await rpcClient.actions.deleteSecret({ scope, secretName: deleteName });
			} else {
				await rpcClient.actions.deleteVariable({ scope, variableName: deleteName });
			}
			toast.success(deleteKind === 'secret' ? 'Secret deleted' : 'Variable deleted');
			deleteOpen = false;
			await load();
		} catch {
			// error interceptor
		} finally {
			deleting = false;
		}
	}

	onMount(load);
</script>

{#snippet kvForm(kind: Kind)}
	<div class="space-y-4">
		<div class="grid gap-4 md:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]">
			<FormField
				label="Name"
				id="actions-{kind}-name"
				required
				help="Letters, digits, underscores, stored upper case"
				error={formName && !nameValid ? 'Invalid name' : undefined}
			>
				<Input
					id="actions-{kind}-name"
					bind:value={formName}
					placeholder={kind === 'secret' ? 'NPM_TOKEN' : 'NODE_VERSION'}
					class="font-mono"
					disabled={!!editing}
					autocomplete="off"
					aria-invalid={!!formName && !nameValid}
				/>
			</FormField>
			<FormField
				label="Value"
				id="actions-{kind}-value"
				required
				help={kind === 'secret'
					? 'Sealed on save and never shown again'
					: 'Visible to anyone who can read workflow runs'}
			>
				<Textarea
					id="actions-{kind}-value"
					bind:value={formValue}
					rows={3}
					class="font-mono"
					autocomplete="off"
					spellcheck={false}
				/>
			</FormField>
		</div>
		<div class="flex items-center justify-end gap-2 border-t pt-4">
			<Button variant="outline" onclick={closeForm}>Cancel</Button>
			<Button onclick={() => save(kind)} disabled={saving || !nameValid || !formValue}>
				{#if saving}
					<Loader class="animate-spin" />
				{/if}
				{saving
					? 'Saving'
					: editing
						? kind === 'secret'
							? 'Replace secret'
							: 'Save changes'
						: kind === 'secret'
							? 'Add secret'
							: 'Add variable'}
			</Button>
		</div>
	</div>
{/snippet}

<div class="space-y-6">
	<div class="space-y-4">
		<div class="section-header">
			<div class="min-w-0">
				<h2 class="section-title">Secrets</h2>
				<p class="section-subtitle">
					Encrypted values exposed to workflows as secrets.NAME, never shown again.
				</p>
			</div>
			{#if canManage}
				{#if creating === 'secret'}
					<Button variant="outline" class="shrink-0" onclick={closeForm}><X />Cancel</Button>
				{:else}
					<Button class="shrink-0" onclick={() => openCreate('secret')}>
						<Plus />
						New secret
					</Button>
				{/if}
			{/if}
		</div>

		{#if creating === 'secret'}
			<div class="box p-4">
				{@render kvForm('secret')}
			</div>
		{/if}

		{#if !loaded}
			<div class="box divide-y">
				{#each { length: 3 }, i (i)}
					<div class="flex items-center gap-3 px-4 py-3">
						<Skeleton class="h-4 w-40" />
						<Skeleton class="ml-auto h-3 w-16" />
					</div>
				{/each}
			</div>
		{:else if secrets.length === 0}
			{#if creating !== 'secret'}
				<EmptyState
					icon={Key}
					message="No secrets yet"
					description={scope.kind === 'org'
						? 'Repository secrets override these by name.'
						: 'Exposed to workflows as secrets.NAME.'}
				>
					{#snippet actions()}
						{#if canManage}
							<Button variant="outline" onclick={() => openCreate('secret')}>
								<Plus />
								New secret
							</Button>
						{/if}
					{/snippet}
				</EmptyState>
			{/if}
		{:else}
			<div class="data-table">
				<Table>
					<TableHeader>
						<TableRow>
							<TableHead>Name</TableHead>
							<TableHead class="hidden w-32 sm:table-cell">Updated</TableHead>
							{#if canManage}
								<TableHead class="w-24"></TableHead>
							{/if}
						</TableRow>
					</TableHeader>
					<TableBody>
						{#each secrets as secret (secret.name)}
							{#if editing?.kind === 'secret' && editing.name === secret.name}
								<TableRow class="bg-muted/30 hover:[&>td]:bg-transparent">
									<TableCell colspan={3} class="px-4 py-4 whitespace-normal">
										{@render kvForm('secret')}
									</TableCell>
								</TableRow>
							{:else}
								<TableRow>
									<TableCell class="font-mono break-all">{secret.name}</TableCell>
									<TableCell class="hidden text-muted-foreground sm:table-cell">
										{secret.updatedAt ? relativeTime(timestampDate(secret.updatedAt)) : '-'}
									</TableCell>
									{#if canManage}
										<TableCell class="text-right">
											<div class="flex justify-end gap-1">
												<Button
													variant="ghost-muted"
													size="icon-sm"
													onclick={() => openEdit('secret', { name: secret.name })}
													aria-label="Replace {secret.name}"
												>
													<Pencil />
												</Button>
												<Button
													variant="ghost-muted"
													size="icon-sm"
													class="hover:text-destructive"
													onclick={() => openDelete('secret', secret.name)}
													aria-label="Delete {secret.name}"
												>
													<Trash />
												</Button>
											</div>
										</TableCell>
									{/if}
								</TableRow>
							{/if}
						{/each}
					</TableBody>
				</Table>
			</div>
		{/if}
	</div>

	<div class="space-y-4">
		<div class="section-header">
			<div class="min-w-0">
				<h2 class="section-title">Variables</h2>
				<p class="section-subtitle">Plain values exposed to workflows as vars.NAME.</p>
			</div>
			{#if canManage}
				{#if creating === 'variable'}
					<Button variant="outline" class="shrink-0" onclick={closeForm}><X />Cancel</Button>
				{:else}
					<Button variant="outline" class="shrink-0" onclick={() => openCreate('variable')}>
						<Plus />
						New variable
					</Button>
				{/if}
			{/if}
		</div>

		{#if creating === 'variable'}
			<div class="box p-4">
				{@render kvForm('variable')}
			</div>
		{/if}

		{#if !loaded}
			<div class="box divide-y">
				{#each { length: 3 }, i (i)}
					<div class="flex items-center gap-3 px-4 py-3">
						<Skeleton class="h-4 w-40" />
						<Skeleton class="h-4 w-56" />
						<Skeleton class="ml-auto h-3 w-16" />
					</div>
				{/each}
			</div>
		{:else if variables.length === 0}
			{#if creating !== 'variable'}
				<EmptyState
					icon={VariableIcon}
					message="No variables yet"
					description={scope.kind === 'org'
						? 'Repository variables override these by name.'
						: 'Exposed to workflows as vars.NAME.'}
				>
					{#snippet actions()}
						{#if canManage}
							<Button variant="outline" onclick={() => openCreate('variable')}>
								<Plus />
								New variable
							</Button>
						{/if}
					{/snippet}
				</EmptyState>
			{/if}
		{:else}
			<div class="data-table">
				<Table>
					<TableHeader>
						<TableRow>
							<TableHead>Name</TableHead>
							<TableHead>Value</TableHead>
							<TableHead class="hidden w-32 sm:table-cell">Updated</TableHead>
							{#if canManage}
								<TableHead class="w-24"></TableHead>
							{/if}
						</TableRow>
					</TableHeader>
					<TableBody>
						{#each variables as variable (variable.name)}
							{#if editing?.kind === 'variable' && editing.name === variable.name}
								<TableRow class="bg-muted/30 hover:[&>td]:bg-transparent">
									<TableCell colspan={4} class="px-4 py-4 whitespace-normal">
										{@render kvForm('variable')}
									</TableCell>
								</TableRow>
							{:else}
								<TableRow>
									<TableCell class="font-mono break-all">{variable.name}</TableCell>
									<TableCell class="max-w-48 truncate font-mono text-muted-foreground sm:max-w-md"
										>{variable.value}</TableCell
									>
									<TableCell class="hidden text-muted-foreground sm:table-cell">
										{variable.updatedAt ? relativeTime(timestampDate(variable.updatedAt)) : '-'}
									</TableCell>
									{#if canManage}
										<TableCell class="text-right">
											<div class="flex justify-end gap-1">
												<Button
													variant="ghost-muted"
													size="icon-sm"
													onclick={() => openEdit('variable', variable)}
													aria-label="Edit {variable.name}"
												>
													<Pencil />
												</Button>
												<Button
													variant="ghost-muted"
													size="icon-sm"
													class="hover:text-destructive"
													onclick={() => openDelete('variable', variable.name)}
													aria-label="Delete {variable.name}"
												>
													<Trash />
												</Button>
											</div>
										</TableCell>
									{/if}
								</TableRow>
							{/if}
						{/each}
					</TableBody>
				</Table>
			</div>
		{/if}
	</div>
</div>

<ConfirmDialog
	bind:open={deleteOpen}
	title={deleteKind === 'secret' ? 'Delete secret' : 'Delete variable'}
	confirmLabel="Delete"
	onConfirm={confirmDelete}
	loading={deleting}
	icon={Trash}
>
	{#snippet description()}
		Delete <strong class="font-mono">{deleteName}</strong>? Workflows referencing it fail at the
		next run.
	{/snippet}
</ConfirmDialog>

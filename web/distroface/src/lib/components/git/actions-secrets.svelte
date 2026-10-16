<script lang="ts">
	import { onMount } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import {
		Table, TableBody, TableCell, TableHead, TableHeader, TableRow
	} from '$lib/components/ui/table';
	import FormPanel from '$lib/components/form-panel.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import { KeyRound, Variable as VariableIcon, Plus, Pencil, Trash2 } from '@lucide/svelte';
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
	let editorOpen = $state(false);
	let editorKind = $state<Kind>('secret');
	let editingExisting = $state(false);
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

	function openEditor(kind: Kind, existing?: { name: string; value?: string }) {
		editorKind = kind;
		editingExisting = !!existing;
		formName = existing?.name ?? '';
		formValue = existing?.value ?? '';
		editorOpen = true;
	}

	async function save() {
		if (!nameValid) return;
		saving = true;
		try {
			const name = formName.trim().toUpperCase();
			if (editorKind === 'secret') {
				await rpcClient.actions.setSecret({ scope, secretName: name, value: formValue });
				toast.success('Secret saved');
			} else {
				await rpcClient.actions.setVariable({ scope, variableName: name, value: formValue });
				toast.success('Variable saved');
			}
			editorOpen = false;
			await load();
		} catch {
			// error interceptor
		} finally {
			saving = false;
		}
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

<div class="space-y-8">
	<div class="space-y-4">
		<div class="section-header">
			<div class="min-w-0">
				<h2 class="section-title">Secrets</h2>
				<p class="section-subtitle">Encrypted values exposed to workflows as secrets.NAME, never shown again.</p>
			</div>
			{#if canManage}
				<Button size="sm" class="shrink-0" onclick={() => openEditor('secret')}>
					<Plus class="h-4 w-4 mr-1.5" />
					New Secret
				</Button>
			{/if}
		</div>

		{#if !loaded}
			<Skeleton class="h-24 w-full rounded-lg" />
		{:else if secrets.length === 0}
			<EmptyState icon={KeyRound} message="No secrets" description="Repository secrets override these by name." />
		{:else}
			<div class="data-table">
				<Table>
					<TableHeader>
						<TableRow class="bg-muted/30 hover:bg-muted/30">
							<TableHead class="th">Name</TableHead>
							<TableHead class="th w-32 hidden sm:table-cell">Updated</TableHead>
							{#if canManage}
								<TableHead class="th w-24"></TableHead>
							{/if}
						</TableRow>
					</TableHeader>
					<TableBody>
						{#each secrets as secret (secret.name)}
							<TableRow>
								<TableCell class="font-mono text-sm py-3 px-3 break-all">{secret.name}</TableCell>
								<TableCell class="text-sm text-muted-foreground py-3 px-3 hidden sm:table-cell">
									{secret.updatedAt ? relativeTime(timestampDate(secret.updatedAt)) : '-'}
								</TableCell>
								{#if canManage}
									<TableCell class="text-right py-3 px-3">
										<div class="flex justify-end gap-1">
											<Button variant="ghost" size="icon" class="h-7 w-7" onclick={() => openEditor('secret', { name: secret.name })} aria-label="Replace">
												<Pencil class="h-3.5 w-3.5" />
											</Button>
											<Button
												variant="ghost" size="icon"
												class="h-7 w-7 text-destructive hover:text-destructive"
												onclick={() => openDelete('secret', secret.name)}
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
				<Button size="sm" variant="outline" class="shrink-0" onclick={() => openEditor('variable')}>
					<Plus class="h-4 w-4 mr-1.5" />
					New Variable
				</Button>
			{/if}
		</div>

		{#if !loaded}
			<Skeleton class="h-24 w-full rounded-lg" />
		{:else if variables.length === 0}
			<EmptyState icon={VariableIcon} message="No variables" description="Repository variables override these by name." />
		{:else}
			<div class="data-table">
				<Table>
					<TableHeader>
						<TableRow class="bg-muted/30 hover:bg-muted/30">
							<TableHead class="th">Name</TableHead>
							<TableHead class="th">Value</TableHead>
							<TableHead class="th w-32 hidden sm:table-cell">Updated</TableHead>
							{#if canManage}
								<TableHead class="th w-24"></TableHead>
							{/if}
						</TableRow>
					</TableHeader>
					<TableBody>
						{#each variables as variable (variable.name)}
							<TableRow>
								<TableCell class="font-mono text-sm py-3 px-3 break-all">{variable.name}</TableCell>
								<TableCell class="font-mono text-[13px] text-muted-foreground py-3 px-3 max-w-[12rem] sm:max-w-md truncate">{variable.value}</TableCell>
								<TableCell class="text-sm text-muted-foreground py-3 px-3 hidden sm:table-cell">
									{variable.updatedAt ? relativeTime(timestampDate(variable.updatedAt)) : '-'}
								</TableCell>
								{#if canManage}
									<TableCell class="text-right py-3 px-3">
										<div class="flex justify-end gap-1">
											<Button variant="ghost" size="icon" class="h-7 w-7" onclick={() => openEditor('variable', variable)} aria-label="Edit">
												<Pencil class="h-3.5 w-3.5" />
											</Button>
											<Button
												variant="ghost" size="icon"
												class="h-7 w-7 text-destructive hover:text-destructive"
												onclick={() => openDelete('variable', variable.name)}
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
			</div>
		{/if}
	</div>
</div>

<FormPanel
	bind:open={editorOpen}
	title={editorKind === 'secret' ? (editingExisting ? 'Replace Secret' : 'New Secret') : (editingExisting ? 'Edit Variable' : 'New Variable')}
	description={editorKind === 'secret' ? 'The value is sealed on save and cannot be read back.' : 'Visible to anyone who can read workflow runs.'}
	icon={editorKind === 'secret' ? KeyRound : VariableIcon}
>
	<FormSection title={editorKind === 'secret' ? 'Secret' : 'Variable'}>
		<div class="space-y-3">
			<FormField label="Name" id="actions-kv-name" required help="Letters, digits, underscores, stored upper case" error={formName && !nameValid ? 'Invalid name' : undefined}>
				<Input id="actions-kv-name" bind:value={formName} placeholder="NPM_TOKEN" class="font-mono" disabled={editingExisting} />
			</FormField>
			<FormField label="Value" id="actions-kv-value" required>
				<Textarea id="actions-kv-value" bind:value={formValue} rows={4} class="font-mono" autocomplete="off" spellcheck={false} />
			</FormField>
		</div>
	</FormSection>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (editorOpen = false)}>Cancel</Button>
		<Button onclick={save} disabled={saving || !nameValid || !formValue}>
			{saving ? 'Saving...' : 'Save'}
		</Button>
	{/snippet}
</FormPanel>

<ConfirmDialog bind:open={deleteOpen} title={deleteKind === 'secret' ? 'Delete Secret' : 'Delete Variable'} confirmLabel="Delete" onConfirm={confirmDelete} loading={deleting} icon={Trash2}>
	{#snippet description()}
		Delete <strong class="font-mono">{deleteName}</strong>? Workflows referencing it fail at the next run.
	{/snippet}
</ConfirmDialog>

<script lang="ts">
	import { page } from '$app/state';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { toast } from 'svelte-sonner';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Input } from '$lib/components/ui/input';
	import { Switch } from '$lib/components/ui/switch';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import FormPanel from '$lib/components/form-panel.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import PrincipalPicker from '$lib/components/git/principal-picker.svelte';
	import { Tag, Plus, Pencil, Trash2 } from '@lucide/svelte';
	import type { GitTagProtection } from '$lib/proto/distroface/v1/git_pb';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);

	let rules = $state<GitTagProtection[]>([]);
	let loaded = $state(false);
	let orgId = $state('');
	let loadedFor = $state('');

	let panelOpen = $state(false);
	let form = $state({ id: '', pattern: '', blockDeletion: true, allowedPushers: [] as string[] });
	let saving = $state(false);

	let deleteOpen = $state(false);
	let deleteTarget = $state<GitTagProtection | null>(null);
	let deleting = $state(false);

	$effect(() => {
		if (repo && repo.fullName !== loadedFor) {
			loadedFor = repo.fullName;
			void load();
			if (repo.isOrgNamespace) void loadOrg();
		}
	});

	async function loadOrg() {
		try {
			const resp = await rpcClient.organization.getOrganization({ name: namespace }, silentCallOptions);
			orgId = resp.organization?.id ?? '';
		} catch {
			orgId = '';
		}
	}

	async function load() {
		try {
			const resp = await rpcClient.gitRefs.listTagProtections({ namespace, name }, silentCallOptions);
			rules = resp.protections;
		} catch {
			rules = [];
		} finally {
			loaded = true;
		}
	}

	function openCreate() {
		form = { id: '', pattern: 'v*', blockDeletion: true, allowedPushers: [] };
		panelOpen = true;
	}

	function openEdit(p: GitTagProtection) {
		form = { id: p.id, pattern: p.pattern, blockDeletion: p.blockDeletion, allowedPushers: [...p.allowedPushers] };
		panelOpen = true;
	}

	async function save() {
		if (!form.pattern.trim()) return;
		saving = true;
		try {
			await rpcClient.gitRefs.setTagProtection({
				namespace, name,
				protection: { ...form, pattern: form.pattern.trim() }
			});
			toast.success(form.id ? 'Rule updated' : 'Rule created');
			panelOpen = false;
			await load();
		} catch {
			// Error interceptor already toasted
		} finally {
			saving = false;
		}
	}

	async function remove() {
		if (!deleteTarget) return;
		deleting = true;
		try {
			await rpcClient.gitRefs.deleteTagProtection({ namespace, name, id: deleteTarget.id });
			toast.success('Rule deleted');
			deleteOpen = false;
			await load();
		} catch {
			// Error interceptor already toasted
		} finally {
			deleting = false;
		}
	}
</script>

<div class="space-y-4">
	<div class="section-header">
		<div class="min-w-0">
			<h2 class="section-title">Tag protection</h2>
			<p class="section-subtitle">Limit who can create matching tags and whether they can be deleted.</p>
		</div>
		<Button size="sm" class="shrink-0" onclick={openCreate} disabled={!repo}>
			<Plus class="h-4 w-4 mr-1.5" />Add Rule
		</Button>
	</div>

	{#if !loaded}
		<Skeleton class="h-40 w-full rounded-xl" />
	{:else if rules.length === 0}
		<EmptyState icon={Tag} message="No tag rules" description="Protect release tags from accidental deletion or unauthorized pushes.">
			{#snippet actions()}
				<Button variant="outline" size="sm" onclick={openCreate}>
					<Plus class="h-4 w-4 mr-1.5" />Add Rule
				</Button>
			{/snippet}
		</EmptyState>
	{:else}
		<div class="rounded-xl border border-border/60 divide-y divide-border/40 overflow-hidden">
			{#each rules as rule (rule.id)}
				<div class="flex items-start gap-4 px-5 py-4">
					<Tag class="h-4 w-4 text-primary mt-0.5 shrink-0" />
					<div class="flex-1 min-w-0 space-y-1.5">
						<code class="font-mono text-sm font-medium break-all">{rule.pattern}</code>
						<div class="flex flex-wrap gap-1">
							{#if rule.blockDeletion}
								<Badge variant="secondary" class="text-[11px] font-normal">No deletion</Badge>
							{/if}
							<Badge variant="secondary" class="text-[11px] font-normal">
								{rule.allowedPushers.length > 0 ? `${rule.allowedPushers.length} allowed pusher${rule.allowedPushers.length === 1 ? '' : 's'}` : 'Any writer may push'}
							</Badge>
						</div>
					</div>
					<div class="flex items-center gap-1 shrink-0">
						<Button variant="ghost" size="icon" class="h-7 w-7" aria-label="Edit rule" onclick={() => openEdit(rule)}>
							<Pencil class="h-3.5 w-3.5" />
						</Button>
						<Button
							variant="ghost" size="icon" class="h-7 w-7 text-destructive hover:text-destructive"
							aria-label="Delete rule"
							onclick={() => { deleteTarget = rule; deleteOpen = true; }}
						>
							<Trash2 class="h-3.5 w-3.5" />
						</Button>
					</div>
				</div>
			{/each}
		</div>
	{/if}
</div>

<FormPanel bind:open={panelOpen} title={form.id ? 'Edit Tag Rule' : 'New Tag Rule'} description="Checked on every tag push." icon={Tag}>
	<div class="space-y-3">
		<FormField label="Tag pattern" id="tag-pattern" required help="Glob over tag names, v* or release-*">
			<Input id="tag-pattern" bind:value={form.pattern} placeholder="v*" class="font-mono text-sm" />
		</FormField>
		<FormField label="Block deletion" horizontal>
			<Switch bind:checked={form.blockDeletion} />
		</FormField>
		<FormField label="Allowed pushers" help="Empty allows every writer">
			<PrincipalPicker bind:selected={form.allowedPushers} {orgId} />
		</FormField>
	</div>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (panelOpen = false)}>Cancel</Button>
		<Button onclick={save} disabled={saving || !form.pattern.trim()}>{saving ? 'Saving...' : form.id ? 'Save Changes' : 'Create Rule'}</Button>
	{/snippet}
</FormPanel>

<ConfirmDialog bind:open={deleteOpen} title="Delete Rule" confirmLabel="Delete" onConfirm={remove} loading={deleting} icon={Trash2}>
	{#snippet description()}
		Tags matching <strong class="font-mono">{deleteTarget?.pattern}</strong> lose this protection.
	{/snippet}
</ConfirmDialog>

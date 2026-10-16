<script lang="ts">
	import { page } from '$app/state';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { toast } from 'svelte-sonner';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import FormPanel from '$lib/components/form-panel.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import ProtectionForm, { emptyProtection, protectionFrom, protectionInit, type ProtectionForm as ProtectionFormValues } from '$lib/components/git/protection-form.svelte';
	import { Shield, Plus, Pencil, Trash2 } from '@lucide/svelte';
	import type { GitBranchProtection } from '$lib/proto/distroface/v1/git_pb';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);

	let rules = $state<GitBranchProtection[]>([]);
	let loaded = $state(false);
	let orgId = $state('');
	let loadedFor = $state('');

	let panelOpen = $state(false);
	let form = $state<ProtectionFormValues>(emptyProtection());
	let saving = $state(false);

	let deleteOpen = $state(false);
	let deleteTarget = $state<GitBranchProtection | null>(null);
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
			const resp = await rpcClient.gitRefs.listBranchProtections({ namespace, name }, silentCallOptions);
			rules = resp.protections;
		} catch {
			rules = [];
		} finally {
			loaded = true;
		}
	}

	function openCreate() {
		form = emptyProtection();
		form.pattern = rules.length === 0 ? (repo?.defaultBranch ?? '') : '';
		panelOpen = true;
	}

	function openEdit(p: GitBranchProtection) {
		form = protectionFrom(p);
		panelOpen = true;
	}

	async function save() {
		if (!form.pattern.trim()) return;
		saving = true;
		try {
			await rpcClient.gitRefs.setBranchProtection({ namespace, name, protection: protectionInit(form) });
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
			await rpcClient.gitRefs.deleteBranchProtection({ namespace, name, id: deleteTarget.id });
			toast.success('Rule deleted');
			deleteOpen = false;
			await load();
		} catch {
			// Error interceptor already toasted
		} finally {
			deleting = false;
		}
	}

	function flags(p: GitBranchProtection): string[] {
		const out: string[] = [];
		if (p.blockForcePush) out.push('No force push');
		if (p.blockDeletion) out.push('No deletion');
		if (p.requirePullRequest) out.push(p.requiredApprovals > 0 ? `PR, ${p.requiredApprovals} approval${p.requiredApprovals === 1 ? '' : 's'}` : 'PR required');
		if (p.dismissStaleApprovals) out.push('Dismiss stale');
		if (p.requireCodeOwnerReview) out.push('Code owners');
		if (p.requireStatusChecks) out.push(p.statusCheckContexts.length > 0 ? `${p.statusCheckContexts.length} check${p.statusCheckContexts.length === 1 ? '' : 's'}` : 'Status checks');
		if (p.requireSignedCommits) out.push('Signed');
		if (p.requireLinearHistory) out.push('Linear');
		if (p.requireUpToDate) out.push('Up to date');
		if (p.allowedPushers.length > 0) out.push(`${p.allowedPushers.length} pusher${p.allowedPushers.length === 1 ? '' : 's'}`);
		if (p.allowedMergers.length > 0) out.push(`${p.allowedMergers.length} merger${p.allowedMergers.length === 1 ? '' : 's'}`);
		if (p.bypassIds.length > 0) out.push(`${p.bypassIds.length} bypass`);
		return out;
	}
</script>

<div class="space-y-4">
	<div class="section-header">
		<div class="min-w-0">
			<h2 class="section-title">Branch protection</h2>
			<p class="section-subtitle">Rules match branch names by glob, higher priority wins on overlap.</p>
		</div>
		<Button size="sm" class="shrink-0" onclick={openCreate} disabled={!repo}>
			<Plus class="h-4 w-4 mr-1.5" />Add Rule
		</Button>
	</div>

	{#if !loaded}
		<Skeleton class="h-40 w-full rounded-xl" />
	{:else if rules.length === 0}
		<EmptyState icon={Shield} message="No protection rules" description="Block force pushes, require pull requests, and gate merges on checks.">
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
					<Shield class="h-4 w-4 text-primary mt-0.5 shrink-0" />
					<div class="flex-1 min-w-0 space-y-1.5">
						<div class="flex items-center gap-2 flex-wrap">
							<code class="font-mono text-sm font-medium break-all">{rule.pattern}</code>
							{#if rule.priority > 0}
								<span class="text-xs text-muted-foreground tabular-nums">priority {rule.priority}</span>
							{/if}
						</div>
						<div class="flex flex-wrap gap-1">
							{#each flags(rule) as f (f)}
								<Badge variant="secondary" class="text-[11px] font-normal">{f}</Badge>
							{:else}
								<span class="text-xs text-muted-foreground">No restrictions</span>
							{/each}
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

<FormPanel bind:open={panelOpen} title={form.id ? 'Edit Rule' : 'New Protection Rule'} description="Applied on push and on merge." icon={Shield} wide>
	<ProtectionForm bind:form {orgId} />
	{#snippet footer()}
		<Button variant="outline" onclick={() => (panelOpen = false)}>Cancel</Button>
		<Button onclick={save} disabled={saving || !form.pattern.trim()}>{saving ? 'Saving...' : form.id ? 'Save Changes' : 'Create Rule'}</Button>
	{/snippet}
</FormPanel>

<ConfirmDialog bind:open={deleteOpen} title="Delete Rule" confirmLabel="Delete" onConfirm={remove} loading={deleting} icon={Trash2}>
	{#snippet description()}
		Branches matching <strong class="font-mono">{deleteTarget?.pattern}</strong> lose this protection.
	{/snippet}
</ConfirmDialog>

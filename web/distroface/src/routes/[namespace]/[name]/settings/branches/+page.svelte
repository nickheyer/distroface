<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { toast } from 'svelte-sonner';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import { Shield, Plus, Pencil, Trash } from '$lib/icons';
	import type { GitBranchProtection } from '$lib/proto/distroface/v1/git_pb';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);
	const newHref = $derived(
		resolve('/[namespace]/[name]/settings/branches/new', { namespace, name })
	);

	let rules = $state<GitBranchProtection[]>([]);
	let loaded = $state(false);
	let loadedFor = $state('');

	let deleteOpen = $state(false);
	let deleteTarget = $state<GitBranchProtection | null>(null);
	let deleting = $state(false);

	$effect(() => {
		if (repo && repo.fullName !== loadedFor) {
			loadedFor = repo.fullName;
			void load();
		}
	});

	async function load() {
		try {
			const resp = await rpcClient.gitRefs.listBranchProtections(
				{ namespace, name },
				silentCallOptions
			);
			rules = resp.protections;
		} catch {
			rules = [];
		} finally {
			loaded = true;
		}
	}

	function editHref(rule: GitBranchProtection) {
		return resolve('/[namespace]/[name]/settings/branches/[id]', { namespace, name, id: rule.id });
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
		if (p.requirePullRequest)
			out.push(
				p.requiredApprovals > 0
					? `PR, ${p.requiredApprovals} approval${p.requiredApprovals === 1 ? '' : 's'}`
					: 'PR required'
			);
		if (p.dismissStaleApprovals) out.push('Dismiss stale');
		if (p.requireCodeOwnerReview) out.push('Code owners');
		if (p.requireStatusChecks)
			out.push(
				p.statusCheckContexts.length > 0
					? `${p.statusCheckContexts.length} check${p.statusCheckContexts.length === 1 ? '' : 's'}`
					: 'Status checks'
			);
		if (p.requireSignedCommits) out.push('Signed');
		if (p.requireLinearHistory) out.push('Linear');
		if (p.requireUpToDate) out.push('Up to date');
		if (p.allowedPushers.length > 0)
			out.push(`${p.allowedPushers.length} pusher${p.allowedPushers.length === 1 ? '' : 's'}`);
		if (p.allowedMergers.length > 0)
			out.push(`${p.allowedMergers.length} merger${p.allowedMergers.length === 1 ? '' : 's'}`);
		if (p.bypassIds.length > 0) out.push(`${p.bypassIds.length} bypass`);
		return out;
	}
</script>

<div class="space-y-6">
	<div class="section-header">
		<div class="min-w-0">
			<h2 class="section-title">Branch protection</h2>
			<p class="section-subtitle">
				Rules match branch names by glob, higher priority wins on overlap.
			</p>
		</div>
		<Button class="shrink-0" href={newHref} disabled={!repo}>
			<Plus />Add rule
		</Button>
	</div>

	{#if !loaded}
		<div class="box divide-y">
			{#each { length: 2 }, i (i)}
				<div class="space-y-2 px-4 py-3">
					<Skeleton class="h-4 w-40" />
					<Skeleton class="h-3 w-64" />
				</div>
			{/each}
		</div>
	{:else if rules.length === 0}
		<EmptyState
			icon={Shield}
			message="No protection rules"
			description="Block force pushes, require pull requests, and gate merges on checks."
		>
			{#snippet actions()}
				<Button variant="outline" href={newHref}>
					<Plus />Add rule
				</Button>
			{/snippet}
		</EmptyState>
	{:else}
		<div class="box divide-y">
			{#each rules as rule (rule.id)}
				<div class="flex items-start gap-3 px-4 py-3">
					<Shield class="mt-0.5 size-4 shrink-0 text-muted-foreground" />
					<div class="min-w-0 flex-1 space-y-1.5">
						<div class="flex flex-wrap items-center gap-2">
							<code class="font-mono text-sm font-medium break-all">{rule.pattern}</code>
							{#if rule.priority > 0}
								<span class="text-xs text-muted-foreground tabular-nums"
									>priority {rule.priority}</span
								>
							{/if}
						</div>
						<div class="flex flex-wrap gap-1">
							{#each flags(rule) as f (f)}
								<Badge variant="secondary">{f}</Badge>
							{:else}
								<span class="text-xs text-muted-foreground">No restrictions</span>
							{/each}
						</div>
					</div>
					<div class="flex shrink-0 items-center gap-1">
						<Button
							variant="ghost-muted"
							size="icon-sm"
							aria-label="Edit rule"
							href={editHref(rule)}
						>
							<Pencil />
						</Button>
						<Button
							variant="ghost-muted"
							size="icon-sm"
							class="text-destructive hover:text-destructive"
							aria-label="Delete rule"
							onclick={() => {
								deleteTarget = rule;
								deleteOpen = true;
							}}
						>
							<Trash />
						</Button>
					</div>
				</div>
			{/each}
		</div>
	{/if}
</div>

<ConfirmDialog
	bind:open={deleteOpen}
	title="Delete rule"
	confirmLabel="Delete"
	onConfirm={remove}
	loading={deleting}
	icon={Trash}
>
	{#snippet description()}
		Branches matching <strong class="font-mono">{deleteTarget?.pattern}</strong> lose this protection.
	{/snippet}
</ConfirmDialog>

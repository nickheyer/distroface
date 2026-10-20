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
	import { Tag, Plus, Pencil, Trash } from '$lib/icons';
	import type { GitTagProtection } from '$lib/proto/distroface/v1/git_pb';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);
	const newHref = $derived(resolve('/[namespace]/[name]/settings/tags/new', { namespace, name }));

	let rules = $state<GitTagProtection[]>([]);
	let loaded = $state(false);
	let loadedFor = $state('');

	let deleteOpen = $state(false);
	let deleteTarget = $state<GitTagProtection | null>(null);
	let deleting = $state(false);

	$effect(() => {
		if (repo && repo.fullName !== loadedFor) {
			loadedFor = repo.fullName;
			void load();
		}
	});

	async function load() {
		try {
			const resp = await rpcClient.gitRefs.listTagProtections(
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

	function editHref(rule: GitTagProtection) {
		return resolve('/[namespace]/[name]/settings/tags/[id]', { namespace, name, id: rule.id });
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

<div class="space-y-6">
	<div class="section-header">
		<div class="min-w-0">
			<h2 class="section-title">Tag protection</h2>
			<p class="section-subtitle">
				Limit who can create matching tags and whether they can be deleted.
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
			icon={Tag}
			message="No tag rules"
			description="Protect release tags from accidental deletion or unauthorized pushes."
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
					<Tag class="mt-0.5 size-4 shrink-0 text-muted-foreground" />
					<div class="min-w-0 flex-1 space-y-1.5">
						<code class="font-mono text-sm font-medium break-all">{rule.pattern}</code>
						<div class="flex flex-wrap gap-1">
							{#if rule.blockDeletion}
								<Badge variant="secondary">No deletion</Badge>
							{/if}
							<Badge variant="secondary">
								{rule.allowedPushers.length > 0
									? `${rule.allowedPushers.length} allowed pusher${rule.allowedPushers.length === 1 ? '' : 's'}`
									: 'Any writer may push'}
							</Badge>
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
		Tags matching <strong class="font-mono">{deleteTarget?.pattern}</strong> lose this protection.
	{/snippet}
</ConfirmDialog>

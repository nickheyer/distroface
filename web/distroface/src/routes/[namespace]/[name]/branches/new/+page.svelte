<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { GitBranch, Loader2 } from '@lucide/svelte';
	import { toast } from 'svelte-sonner';
	import { rpcClient } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import EmptyState from '$lib/components/empty-state.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import { GitUnit } from '$lib/proto/distroface/v1/git_pb';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);
	const canWrite = $derived(!!repo && gitRepoStore.canWrite(GitUnit.CODE) && !repo.isArchived);
	const listHref = $derived(resolve('/[namespace]/[name]/branches', { namespace, name }));

	let branch = $state(page.url.searchParams.get('name') ?? '');
	let from = $state(page.url.searchParams.get('from') ?? '');
	let seededFor = $state('');
	let creating = $state(false);

	$effect(() => {
		if (repo && repo.fullName !== seededFor) {
			seededFor = repo.fullName;
			if (!from) from = repo.defaultBranch;
		}
	});

	async function createBranch() {
		if (!branch.trim() || creating) return;
		creating = true;
		try {
			await rpcClient.gitRefs.createBranch({
				namespace,
				name,
				branch: branch.trim(),
				from: from.trim()
			});
			toast.success('Branch created');
			gitRepoStore.reload();
			await goto(resolve('/[namespace]/[name]/branches', { namespace, name }));
		} catch {
			// Error interceptor already toasted
		} finally {
			creating = false;
		}
	}
</script>

<svelte:head>
	<title>New branch - {namespace}/{name}</title>
</svelte:head>

{#if !repo}
	<Skeleton class="h-72 w-full rounded-lg" />
{:else if !canWrite}
	<EmptyState
		icon={GitBranch}
		message="You cannot create branches"
		description={repo.isArchived
			? 'This repository is archived.'
			: 'Write access to code is required.'}
	>
		{#snippet actions()}
			<Button variant="outline" href={listHref}>Back to branches</Button>
		{/snippet}
	</EmptyState>
{:else}
	<FormPage
		title="New branch"
		description="Starts from a branch, tag, or commit in {namespace}/{name}."
		backHref={listHref}
		backLabel="Branches"
	>
		<FormSection title="Branch" description="The new ref and where it starts.">
			<FormField label="Branch name" id="new-branch-name" required span="half">
				<Input
					id="new-branch-name"
					bind:value={branch}
					placeholder="feature/my-change"
					class="font-mono"
					autocomplete="off"
					spellcheck="false"
				/>
			</FormField>
			<FormField
				label="Create from"
				id="new-branch-from"
				help="Branch, tag, or commit sha"
				span="half"
			>
				<Input id="new-branch-from" bind:value={from} class="font-mono" spellcheck="false" />
			</FormField>
		</FormSection>

		{#snippet footer()}
			<Button onclick={createBranch} disabled={creating || !branch.trim()}>
				{#if creating}<Loader2 class="animate-spin" />{/if}
				{creating ? 'Creating' : 'Create branch'}
			</Button>
			<Button variant="ghost-muted" href={listHref}>Cancel</Button>
		{/snippet}
	</FormPage>
{/if}

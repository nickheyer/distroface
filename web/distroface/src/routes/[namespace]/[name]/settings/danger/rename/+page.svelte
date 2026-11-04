<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { Loader, Pencil } from '$lib/icons';
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

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);
	const dangerHref = $derived(resolve('/[namespace]/[name]/settings/danger', { namespace, name }));

	let newName = $state('');
	let seededFor = $state('');
	let renaming = $state(false);

	$effect(() => {
		if (repo && repo.fullName !== seededFor) {
			seededFor = repo.fullName;
			newName = repo.name;
		}
	});

	const nameValid = $derived(
		/^[A-Za-z0-9][A-Za-z0-9._-]*$/.test(newName.trim()) && newName.trim() !== name
	);

	async function rename() {
		if (!nameValid || renaming) return;
		renaming = true;
		try {
			const resp = await rpcClient.git.moveGitRepository({
				namespace,
				name,
				newName: newName.trim()
			});
			toast.success('Repository renamed');
			await goto(
				resolve('/[namespace]/[name]/settings/danger', {
					namespace: resp.repository?.namespace ?? namespace,
					name: resp.repository?.name ?? newName.trim()
				})
			);
		} catch {
			// Error interceptor already toasted
		} finally {
			renaming = false;
		}
	}
</script>

<svelte:head>
	<title>Rename repository - {namespace}/{name}</title>
</svelte:head>

{#if !repo}
	<Skeleton class="h-72 w-full rounded-lg" />
{:else if !gitRepoStore.isOwner}
	<EmptyState
		icon={Pencil}
		message="You cannot rename this repository"
		description="Only the repository owner can rename it."
	>
		{#snippet actions()}
			<Button variant="outline" href={dangerHref}>Back to danger zone</Button>
		{/snippet}
	</EmptyState>
{:else}
	<FormPage
		level="section"
		title="Rename repository"
		description="Linked packages move with it. Old clone urls redirect until the name is reused."
		backHref={dangerHref}
		backLabel="Danger zone"
	>
		<FormSection title="New name" description="Update remotes afterwards, redirects are not kept.">
			<FormField
				label="Repository name"
				id="rename-name"
				required
				help="Letters, digits, dots, dashes, and underscores"
				error={newName.trim() && !nameValid && newName.trim() !== name
					? 'Letters, digits, dots, dashes, and underscores only'
					: ''}
				span="half"
			>
				<Input
					id="rename-name"
					bind:value={newName}
					class="font-mono"
					autocomplete="off"
					spellcheck="false"
				/>
			</FormField>
		</FormSection>

		{#snippet footer()}
			<Button variant="destructive" onclick={rename} disabled={renaming || !nameValid}>
				{#if renaming}<Loader class="animate-spin" />{/if}
				{renaming ? 'Renaming' : 'Rename repository'}
			</Button>
			<Button variant="ghost-muted" href={dangerHref}>Cancel</Button>
		{/snippet}
	</FormPage>
{/if}

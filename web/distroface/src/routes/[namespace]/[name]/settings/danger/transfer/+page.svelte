<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { ArrowRightLeft, Loader2 } from '@lucide/svelte';
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

	let newNamespace = $state('');
	let transferring = $state(false);

	const canSubmit = $derived(!!newNamespace.trim() && newNamespace.trim() !== namespace);

	async function transfer() {
		if (!canSubmit || transferring) return;
		const ns = newNamespace.trim();
		transferring = true;
		try {
			const resp = await rpcClient.git.moveGitRepository({ namespace, name, newNamespace: ns });
			if (resp.transfer) {
				toast.success(`Transfer to ${ns} is waiting for acceptance`);
				await goto(resolve('/[namespace]/[name]/settings/danger', { namespace, name }));
			} else {
				toast.success(`Moved to ${ns}`);
				await goto(
					resolve('/[namespace]/[name]/settings/danger', {
						namespace: resp.repository?.namespace ?? ns,
						name: resp.repository?.name ?? name
					})
				);
			}
		} catch {
			// Error interceptor already toasted
		} finally {
			transferring = false;
		}
	}
</script>

<svelte:head>
	<title>Transfer repository - {namespace}/{name}</title>
</svelte:head>

{#if !repo}
	<Skeleton class="h-72 w-full rounded-lg" />
{:else if !gitRepoStore.isOwner}
	<EmptyState
		icon={ArrowRightLeft}
		message="You cannot transfer this repository"
		description="Only the repository owner can move it."
	>
		{#snippet actions()}
			<Button variant="outline" href={dangerHref}>Back to danger zone</Button>
		{/snippet}
	</EmptyState>
{:else}
	<FormPage
		level="section"
		title="Transfer repository"
		description="Collaborators and settings travel with it. Users must accept, organizations you administer move at once."
		backHref={dangerHref}
		backLabel="Danger zone"
	>
		<FormSection title="Destination" description="A username or organization name.">
			<FormField label="New namespace" id="transfer-ns" required span="half">
				<Input
					id="transfer-ns"
					bind:value={newNamespace}
					placeholder="org-or-user"
					class="font-mono"
					autocomplete="off"
					spellcheck="false"
				/>
			</FormField>
		</FormSection>

		{#snippet footer()}
			<Button variant="destructive" onclick={transfer} disabled={transferring || !canSubmit}>
				{#if transferring}<Loader2 class="animate-spin" />{/if}
				{transferring ? 'Transferring' : 'Transfer repository'}
			</Button>
			<Button variant="ghost-muted" href={dangerHref}>Cancel</Button>
		{/snippet}
	</FormPage>
{/if}

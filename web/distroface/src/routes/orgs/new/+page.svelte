<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { Building, Loader } from '$lib/icons';
	import { toast } from 'svelte-sonner';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import EmptyState from '$lib/components/empty-state.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';

	let orgName = $state('');
	let orgDisplayName = $state('');
	let orgDescription = $state('');
	let creating = $state(false);

	const listHref = resolve('/orgs');
	const canCreate = $derived(authStore.hasPermission('organizations', 'create'));
	const nameValid = $derived(/^[a-z0-9][a-z0-9._-]{0,62}$/.test(orgName.trim().toLowerCase()));

	async function createOrg() {
		if (!nameValid || creating) return;
		creating = true;
		const name = orgName.trim().toLowerCase();
		try {
			await rpcClient.organization.createOrganization({
				name,
				displayName: orgDisplayName.trim(),
				description: orgDescription.trim()
			});
			toast.success('Organization created');
			await goto(resolve('/orgs/[name]', { name }));
		} catch {
			// Error interceptor already toasted
		} finally {
			creating = false;
		}
	}
</script>

<svelte:head>
	<title>New organization</title>
</svelte:head>

{#if !canCreate}
	<EmptyState
		icon={Building}
		message="You cannot create organizations"
		description="Ask an administrator for the organizations permission."
	>
		{#snippet actions()}
			<Button variant="outline" href={listHref}>Back to organizations</Button>
		{/snippet}
	</EmptyState>
{:else}
	<FormPage
		title="Create a new organization"
		description="A shared namespace where a team publishes images, artifacts, and repositories together."
		backHref={listHref}
		backLabel="Organizations"
	>
		<FormSection title="Organization" description="Namespace, display name, and purpose.">
			<FormField
				label="Name"
				id="org-name"
				required
				help="Lowercase, becomes the push namespace, like registry.example.com/my-team/app"
				error={orgName && !nameValid ? 'Lowercase letters, digits, dots, dashes, underscores' : ''}
				span="half"
			>
				<Input
					id="org-name"
					bind:value={orgName}
					placeholder="my-team"
					class="font-mono"
					autocomplete="off"
					spellcheck="false"
					aria-invalid={!!orgName && !nameValid}
				/>
			</FormField>
			<FormField
				label="Display name"
				id="org-display"
				help="Shown in place of the name wherever there is room"
				span="half"
			>
				<Input id="org-display" bind:value={orgDisplayName} placeholder="My Team" />
			</FormField>
			<FormField label="Description" id="org-desc">
				<Textarea
					id="org-desc"
					bind:value={orgDescription}
					placeholder="What does this organization do?"
					rows={3}
				/>
			</FormField>
		</FormSection>

		{#snippet footer()}
			<Button onclick={createOrg} disabled={creating || !nameValid}>
				{#if creating}<Loader class="animate-spin" />{/if}
				{creating ? 'Creating' : 'Create organization'}
			</Button>
			<Button variant="ghost-muted" href={listHref}>Cancel</Button>
		{/snippet}
	</FormPage>
{/if}

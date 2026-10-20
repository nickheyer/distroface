<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { Key, Loader2 } from '@lucide/svelte';
	import { toast } from 'svelte-sonner';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Switch } from '$lib/components/ui/switch';
	import EmptyState from '$lib/components/empty-state.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';

	let name = $state('');
	let description = $state('');
	let isDefault = $state(false);
	let creating = $state(false);

	const listHref = resolve('/admin/roles');
	const canCreate = $derived(authStore.hasPermission('roles', 'create'));

	async function createRole() {
		if (!name.trim() || creating) return;
		creating = true;
		let createdId = '';
		try {
			const resp = await rpcClient.role.createRole({
				name: name.trim().toLowerCase(),
				description: description.trim(),
				isDefault,
				permissions: []
			});
			createdId = resp.role?.id ?? '';
		} catch {
			creating = false;
			return;
		}
		toast.success('Role created');
		if (createdId && authStore.canUpdateRoles) {
			await goto(resolve('/admin/roles/[id]', { id: createdId }));
		} else {
			await goto(resolve('/admin/roles'));
		}
	}
</script>

<svelte:head>
	<title>New role</title>
</svelte:head>

{#if !canCreate}
	<EmptyState
		icon={Key}
		message="You cannot create roles"
		description="Ask an administrator for the roles permission."
	>
		{#snippet actions()}
			<Button variant="outline" href={listHref}>Back to roles</Button>
		{/snippet}
	</EmptyState>
{:else}
	<FormPage
		level="section"
		title="New role"
		description="Permissions are configured after creation."
		backHref={listHref}
		backLabel="Roles"
	>
		<FormSection title="Role" description="Name and whether new users get it.">
			<FormField
				label="Name"
				id="role-name"
				required
				help="Lowercase letters, numbers, hyphens"
				span="half"
			>
				<Input
					id="role-name"
					bind:value={name}
					placeholder="moderator"
					class="font-mono"
					autocomplete="off"
					spellcheck="false"
				/>
			</FormField>
			<FormField label="Description" id="role-desc">
				<Input id="role-desc" bind:value={description} placeholder="What this role is for" />
			</FormField>
			<FormField label="Default role" help="Granted to new users" layout="toggle">
				<Switch bind:checked={isDefault} />
			</FormField>
		</FormSection>

		{#snippet footer()}
			<Button onclick={createRole} disabled={creating || !name.trim()}>
				{#if creating}<Loader2 class="animate-spin" />{/if}
				{creating ? 'Creating' : 'Create role'}
			</Button>
			<Button variant="ghost-muted" href={listHref}>Cancel</Button>
		{/snippet}
	</FormPage>
{/if}

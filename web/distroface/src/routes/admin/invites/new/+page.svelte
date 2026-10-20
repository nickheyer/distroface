<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { Loader, Ticket } from '$lib/icons';
	import { toast } from 'svelte-sonner';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import LimitInput from '$lib/components/limit-input.svelte';
	import AsyncSelect from '$lib/components/async-select.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';

	let description = $state('');
	let selectedRoles = $state<string[]>([]);
	let pin = $state('');
	let maxUses = $state<number | undefined>(undefined);
	let expiryHours = $state<number | undefined>(undefined);
	let creating = $state(false);

	const listHref = resolve('/admin/invites');
	const canCreate = $derived(authStore.hasPermission('settings', 'create'));

	async function fetchRolePage(query: string, pageToken: string) {
		const resp = await rpcClient.role.listRoles({
			page: { query: { text: query, filters: [] }, pageToken, pageSize: 20 }
		});
		return {
			items: resp.roles.map((r) => ({ value: r.id, label: r.name, description: r.description })),
			nextPageToken: resp.page?.nextPageToken ?? ''
		};
	}

	async function createInvite() {
		if (!description.trim() || creating) return;
		creating = true;
		try {
			await rpcClient.auth.createInvite({
				description: description.trim(),
				roleIds: selectedRoles,
				pin: pin || undefined,
				maxUses: maxUses && maxUses > 0 ? maxUses : undefined,
				expiresInHours: expiryHours && expiryHours > 0 ? expiryHours : undefined
			});
		} catch {
			creating = false;
			return;
		}
		toast.success('Invite created');
		await goto(resolve('/admin/invites'));
	}
</script>

<svelte:head>
	<title>New invite</title>
</svelte:head>

{#if !canCreate}
	<EmptyState
		icon={Ticket}
		message="You cannot create invites"
		description="Ask an administrator for the settings permission."
	>
		{#snippet actions()}
			<Button variant="outline" href={listHref}>Back to invites</Button>
		{/snippet}
	</EmptyState>
{:else}
	<FormPage
		level="section"
		title="New invite"
		description="A link that lets someone register while public registration is off."
		backHref={listHref}
		backLabel="Invites"
	>
		<FormSection title="Invitation" description="What the link is for and what it grants.">
			<FormField label="Description" id="invite-desc" required span="half">
				<Input id="invite-desc" bind:value={description} placeholder="Team onboarding Q1" />
			</FormField>
			<FormField label="Roles" help="Granted on registration">
				<AsyncSelect
					multiple
					bind:selected={selectedRoles}
					placeholder="Select roles"
					searchPlaceholder="Search roles"
					fetchPage={fetchRolePage}
				/>
			</FormField>
		</FormSection>

		<FormSection title="Restrictions" description="Who can redeem it and for how long.">
			<FormField label="PIN" id="invite-pin-input" help="Also required to register" span="third">
				<Input
					id="invite-pin-input"
					bind:value={pin}
					placeholder="None"
					autocomplete="new-password"
					data-1p-ignore
					data-lpignore="true"
					data-bwignore
				/>
			</FormField>
			<FormField label="Max uses" id="invite-max" layout="inline" newRow>
				<LimitInput id="invite-max" label="uses" unit="uses" bind:value={maxUses} seed={1} />
			</FormField>
			<FormField label="Expires" id="invite-expiry" layout="inline">
				<LimitInput
					id="invite-expiry"
					label="expiry"
					unit="hours"
					bind:value={expiryHours}
					seed={72}
				/>
			</FormField>
		</FormSection>

		{#snippet footer()}
			<Button onclick={createInvite} disabled={creating || !description.trim()}>
				{#if creating}<Loader class="animate-spin" />{/if}
				{creating ? 'Creating' : 'Create invite'}
			</Button>
			<Button variant="ghost-muted" href={listHref}>Cancel</Button>
		{/snippet}
	</FormPage>
{/if}

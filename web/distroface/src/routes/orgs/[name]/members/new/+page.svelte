<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { getContext } from 'svelte';
	import { Loader2, Users } from '@lucide/svelte';
	import { toast } from 'svelte-sonner';
	import { rpcClient } from '$lib/api/rpc-client';
	import { Button } from '$lib/components/ui/button';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import AsyncSelect from '$lib/components/async-select.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import { orgRoleLabel } from '$lib/utils';
	import { OrgRole } from '$lib/proto/distroface/v1/types_pb';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgId = $derived(ctx.org?.id ?? '');
	const orgName = $derived(page.params.name ?? '');
	const listHref = $derived(resolve('/orgs/[name]/members', { name: orgName }));

	let userId = $state('');
	let role = $state<OrgRole>(OrgRole.MEMBER);
	let adding = $state(false);

	async function addMember() {
		if (!userId || adding) return;
		adding = true;
		const ok = await rpcClient.organization.addOrgMember({ orgId, userId, role }).then(
			() => true,
			() => false
		);
		adding = false;
		if (!ok) return;
		toast.success('Member added');
		await ctx.refresh();
		await goto(resolve('/orgs/[name]/members', { name: orgName }));
	}
</script>

<svelte:head>
	<title>Add member - {orgName}</title>
</svelte:head>

{#if !ctx.canAdmin}
	<EmptyState
		icon={Users}
		message="You cannot add members"
		description="Only organization owners and admins manage membership."
	>
		{#snippet actions()}
			<Button variant="outline" href={listHref}>Back to members</Button>
		{/snippet}
	</EmptyState>
{:else}
	<FormPage
		level="section"
		title="Add member"
		description="Members can push images to the shared namespace."
		backHref={listHref}
		backLabel="Members"
	>
		<FormSection title="Membership" description="Who joins and what they may do.">
			<FormField label="User" id="add-user" required span="half">
				<AsyncSelect
					bind:selected={userId}
					placeholder="Select a user"
					searchPlaceholder="Search users"
					fetchPage={async (query, pageToken) => {
						const resp = await rpcClient.user.searchUsers({
							page: { query: { text: query, filters: [] }, pageToken, pageSize: 20 }
						});
						return {
							items: resp.users.map((u) => ({ value: u.id, label: u.username })),
							nextPageToken: resp.page?.nextPageToken ?? ''
						};
					}}
				/>
			</FormField>
			<FormField
				label="Role"
				id="add-role"
				help="Admins manage members and settings, members push images"
				span="half"
			>
				<Select
					type="single"
					value={String(role)}
					onValueChange={(v) => {
						if (v) role = Number(v) as OrgRole;
					}}
				>
					<SelectTrigger class="w-full" id="add-role">{orgRoleLabel(role)}</SelectTrigger>
					<SelectContent>
						<SelectItem value={String(OrgRole.ADMIN)}>Admin</SelectItem>
						<SelectItem value={String(OrgRole.MEMBER)}>Member</SelectItem>
					</SelectContent>
				</Select>
			</FormField>
		</FormSection>

		{#snippet footer()}
			<Button onclick={addMember} disabled={adding || !userId}>
				{#if adding}<Loader2 class="animate-spin" />{/if}
				{adding ? 'Adding' : 'Add member'}
			</Button>
			<Button variant="ghost-muted" href={listHref}>Cancel</Button>
		{/snippet}
	</FormPage>
{/if}

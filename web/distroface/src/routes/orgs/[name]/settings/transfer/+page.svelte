<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { getContext } from 'svelte';
	import { ArrowLeftRight, Loader } from '$lib/icons';
	import { toast } from 'svelte-sonner';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import AsyncSelect from '$lib/components/async-select.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import { orgRoleLabel } from '$lib/utils';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgId = $derived(ctx.org?.id ?? '');
	const orgName = $derived(page.params.name ?? '');
	const settingsHref = $derived(resolve('/orgs/[name]/settings', { name: orgName }));

	let userId = $state('');
	let transferring = $state(false);
	let confirmOpen = $state(false);
	let memberLabels = $state<Record<string, string>>({});
	const newOwner = $derived(memberLabels[userId] ?? 'the selected member');

	async function transfer() {
		if (!userId || transferring) return;
		transferring = true;
		const ok = await rpcClient.organization.transferOrgOwnership({ orgId, userId }).then(
			() => true,
			() => false
		);
		transferring = false;
		confirmOpen = false;
		if (!ok) return;
		toast.success('Ownership transferred');
		await ctx.refresh();
		await goto(resolve('/orgs/[name]/settings', { name: orgName }));
	}
</script>

<svelte:head>
	<title>Transfer ownership - {orgName}</title>
</svelte:head>

{#if ctx.loading || !ctx.org}
	<Skeleton class="h-72 w-full rounded-lg" />
{:else if !ctx.canAdmin}
	<EmptyState
		icon={ArrowLeftRight}
		message="You cannot transfer this organization"
		description="Only the owner can hand it to another member."
	>
		{#snippet actions()}
			<Button variant="outline" href={resolve('/orgs/[name]', { name: orgName })}
				>Back to {orgName}</Button
			>
		{/snippet}
	</EmptyState>
{:else}
	<FormPage
		level="section"
		title="Transfer ownership"
		description="Make another member the owner of {orgName}. You become an admin."
		backHref={settingsHref}
		backLabel="Settings"
	>
		<FormSection title="New owner" description="Only current members can receive ownership.">
			<FormField label="Member" id="transfer-member" required span="half">
				<AsyncSelect
					bind:selected={userId}
					placeholder="Select a member"
					searchPlaceholder="Search members"
					fetchPage={async (query, pageToken) => {
						const resp = await rpcClient.organization.listOrgMembers({
							page: { query: { text: query, filters: [] }, pageToken, pageSize: 20 },
							orgId
						});
						for (const m of resp.members) memberLabels[m.userId] = m.username;
						return {
							items: resp.members
								.filter((m) => m.userId !== authStore.user?.id)
								.map((m) => ({
									value: m.userId,
									label: `${m.username} (${orgRoleLabel(m.role)})`
								})),
							nextPageToken: resp.page?.nextPageToken ?? ''
						};
					}}
				/>
			</FormField>
		</FormSection>

		{#snippet footer()}
			<Button
				variant="destructive"
				onclick={() => (confirmOpen = true)}
				disabled={transferring || !userId}
			>
				{#if transferring}<Loader class="animate-spin" />{/if}
				{transferring ? 'Transferring' : 'Transfer ownership'}
			</Button>
			<Button variant="ghost-muted" href={settingsHref}>Cancel</Button>
		{/snippet}
	</FormPage>
{/if}

<ConfirmDialog
	bind:open={confirmOpen}
	title="Transfer ownership"
	confirmLabel="Transfer ownership"
	onConfirm={transfer}
	loading={transferring}
	icon={ArrowLeftRight}
>
	{#snippet description()}
		<strong>{newOwner}</strong> becomes the owner of <strong>{orgName}</strong> and you become an admin.
		Only the new owner can hand it back.
	{/snippet}
</ConfirmDialog>

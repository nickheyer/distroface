<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { getContext } from 'svelte';
	import { ArrowRightLeft, Loader2 } from '@lucide/svelte';
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
	import { orgRoleLabel } from '$lib/utils';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgId = $derived(ctx.org?.id ?? '');
	const orgName = $derived(page.params.name ?? '');
	const settingsHref = $derived(resolve('/orgs/[name]/settings', { name: orgName }));

	let userId = $state('');
	let transferring = $state(false);

	async function transfer() {
		if (!userId || transferring) return;
		transferring = true;
		const ok = await rpcClient.organization.transferOrgOwnership({ orgId, userId }).then(
			() => true,
			() => false
		);
		transferring = false;
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
		icon={ArrowRightLeft}
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
			<Button variant="destructive" onclick={transfer} disabled={transferring || !userId}>
				{#if transferring}<Loader2 class="animate-spin" />{/if}
				{transferring ? 'Transferring' : 'Transfer ownership'}
			</Button>
			<Button variant="ghost-muted" href={settingsHref}>Cancel</Button>
		{/snippet}
	</FormPage>
{/if}

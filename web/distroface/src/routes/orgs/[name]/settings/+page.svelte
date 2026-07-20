<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { getContext } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import AsyncSelect from '$lib/components/async-select.svelte';
	import FormPanel from '$lib/components/form-panel.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import FormCard from '$lib/components/form-card.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import OrgSettingsManager from '$lib/components/org-settings-manager.svelte';
	import { Pencil, Save, Trash2, ArrowRightLeft } from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { toast } from 'svelte-sonner';
	import { orgRoleLabel } from '$lib/utils';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgName = $derived(page.params.name ?? '');
	const orgId = $derived(ctx.org?.id ?? '');

	let editDisplayName = $state('');
	let editDescription = $state('');
	let savingOrg = $state(false);

	let transferOpen = $state(false);
	let transferUserId = $state('');
	let transferring = $state(false);

	let deleteOrgOpen = $state(false);
	let deletingOrg = $state(false);

	$effect(() => {
		if (!ctx.loading && ctx.org && !ctx.canAdmin) {
			goto(resolve('/orgs/[name]', { name: orgName }));
		}
	});

	$effect(() => {
		if (ctx.org) {
			editDisplayName = ctx.org.displayName;
			editDescription = ctx.org.description;
		}
	});

	async function saveOrg() {
		savingOrg = true;
		try {
			await rpcClient.organization.updateOrganization({
				id: orgId,
				displayName: editDisplayName,
				description: editDescription
			});
			toast.success('Organization updated');
			await ctx.refresh();
		} catch {
			// error interceptor
		} finally {
			savingOrg = false;
		}
	}

	function openTransfer() {
		transferUserId = '';
		transferOpen = true;
	}

	async function confirmTransfer() {
		if (!transferUserId) return;
		transferring = true;
		try {
			await rpcClient.organization.transferOrgOwnership({ orgId, userId: transferUserId });
			toast.success('Ownership transferred');
			transferOpen = false;
			await ctx.refresh();
		} catch {
			// error interceptor
		} finally {
			transferring = false;
		}
	}

	async function confirmDeleteOrg() {
		deletingOrg = true;
		try {
			await rpcClient.organization.deleteOrganization({ id: orgId });
			toast.success('Organization deleted');
			goto(resolve('/orgs'));
		} catch {
			// error interceptor
		} finally {
			deletingOrg = false;
		}
	}
</script>

{#if ctx.loading || !ctx.org}
	<Skeleton class="h-72 w-full rounded-xl" />
{:else if ctx.canAdmin}
	<div class="space-y-4">
		<h2 class="section-title">Organization Settings</h2>

		<FormCard title="Details" description="Public name and description." icon={Pencil}>
			<div class="space-y-3">
				<FormField label="Display Name" id="edit-org-display">
					<Input id="edit-org-display" bind:value={editDisplayName} placeholder="Display name" />
				</FormField>
				<FormField label="Description" id="edit-org-desc">
					<Textarea id="edit-org-desc" bind:value={editDescription} placeholder="What does this organization do?" rows={3} />
				</FormField>
			</div>
			{#snippet footer()}
				<Button onclick={saveOrg} disabled={savingOrg} class="gap-2">
					<Save class="h-4 w-4" />
					{savingOrg ? 'Saving...' : 'Save Changes'}
				</Button>
			{/snippet}
		</FormCard>

		<OrgSettingsManager {orgId} />

		<div class="rounded-xl border border-destructive/40 overflow-hidden">
			<div class="px-6 py-4 border-b border-destructive/30 bg-destructive/5">
				<h3 class="text-sm font-semibold text-destructive">Danger Zone</h3>
			</div>
			<div class="divide-y divide-border/40">
				<div class="flex items-center justify-between gap-4 px-6 py-4">
					<div class="min-w-0">
						<p class="text-sm font-medium">Transfer ownership</p>
						<p class="text-[13px] text-muted-foreground mt-0.5">
							Make another member the owner, you become admin.
						</p>
					</div>
					<Button
						variant="outline"
						class="shrink-0 text-destructive border-destructive/40 hover:bg-destructive/10 hover:text-destructive"
						onclick={openTransfer}
					>
						<ArrowRightLeft class="h-4 w-4 mr-1.5" />
						Transfer
					</Button>
				</div>
				{#if ctx.canDelete}
					<div class="flex items-center justify-between gap-4 px-6 py-4">
						<div class="min-w-0">
							<p class="text-sm font-medium">Delete this organization</p>
							<p class="text-[13px] text-muted-foreground mt-0.5">
								Deletes every repository under this namespace, permanently.
							</p>
						</div>
						<Button
							variant="outline"
							class="shrink-0 text-destructive border-destructive/40 hover:bg-destructive/10 hover:text-destructive"
							onclick={() => (deleteOrgOpen = true)}
						>
							<Trash2 class="h-4 w-4 mr-1.5" />
							Delete
						</Button>
					</div>
				{/if}
			</div>
		</div>
	</div>
{/if}

<FormPanel
	bind:open={transferOpen}
	title="Transfer Ownership"
	description="Make another member the owner, you become admin."
	icon={ArrowRightLeft}
>
	<div class="space-y-6">
		<FormSection title="New Owner">
			<FormField label="Member" id="transfer-member" required help="Only current members can receive ownership">
				<AsyncSelect
					bind:selected={transferUserId}
					placeholder="Select a member..."
					searchPlaceholder="Search members..."
					fetchPage={async (query, pageToken) => {
						const resp = await rpcClient.organization.listOrgMembers({
							page: { query: { text: query, filters: [] }, pageToken, pageSize: 20 },
							orgId
						});
						return {
							items: resp.members
								.filter((m) => m.userId !== authStore.user?.id)
								.map((m) => ({ value: m.userId, label: `${m.username} (${orgRoleLabel(m.role)})` })),
							nextPageToken: resp.page?.nextPageToken ?? ''
						};
					}}
				/>
			</FormField>
		</FormSection>
	</div>

	{#snippet footer()}
		<Button variant="outline" onclick={() => (transferOpen = false)}>Cancel</Button>
		<Button variant="destructive" onclick={confirmTransfer} disabled={transferring || !transferUserId}>
			{transferring ? 'Transferring...' : 'Transfer Ownership'}
		</Button>
	{/snippet}
</FormPanel>

<ConfirmDialog bind:open={deleteOrgOpen} title="Delete Organization" confirmLabel="Delete" onConfirm={confirmDeleteOrg} loading={deletingOrg} icon={Trash2}>
	{#snippet description()}
		Deletes <strong>{orgName}</strong> and every repository and artifact under it, permanently.
	{/snippet}
</ConfirmDialog>

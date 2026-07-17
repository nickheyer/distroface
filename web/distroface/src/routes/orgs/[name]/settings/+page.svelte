<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { getContext } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import {
		Select, SelectContent, SelectItem, SelectTrigger
	} from '$lib/components/ui/select';
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
	import type { OrgMember } from '$lib/proto/distroface/v1/types_pb';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgName = $derived(page.params.name ?? '');

	let editDisplayName = $state('');
	let editDescription = $state('');
	let savingOrg = $state(false);

	let transferOpen = $state(false);
	let transferUsername = $state('');
	let transferring = $state(false);
	let transferCandidates = $state<OrgMember[]>([]);

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
				name: orgName,
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

	async function openTransfer() {
		transferUsername = '';
		transferOpen = true;
		try {
			const resp = await rpcClient.organization.listOrgMembers({ orgName, pageSize: 100 });
			transferCandidates = resp.members.filter((m) => m.username !== authStore.user?.username);
		} catch {
			transferCandidates = [];
		}
	}

	async function confirmTransfer() {
		if (!transferUsername) return;
		transferring = true;
		try {
			await rpcClient.organization.transferOrgOwnership({ orgName, username: transferUsername });
			toast.success(`Ownership transferred to ${transferUsername}`);
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
			await rpcClient.organization.deleteOrganization({ name: orgName });
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

		<FormCard title="Details" description="Public name and description for this organization." icon={Pencil}>
			<div class="space-y-3">
				<FormField label="Display Name" id="edit-org-display" help="The public name shown in the UI.">
					<Input id="edit-org-display" bind:value={editDisplayName} placeholder="Display name" />
				</FormField>
				<FormField label="Description" id="edit-org-desc" help="Tell people what this organization is about.">
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

		<OrgSettingsManager {orgName} />

		<div class="rounded-xl border border-destructive/40 overflow-hidden">
			<div class="px-6 py-4 border-b border-destructive/30 bg-destructive/5">
				<h3 class="text-sm font-semibold text-destructive">Danger Zone</h3>
			</div>
			<div class="divide-y divide-border/40">
				<div class="flex items-center justify-between gap-4 px-6 py-4">
					<div class="min-w-0">
						<p class="text-sm font-medium">Transfer ownership</p>
						<p class="text-[13px] text-muted-foreground mt-0.5">
							Make another member the owner of this organization. You will be demoted to admin.
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
								All repositories under this namespace will be deleted. This cannot be undone.
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
	description="Hand this organization to another member. You will be demoted to admin."
	icon={ArrowRightLeft}
>
	<div class="space-y-6">
		<FormSection title="New Owner">
			<FormField label="Member" id="transfer-member" required help="Only current members can receive ownership.">
				<Select
					type="single"
					value={transferUsername}
					onValueChange={(v) => { if (v) transferUsername = v; }}
				>
					<SelectTrigger id="transfer-member" class="w-full">
						{transferUsername || 'Select a member...'}
					</SelectTrigger>
					<SelectContent>
						{#each transferCandidates as m (m.userId)}
							<SelectItem value={m.username}>{m.username} ({orgRoleLabel(m.role)})</SelectItem>
						{/each}
					</SelectContent>
				</Select>
			</FormField>
		</FormSection>
	</div>

	{#snippet footer()}
		<Button variant="outline" onclick={() => (transferOpen = false)}>Cancel</Button>
		<Button variant="destructive" onclick={confirmTransfer} disabled={transferring || !transferUsername}>
			{transferring ? 'Transferring...' : 'Transfer Ownership'}
		</Button>
	{/snippet}
</FormPanel>

<ConfirmDialog bind:open={deleteOrgOpen} title="Delete Organization" confirmLabel="Delete" onConfirm={confirmDeleteOrg} loading={deletingOrg} icon={Trash2}>
	{#snippet description()}
		Are you sure you want to delete <strong>{orgName}</strong>? All repositories under this
		namespace will also be deleted. This action cannot be undone.
	{/snippet}
</ConfirmDialog>

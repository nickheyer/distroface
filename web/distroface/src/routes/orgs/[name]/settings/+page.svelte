<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { getContext } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import OrgSettingsManager from '$lib/components/org-settings-manager.svelte';
	import { Save, Trash, ArrowLeftRight, Loader, LogOut } from '$lib/icons';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { toast } from 'svelte-sonner';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgName = $derived(page.params.name ?? '');
	const orgId = $derived(ctx.org?.id ?? '');

	let editDisplayName = $state('');
	let editDescription = $state('');
	let savingOrg = $state(false);
	const transferHref = $derived(resolve('/orgs/[name]/settings/transfer', { name: orgName }));

	let deleteOrgOpen = $state(false);
	let deletingOrg = $state(false);

	// Outsiders have nothing to change here
	$effect(() => {
		if (!ctx.loading && ctx.org && !ctx.canAdmin && !ctx.isMember) {
			goto(resolve('/orgs/[name]', { name: orgName }));
		}
	});

	let leaveOpen = $state(false);
	let leaving = $state(false);

	async function confirmLeave() {
		leaving = true;
		try {
			await rpcClient.organization.removeOrgMember({ orgId, userId: authStore.user?.id ?? '' });
			toast.success(`You left ${orgName}`);
			goto(resolve('/orgs'));
		} catch {
			// error interceptor
		} finally {
			leaving = false;
		}
	}

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
	<Skeleton class="h-72 w-full rounded-lg" />
{:else if ctx.canAdmin}
	<FormPage
		level="section"
		title="Organization settings"
		description="Name, description, and ownership of {orgName}."
	>
		<FormSection title="Details" description="Public name and description.">
			<FormField label="Display name" id="edit-org-display" span="half">
				<Input id="edit-org-display" bind:value={editDisplayName} placeholder="Display name" />
			</FormField>
			<FormField label="Description" id="edit-org-desc">
				<Textarea
					id="edit-org-desc"
					bind:value={editDescription}
					placeholder="What does this organization do?"
					rows={3}
				/>
			</FormField>
			{#snippet footer()}
				<Button onclick={saveOrg} disabled={savingOrg}>
					{#if savingOrg}
						<Loader class="animate-spin" />
						Saving
					{:else}
						<Save />
						Save changes
					{/if}
				</Button>
			{/snippet}
		</FormSection>

		<OrgSettingsManager {orgId} />

		<FormSection
			title="Danger zone"
			description="Membership, ownership, and deletion of {orgName}."
		>
			<div class="box divide-y border-destructive/40">
				{#if ctx.isMember}
					<div class="flex items-center justify-between gap-4 px-4 py-3">
						<div class="min-w-0">
							<p class="text-sm font-medium">Leave this organization</p>
							<p class="mt-0.5 text-sm text-muted-foreground">
								You lose access to its private repositories. The last owner cannot leave.
							</p>
						</div>
						<Button
							variant="outline-destructive"
							class="shrink-0"
							onclick={() => (leaveOpen = true)}
						>
							<LogOut />
							Leave
						</Button>
					</div>
				{/if}
				<div class="flex items-center justify-between gap-4 px-4 py-3">
					<div class="min-w-0">
						<p class="text-sm font-medium">Transfer ownership</p>
						<p class="mt-0.5 text-sm text-muted-foreground">
							Make another member the owner, you become admin.
						</p>
					</div>
					<Button variant="outline-destructive" class="shrink-0" href={transferHref}>
						<ArrowLeftRight />
						Transfer
					</Button>
				</div>
				{#if ctx.canDelete}
					<div class="flex items-center justify-between gap-4 px-4 py-3">
						<div class="min-w-0">
							<p class="text-sm font-medium">Delete this organization</p>
							<p class="mt-0.5 text-sm text-muted-foreground">
								Deletes every repository under this namespace, permanently.
							</p>
						</div>
						<Button
							variant="outline-destructive"
							class="shrink-0"
							onclick={() => (deleteOrgOpen = true)}
						>
							<Trash />
							Delete
						</Button>
					</div>
				{/if}
			</div>
		</FormSection>
	</FormPage>
{:else if ctx.isMember}
	<FormPage level="section" title="Membership" description="Your place in {orgName}.">
		<FormSection title="Danger zone" description="Leaving is immediate.">
			<div class="box border-destructive/40">
				<div class="flex items-center justify-between gap-4 px-4 py-3">
					<div class="min-w-0">
						<p class="text-sm font-medium">Leave this organization</p>
						<p class="mt-0.5 text-sm text-muted-foreground">
							You lose access to its private repositories and teams. An admin can add you back.
							Memberships synced from an identity provider cannot be left here.
						</p>
					</div>
					<Button variant="outline-destructive" class="shrink-0" onclick={() => (leaveOpen = true)}>
						<LogOut />
						Leave
					</Button>
				</div>
			</div>
		</FormSection>
	</FormPage>
{/if}

<ConfirmDialog
	bind:open={leaveOpen}
	title="Leave organization"
	confirmLabel="Leave"
	onConfirm={confirmLeave}
	loading={leaving}
	icon={LogOut}
>
	{#snippet description()}
		You will no longer be a member of <strong>{orgName}</strong>.
	{/snippet}
</ConfirmDialog>

<ConfirmDialog
	bind:open={deleteOrgOpen}
	title="Delete organization"
	confirmLabel="Delete"
	onConfirm={confirmDeleteOrg}
	loading={deletingOrg}
	icon={Trash}
>
	{#snippet description()}
		Deletes <strong>{orgName}</strong> and every repository and artifact under it, permanently.
	{/snippet}
</ConfirmDialog>

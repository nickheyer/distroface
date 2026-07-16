<script lang="ts">
	import { onMount } from 'svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { toast } from 'svelte-sonner';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Switch } from '$lib/components/ui/switch';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import CopyButton from '$lib/components/copy-button.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import PortalFormPanel from '$lib/components/portal-form-panel.svelte';
	import type { RegistryPortal } from '$lib/proto/distroface/v1/portal_pb';
	import { Globe, Plus, Pencil, Trash2 } from '@lucide/svelte';
	import { parseAddress, formatAddress } from '$lib/portal-address';

	let { orgName }: { orgName: string } = $props();

	type RuleDraft = { pattern: string; replace: string };

	let portals = $state<RegistryPortal[]>([]);
	let loading = $state(false);
	let toggling = $state<string | null>(null);

	let createOpen = $state(false);
	let newName = $state('');
	let newAddress = $state('');
	let newMapUnqualified = $state(true);
	let newAllowPush = $state(true);
	let newRequireAuth = $state(false);
	let newRules = $state<RuleDraft[]>([]);
	let creating = $state(false);

	let editOpen = $state(false);
	let editTarget = $state<RegistryPortal | null>(null);
	let editName = $state('');
	let editAddress = $state('');
	let editMapUnqualified = $state(true);
	let editAllowPush = $state(true);
	let editRequireAuth = $state(false);
	let editRules = $state<RuleDraft[]>([]);
	let saving = $state(false);

	let deleteOpen = $state(false);
	let deleteTarget = $state<RegistryPortal | null>(null);
	let deleting = $state(false);

	function cleanRules(rules: RuleDraft[]): RuleDraft[] {
		return rules
			.map((r) => ({ pattern: r.pattern.trim(), replace: r.replace.trim() }))
			.filter((r) => r.pattern !== '' || r.replace !== '');
	}

	function addressOk(address: string): boolean {
		return parseAddress(address).error === '';
	}

	function portalMeta(portal: RegistryPortal): string[] {
		const parts = [portal.name];
		if (!portal.allowPush) parts.push('pull only');
		if (portal.requireAuth) parts.push('auth required');
		if (!portal.mapUnqualified) parts.push('no bare-name mapping');
		if (portal.rules.length > 0) {
			parts.push(`${portal.rules.length} rule${portal.rules.length !== 1 ? 's' : ''}`);
		}
		return parts;
	}

	async function load() {
		loading = true;
		try {
			const resp = await rpcClient.portal.listPortals({ orgName });
			portals = resp.portals;
		} catch { portals = []; }
		finally { loading = false; }
	}

	async function setRunning(portal: RegistryPortal, enabled: boolean) {
		toggling = portal.id;
		try {
			const resp = await rpcClient.portal.updatePortal({ orgName, id: portal.id, enabled });
			const updated = resp.portal;
			if (updated) portals = portals.map((p) => (p.id === updated.id ? updated : p));
			toast.success(enabled ? 'Portal started' : 'Portal stopped');
		} catch { load(); }
		finally { toggling = null; }
	}

	function openCreate() {
		newName = '';
		newAddress = '';
		newMapUnqualified = true;
		newAllowPush = true;
		newRequireAuth = false;
		newRules = [];
		createOpen = true;
	}

	async function submitCreate() {
		const address = parseAddress(newAddress);
		creating = true;
		try {
			await rpcClient.portal.createPortal({
				orgName,
				name: newName.trim(),
				hostname: address.hostname,
				port: address.port,
				mapUnqualified: newMapUnqualified,
				rules: cleanRules(newRules),
				allowPush: newAllowPush,
				requireAuth: newRequireAuth
			});
			createOpen = false;
			toast.success('Portal created');
			load();
		} catch { /* error interceptor */ }
		finally { creating = false; }
	}

	function openEdit(portal: RegistryPortal) {
		editTarget = portal;
		editName = portal.name;
		editAddress = formatAddress(portal.hostname, portal.port);
		editMapUnqualified = portal.mapUnqualified;
		editAllowPush = portal.allowPush;
		editRequireAuth = portal.requireAuth;
		editRules = portal.rules.map((r) => ({ pattern: r.pattern, replace: r.replace }));
		editOpen = true;
	}

	async function submitEdit() {
		if (!editTarget) return;
		const address = parseAddress(editAddress);
		saving = true;
		try {
			await rpcClient.portal.updatePortal({
				orgName,
				id: editTarget.id,
				name: editName.trim(),
				hostname: address.hostname,
				port: address.port,
				mapUnqualified: editMapUnqualified,
				setRules: true,
				rules: cleanRules(editRules),
				allowPush: editAllowPush,
				requireAuth: editRequireAuth
			});
			editOpen = false;
			toast.success('Portal updated');
			load();
		} catch { /* error interceptor */ }
		finally { saving = false; }
	}

	function confirmDelete(portal: RegistryPortal) {
		deleteTarget = portal;
		deleteOpen = true;
	}

	async function doDelete() {
		if (!deleteTarget) return;
		deleting = true;
		try {
			await rpcClient.portal.deletePortal({ orgName, id: deleteTarget.id });
			deleteOpen = false;
			toast.success('Portal deleted');
			load();
		} catch { /* error interceptor */ }
		finally { deleting = false; }
	}

	onMount(() => { load(); });
</script>

<div class="space-y-4">
	<div class="section-header">
		<div class="min-w-0 space-y-1">
			<h2 class="section-title">Registry Portals</h2>
			<p class="text-[13px] text-muted-foreground leading-snug max-w-2xl">
				Extra hostnames and ports that serve this organization's images.
			</p>
		</div>
		<Button size="sm" class="shrink-0" onclick={openCreate}>
			<Plus class="h-4 w-4 mr-1.5" />Add Portal
		</Button>
	</div>

	{#if loading}
		<div class="space-y-2">
			{#each { length: 2 }, i (i)}
				<Skeleton class="h-16 w-full rounded-xl" />
			{/each}
		</div>
	{:else if portals.length === 0}
		<EmptyState
			icon={Globe}
			message="No portals"
			description="Add a hostname or port that serves {orgName}'s images. Example: docker pull registry.example.com/myimage."
		>
			{#snippet actions()}
				<Button variant="outline" size="sm" onclick={openCreate}>
					<Plus class="h-3.5 w-3.5 mr-1.5" />Add Portal
				</Button>
			{/snippet}
		</EmptyState>
	{:else}
		<div class="space-y-2">
			{#each portals as portal (portal.id)}
				<div class="rounded-xl border border-border/60 bg-card px-4 py-3 flex items-center gap-3.5">
					<span
						class="h-2 w-2 rounded-full shrink-0 {portal.enabled
							? 'bg-green-500 shadow-[0_0_6px] shadow-green-500/60'
							: 'bg-muted-foreground/30'}"
						role="status"
						aria-label={portal.enabled ? 'Running' : 'Stopped'}
					></span>

					<div class="flex-1 min-w-0">
						<div class="flex items-center gap-1 min-w-0">
							<span
								class="font-mono text-sm font-medium truncate {portal.enabled
									? ''
									: 'text-muted-foreground'}"
							>
								{formatAddress(portal.hostname, portal.port)}
							</span>
							{#if portal.hostname === ''}
								<span class="text-xs text-muted-foreground/60 shrink-0 ml-0.5">any host</span>
							{/if}
							{#if portal.hostname !== ''}
								<CopyButton
									text={formatAddress(portal.hostname, portal.port)}
									label="Address copied"
								/>
							{/if}
						</div>
						<p class="text-xs text-muted-foreground/70 truncate mt-0.5">
							{portalMeta(portal).join(' · ')}
						</p>
					</div>

					<div class="flex items-center gap-2 shrink-0">
						<span class="text-xs text-muted-foreground w-14 text-right hidden sm:block">
							{portal.enabled ? 'Running' : 'Stopped'}
						</span>
						<Switch
							checked={portal.enabled}
							disabled={toggling === portal.id}
							onCheckedChange={(checked) => setRunning(portal, checked)}
							aria-label="{portal.enabled ? 'Stop' : 'Start'} portal {portal.name}"
						/>
						<div class="w-px h-4 bg-border/60 mx-1"></div>
						<Button variant="ghost" size="icon" class="h-7 w-7" onclick={() => openEdit(portal)}>
							<Pencil class="h-3 w-3" />
						</Button>
						<Button
							variant="ghost"
							size="icon"
							class="h-7 w-7 text-destructive"
							onclick={() => confirmDelete(portal)}
						>
							<Trash2 class="h-3 w-3" />
						</Button>
					</div>
				</div>
			{/each}
		</div>
	{/if}
</div>

<PortalFormPanel
	bind:open={createOpen}
	title="Add Portal"
	description="Serve this organization's images from an extra hostname or port. New portals start immediately."
	{orgName}
	bind:name={newName}
	bind:address={newAddress}
	bind:mapUnqualified={newMapUnqualified}
	bind:allowPush={newAllowPush}
	bind:requireAuth={newRequireAuth}
	bind:rules={newRules}
>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (createOpen = false)}>Cancel</Button>
		<Button onclick={submitCreate} disabled={creating || !newName.trim() || !addressOk(newAddress)}>
			{creating ? 'Creating...' : 'Create Portal'}
		</Button>
	{/snippet}
</PortalFormPanel>

<PortalFormPanel
	bind:open={editOpen}
	title="Edit Portal"
	description="Changes apply immediately."
	idPrefix="portal-edit"
	{orgName}
	bind:name={editName}
	bind:address={editAddress}
	bind:mapUnqualified={editMapUnqualified}
	bind:allowPush={editAllowPush}
	bind:requireAuth={editRequireAuth}
	bind:rules={editRules}
>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (editOpen = false)}>Cancel</Button>
		<Button onclick={submitEdit} disabled={saving || !editName.trim() || !addressOk(editAddress)}>
			{saving ? 'Saving...' : 'Save Changes'}
		</Button>
	{/snippet}
</PortalFormPanel>

<ConfirmDialog bind:open={deleteOpen} title="Delete Portal" confirmLabel="Delete" onConfirm={doDelete} loading={deleting} icon={Trash2}>
	{#snippet description()}
		Delete <strong>{deleteTarget ? formatAddress(deleteTarget.hostname, deleteTarget.port) : ''}</strong>?
		Clients using it will stop working immediately.
	{/snippet}
</ConfirmDialog>

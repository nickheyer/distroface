<script lang="ts">
	import { onMount } from 'svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { toast } from 'svelte-sonner';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import {
		Table, TableBody, TableCell, TableHead, TableHeader, TableRow
	} from '$lib/components/ui/table';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import PortalFormPanel from '$lib/components/portal-form-panel.svelte';
	import type { RegistryPortal } from '$lib/proto/distroface/v1/portal_pb';
	import { Globe, Plus, Pencil, Trash2 } from '@lucide/svelte';
	import { parseEndpoint, formatEndpoint } from '$lib/portal-endpoint';

	let { orgName }: { orgName: string } = $props();

	type RuleDraft = { pattern: string; replace: string };

	// ── List state ──────────────────────────────────────────────────────
	let portals = $state<RegistryPortal[]>([]);
	let loading = $state(false);

	// ── Create state ────────────────────────────────────────────────────
	let createOpen = $state(false);
	let newName = $state('');
	let newEndpoint = $state('');
	let newMapUnqualified = $state(true);
	let newAllowPush = $state(true);
	let newRequireAuth = $state(false);
	let newRules = $state<RuleDraft[]>([]);
	let creating = $state(false);

	// ── Edit state ──────────────────────────────────────────────────────
	let editOpen = $state(false);
	let editTarget = $state<RegistryPortal | null>(null);
	let editName = $state('');
	let editEndpoint = $state('');
	let editMapUnqualified = $state(true);
	let editAllowPush = $state(true);
	let editRequireAuth = $state(false);
	let editEnabled = $state(true);
	let editRules = $state<RuleDraft[]>([]);
	let saving = $state(false);

	// ── Delete state ────────────────────────────────────────────────────
	let deleteOpen = $state(false);
	let deleteTarget = $state<RegistryPortal | null>(null);
	let deleting = $state(false);

	function cleanRules(rules: RuleDraft[]): RuleDraft[] {
		return rules
			.map((r) => ({ pattern: r.pattern.trim(), replace: r.replace.trim() }))
			.filter((r) => r.pattern !== '' || r.replace !== '');
	}

	function endpointOk(endpoint: string): boolean {
		return parseEndpoint(endpoint).error === '';
	}

	// ── Handlers ────────────────────────────────────────────────────────
	async function load() {
		loading = true;
		try {
			const resp = await rpcClient.portal.listPortals({ orgName });
			portals = resp.portals;
		} catch { portals = []; }
		finally { loading = false; }
	}

	function openCreate() {
		newName = '';
		newEndpoint = '';
		newMapUnqualified = true;
		newAllowPush = true;
		newRequireAuth = false;
		newRules = [];
		createOpen = true;
	}

	async function submitCreate() {
		const endpoint = parseEndpoint(newEndpoint);
		creating = true;
		try {
			await rpcClient.portal.createPortal({
				orgName,
				name: newName.trim(),
				hostname: endpoint.hostname,
				port: endpoint.port,
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
		editEndpoint = formatEndpoint(portal.hostname, portal.port);
		editMapUnqualified = portal.mapUnqualified;
		editAllowPush = portal.allowPush;
		editRequireAuth = portal.requireAuth;
		editEnabled = portal.enabled;
		editRules = portal.rules.map((r) => ({ pattern: r.pattern, replace: r.replace }));
		editOpen = true;
	}

	async function submitEdit() {
		if (!editTarget) return;
		saving = true;
		try {
			await rpcClient.portal.updatePortal({
				orgName,
				id: editTarget.id,
				name: editName.trim(),
				hostname: parseEndpoint(editEndpoint).hostname,
				port: parseEndpoint(editEndpoint).port,
				mapUnqualified: editMapUnqualified,
				setRules: true,
				rules: cleanRules(editRules),
				allowPush: editAllowPush,
				requireAuth: editRequireAuth,
				enabled: editEnabled
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
				Requests to a portal's hostname and/or dedicated port are aliased into this
				organization's namespace — e.g.
				<code class="font-mono text-xs">docker pull &lt;hostname-or-host:port&gt;/myimage</code>
				resolves to <code class="font-mono text-xs">{orgName}/myimage</code>. Point DNS for portal
				hostnames at this server.
			</p>
		</div>
		<Button size="sm" class="shrink-0" onclick={openCreate}>
			<Plus class="h-4 w-4 mr-1.5" />Add Portal
		</Button>
	</div>

	{#if loading}
		<div class="space-y-2">
			{#each Array(2) as _}
				<Skeleton class="h-14 w-full rounded-xl" />
			{/each}
		</div>
	{:else if portals.length === 0}
		<EmptyState
			icon={Globe}
			message="No portals"
			description="Add a portal to serve this organization's images from an alternate registry hostname or dedicated port."
		>
			{#snippet actions()}
				<Button variant="outline" size="sm" onclick={openCreate}>
					<Plus class="h-3.5 w-3.5 mr-1.5" />Add Portal
				</Button>
			{/snippet}
		</EmptyState>
	{:else}
		<div class="data-table">
			<Table class="table-fixed">
				<TableHeader>
					<TableRow>
						<TableHead class="th">Endpoint</TableHead>
						<TableHead class="th w-32">Name</TableHead>
						<TableHead class="th w-56">Options</TableHead>
						<TableHead class="th w-20 text-center">Status</TableHead>
						<TableHead class="th w-24 text-right">Actions</TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{#each portals as portal (portal.id)}
						<TableRow>
							<TableCell class="py-3 px-3">
								<span class="font-mono text-xs truncate block">
									{formatEndpoint(portal.hostname, portal.port)}{#if portal.hostname === ''}<span class="font-sans text-muted-foreground/60">
											(any host)</span
										>{/if}
								</span>
							</TableCell>
							<TableCell class="py-3 px-3">
								<span class="text-sm truncate block">{portal.name}</span>
							</TableCell>
							<TableCell class="py-3 px-3">
								<div class="flex flex-wrap gap-1">
									{#if portal.mapUnqualified}
										<Badge variant="outline" class="text-[10px] py-0 h-4.5">map unqualified</Badge>
									{/if}
									<Badge variant="outline" class="text-[10px] py-0 h-4.5">
										{portal.allowPush ? 'push + pull' : 'pull only'}
									</Badge>
									{#if portal.requireAuth}
										<Badge variant="outline" class="text-[10px] py-0 h-4.5">auth required</Badge>
									{/if}
									{#if portal.rules.length > 0}
										<Badge variant="outline" class="text-[10px] py-0 h-4.5">
											{portal.rules.length} rule{portal.rules.length !== 1 ? 's' : ''}
										</Badge>
									{/if}
								</div>
							</TableCell>
							<TableCell class="py-3 px-3 text-center">
								{#if portal.enabled}
									<Badge variant="secondary" class="text-[10px] py-0 h-4.5 text-green-600 dark:text-green-400">Enabled</Badge>
								{:else}
									<Badge variant="secondary" class="text-[10px] py-0 h-4.5 text-muted-foreground">Disabled</Badge>
								{/if}
							</TableCell>
							<TableCell class="py-3 px-3 text-right">
								<div class="flex items-center justify-end gap-1">
									<Button variant="ghost" size="icon" class="h-7 w-7" onclick={() => openEdit(portal)}>
										<Pencil class="h-3 w-3" />
									</Button>
									<Button variant="ghost" size="icon" class="h-7 w-7 text-destructive" onclick={() => confirmDelete(portal)}>
										<Trash2 class="h-3 w-3" />
									</Button>
								</div>
							</TableCell>
						</TableRow>
					{/each}
				</TableBody>
			</Table>
		</div>
	{/if}
</div>

<!-- Create Portal Panel -->
<PortalFormPanel
	bind:open={createOpen}
	title="Add Portal"
	description="Serve this organization's images from an alternate registry hostname or dedicated port."
	formMode="create"
	{orgName}
	bind:name={newName}
	bind:endpoint={newEndpoint}
	bind:mapUnqualified={newMapUnqualified}
	bind:allowPush={newAllowPush}
	bind:requireAuth={newRequireAuth}
	bind:rules={newRules}
>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (createOpen = false)}>Cancel</Button>
		<Button onclick={submitCreate} disabled={creating || !newName.trim() || !endpointOk(newEndpoint)}>
			{creating ? 'Creating...' : 'Create Portal'}
		</Button>
	{/snippet}
</PortalFormPanel>

<!-- Edit Portal Panel -->
<PortalFormPanel
	bind:open={editOpen}
	title="Edit Portal"
	description="Update portal configuration."
	formMode="edit"
	idPrefix="portal-edit"
	{orgName}
	bind:name={editName}
	bind:endpoint={editEndpoint}
	bind:mapUnqualified={editMapUnqualified}
	bind:allowPush={editAllowPush}
	bind:requireAuth={editRequireAuth}
	bind:enabled={editEnabled}
	bind:rules={editRules}
>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (editOpen = false)}>Cancel</Button>
		<Button onclick={submitEdit} disabled={saving || !editName.trim() || !endpointOk(editEndpoint)}>
			{saving ? 'Saving...' : 'Save Changes'}
		</Button>
	{/snippet}
</PortalFormPanel>

<!-- Delete Portal -->
<ConfirmDialog bind:open={deleteOpen} title="Delete Portal" confirmLabel="Delete" onConfirm={doDelete} loading={deleting} icon={Trash2}>
	{#snippet description()}
		Are you sure you want to delete the portal
		<strong>{deleteTarget ? formatEndpoint(deleteTarget.hostname, deleteTarget.port) : ''}</strong>? Clients using this
		endpoint will stop working immediately. This action cannot be undone.
	{/snippet}
</ConfirmDialog>

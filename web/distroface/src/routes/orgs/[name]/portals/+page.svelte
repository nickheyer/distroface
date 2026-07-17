<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount, getContext } from 'svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { toast } from 'svelte-sonner';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Switch } from '$lib/components/ui/switch';
	import {
		Table, TableBody, TableCell, TableHead, TableHeader, TableRow
	} from '$lib/components/ui/table';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import CopyButton from '$lib/components/copy-button.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import { Globe, Plus, Pencil, Trash2 } from '@lucide/svelte';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime } from '$lib/utils';
	import { effectiveAddress } from '$lib/portal-address';
	import type { RegistryPortal } from '$lib/proto/distroface/v1/portal_pb';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgName = $derived(page.params.name ?? '');

	let portals = $state<RegistryPortal[]>([]);
	let mainPort = $state(0);
	let loading = $state(true);
	let toggling = $state<string | null>(null);

	let deleteOpen = $state(false);
	let deleteTarget = $state<RegistryPortal | null>(null);
	let deleting = $state(false);

	$effect(() => {
		if (!ctx.loading && ctx.org && !ctx.canAdmin) {
			goto(resolve('/orgs/[name]', { name: orgName }));
		}
	});

	async function load() {
		loading = true;
		try {
			const resp = await rpcClient.portal.listPortals({ orgName });
			portals = resp.portals;
			mainPort = resp.mainPort;
		} catch { portals = []; }
		finally { loading = false; }
	}

	function accessBadges(portal: RegistryPortal): string[] {
		const badges = [];
		if (!portal.allowPush) badges.push('Pull only');
		if (portal.requireAuth) badges.push('Sign-in required');
		if (portal.mapUnqualified) badges.push('Bare names');
		if (portal.rules.length > 0) {
			badges.push(`${portal.rules.length} rewrite${portal.rules.length !== 1 ? 's' : ''}`);
		}
		return badges;
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

	function openEdit(portal: RegistryPortal) {
		goto(resolve('/orgs/[name]/portals/[id]', { name: orgName, id: portal.id }));
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

	onMount(load);
</script>

<div class="space-y-4">
	<div class="section-header">
		<div class="min-w-0 space-y-1">
			<h2 class="section-title">Portals</h2>
			<p class="section-subtitle max-w-2xl">
				A portal serves this organization's own view of the app — registry, artifacts, and web
				UI, scoped to {orgName} — on another hostname or port.
			</p>
		</div>
		<Button size="sm" class="shrink-0" onclick={() => goto(resolve('/orgs/[name]/portals/new', { name: orgName }))}>
			<Plus class="h-4 w-4 mr-1.5" />New Portal
		</Button>
	</div>

	{#if loading}
		<div class="space-y-2">
			{#each { length: 2 }, i (i)}
				<Skeleton class="h-14 w-full rounded-xl" />
			{/each}
		</div>
	{:else if portals.length === 0}
		<EmptyState
			icon={Globe}
			message="No portals yet"
			description="Give {orgName} its own address, like registry.example.com — clients that use it only ever see this organization."
		>
			{#snippet actions()}
				<Button
					variant="outline"
					size="sm"
					onclick={() => goto(resolve('/orgs/[name]/portals/new', { name: orgName }))}
				>
					<Plus class="h-3.5 w-3.5 mr-1.5" />New Portal
				</Button>
			{/snippet}
		</EmptyState>
	{:else}
		<div class="data-table">
			<Table>
				<TableHeader>
					<TableRow class="bg-muted/30 hover:bg-muted/30">
						<TableHead class="th">Portal</TableHead>
						<TableHead class="th">Address</TableHead>
						<TableHead class="th hidden lg:table-cell">Access</TableHead>
						<TableHead class="th">Status</TableHead>
						<TableHead class="th w-24"></TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{#each portals as portal (portal.id)}
						<TableRow>
							<TableCell class="py-3 px-3">
								<p class="font-medium">{portal.name}</p>
								{#if portal.createdAt}
									<p class="text-xs text-muted-foreground mt-0.5">
										Created {relativeTime(timestampDate(portal.createdAt))}
									</p>
								{/if}
							</TableCell>
							<TableCell class="py-3 px-3">
								<div class="flex items-center gap-1 min-w-0">
									<span class="font-mono text-[13px] truncate {portal.enabled ? '' : 'text-muted-foreground'}">
										{effectiveAddress(portal.hostname, portal.port)}
									</span>
									<CopyButton text={effectiveAddress(portal.hostname, portal.port)} label="Address copied" />
								</div>
								{#if portal.hostname === ''}
									<p class="text-xs text-muted-foreground/70 mt-0.5">Any hostname on port {portal.port}</p>
								{:else if portal.port === 0}
									<p class="text-xs text-muted-foreground/70 mt-0.5">App port{mainPort ? ` (${mainPort})` : ''}</p>
								{/if}
							</TableCell>
							<TableCell class="py-3 px-3 hidden lg:table-cell">
								<div class="flex flex-wrap gap-1">
									{#each accessBadges(portal) as badge (badge)}
										<Badge variant="outline" class="text-xs font-normal">{badge}</Badge>
									{:else}
										<span class="text-xs text-muted-foreground/70">Open push and pull</span>
									{/each}
								</div>
							</TableCell>
							<TableCell class="py-3 px-3">
								<div class="flex items-center gap-2">
									<Switch
										checked={portal.enabled}
										disabled={toggling === portal.id}
										onCheckedChange={(checked) => setRunning(portal, checked)}
										aria-label="{portal.enabled ? 'Stop' : 'Start'} portal {portal.name}"
									/>
									<span class="text-xs text-muted-foreground hidden sm:inline">
										{portal.enabled ? 'Running' : 'Stopped'}
									</span>
								</div>
							</TableCell>
							<TableCell class="text-right py-3 px-3">
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
							</TableCell>
						</TableRow>
					{/each}
				</TableBody>
			</Table>
		</div>
	{/if}
</div>

<ConfirmDialog bind:open={deleteOpen} title="Delete Portal" confirmLabel="Delete" onConfirm={doDelete} loading={deleting} icon={Trash2}>
	{#snippet description()}
		Delete <strong>{deleteTarget?.name}</strong> at
		<strong>{deleteTarget ? effectiveAddress(deleteTarget.hostname, deleteTarget.port) : ''}</strong>?
		Clients using this address will stop working immediately.
	{/snippet}
</ConfirmDialog>

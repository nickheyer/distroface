<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { rpcClient } from '$lib/api/rpc-client';
	import { toast } from 'svelte-sonner';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Input } from '$lib/components/ui/input';
	import { Switch } from '$lib/components/ui/switch';
	import FormField from '$lib/components/form-field.svelte';
	import FormCard from '$lib/components/form-card.svelte';
	import CopyButton from '$lib/components/copy-button.svelte';
	import { Globe, Tag, Network, Lock, Shuffle, Plus, X } from '@lucide/svelte';
	import { effectiveAddress, placementError } from '$lib/portal-address';
	import type { RegistryPortal } from '$lib/proto/distroface/v1/portal_pb';

	let {
		orgName,
		orgId,
		mainPort = 0,
		portal = null
	}: {
		orgName: string;
		orgId: string;
		mainPort?: number;
		portal?: RegistryPortal | null;
	} = $props();

	type RuleDraft = { pattern: string; replace: string };

	// Draft snapshot on mount, parent keys this component per portal
	/* svelte-ignore state_referenced_locally */
	let name = $state(portal?.name ?? '');
	/* svelte-ignore state_referenced_locally */
	let hostname = $state(portal?.hostname ?? '');
	/* svelte-ignore state_referenced_locally */
	let portText = $state(portal && portal.port > 0 ? String(portal.port) : '');
	/* svelte-ignore state_referenced_locally */
	let mapUnqualified = $state(portal?.mapUnqualified ?? true);
	/* svelte-ignore state_referenced_locally */
	let allowPush = $state(portal?.allowPush ?? true);
	/* svelte-ignore state_referenced_locally */
	let requireAuth = $state(portal?.requireAuth ?? false);
	/* svelte-ignore state_referenced_locally */
	let rules = $state<RuleDraft[]>(
		portal?.rules.map((r) => ({ pattern: r.pattern, replace: r.replace })) ?? []
	);
	let submitting = $state(false);

	function goBack() {
		goto(resolve('/orgs/[name]/portals', { name: orgName }));
	}

	const addressError = $derived(
		hostname.trim() === '' && portText.trim() === '' ? '' : placementError(hostname, portText)
	);
	const formValid = $derived(
		name.trim() !== '' &&
		(hostname.trim() !== '' || portText.trim() !== '') &&
		placementError(hostname, portText) === ''
	);
	const previewAddress = $derived(
		placementError(hostname, portText) === '' && (hostname.trim() !== '' || portText.trim() !== '')
			? effectiveAddress(hostname.trim().toLowerCase(), Number(portText.trim()) || 0)
			: ''
	);
	const previewImage = $derived(mapUnqualified ? 'myimage' : `${orgName}/myimage`);
	const scheme = $derived(
		typeof window !== 'undefined' ? window.location.protocol.replace(':', '') : 'http'
	);
	const ruleCount = $derived(
		rules.filter((r) => r.pattern.trim() !== '' || r.replace.trim() !== '').length
	);

	async function submit() {
		if (!formValid) return;
		const cleanedRules = rules
			.map((r) => ({ pattern: r.pattern.trim(), replace: r.replace.trim() }))
			.filter((r) => r.pattern !== '' || r.replace !== '');
		const common = {
			orgId,
			name: name.trim(),
			hostname: hostname.trim().toLowerCase(),
			port: Number(portText.trim()) || 0,
			mapUnqualified,
			allowPush,
			requireAuth,
			rules: cleanedRules
		};
		submitting = true;
		try {
			if (portal) {
				await rpcClient.portal.updatePortal({ ...common, id: portal.id, setRules: true });
				toast.success('Portal updated');
			} else {
				await rpcClient.portal.createPortal(common);
				toast.success('Portal created');
			}
			goBack();
		} catch { /* error interceptor */ }
		finally { submitting = false; }
	}
</script>

<div class="grid gap-6 lg:grid-cols-[1fr_19rem] items-start">
	<div class="space-y-4 min-w-0">
		<FormCard title="Portal" description="A label for this portal, shown only in the portal list." icon={Tag}>
			<FormField label="Name" id="portal-name" required>
				<Input id="portal-name" bind:value={name} placeholder="e.g. public-mirror" class="max-w-sm" />
			</FormField>
		</FormCard>

		<FormCard
			title="Address"
			description="Where the portal answers. Set a hostname, a dedicated port, or both."
			icon={Network}
		>
			<div class="grid grid-cols-1 sm:grid-cols-[1fr_9rem] gap-3">
				<FormField
					label="Hostname"
					id="portal-hostname"
					help="A DNS name pointed at this server. Empty answers on any hostname."
					error={addressError && !addressError.startsWith('Port') ? addressError : ''}
				>
					<Input
						id="portal-hostname"
						bind:value={hostname}
						class="font-mono"
						placeholder="registry.example.com"
					/>
				</FormField>
				<FormField
					label="Port"
					id="portal-port"
					help="Empty uses the app's port{mainPort ? ` (${mainPort})` : ''}."
					error={addressError.startsWith('Port') ? addressError : ''}
				>
					<Input
						id="portal-port"
						bind:value={portText}
						class="font-mono"
						inputmode="numeric"
						placeholder={mainPort ? String(mainPort) : 'app port'}
					/>
				</FormField>
			</div>
		</FormCard>

		<FormCard title="Access" description="What clients on this address are allowed to do." icon={Lock}>
			<div class="space-y-3">
				<FormField
					label="Allow push"
					horizontal
					help="Off makes the portal read-only. Pushes and uploads are rejected."
				>
					<Switch bind:checked={allowPush} />
				</FormField>

				<FormField
					label="Require sign-in"
					horizontal
					help="On refuses anonymous pulls, even from public repositories."
				>
					<Switch bind:checked={requireAuth} />
				</FormField>
			</div>
		</FormCard>

		<FormCard
			title="Image names"
			description="How names requested on this address map into {orgName}'s namespace."
			icon={Shuffle}
		>
			<div class="space-y-3">
				<FormField
					label="Map bare names"
					horizontal
					help="docker pull {previewAddress || 'portal-host'}/myimage resolves to {orgName}/myimage."
				>
					<Switch bind:checked={mapUnqualified} />
				</FormField>

				<FormField
					label="Rewrite rules"
					help="Optional regex rewrites of requested names, before bare-name mapping. First match wins; results must stay under {orgName}/."
				>
					<div class="space-y-2">
						{#each rules as rule, i (i)}
							<div class="flex items-center gap-2">
								<Input
									bind:value={rule.pattern}
									class="font-mono text-xs"
									placeholder="legacy/(.+)"
									aria-label="Rule pattern"
								/>
								<span class="text-xs text-muted-foreground shrink-0">→</span>
								<Input
									bind:value={rule.replace}
									class="font-mono text-xs"
									placeholder="{orgName}/$1"
									aria-label="Rule replacement"
								/>
								<Button
									variant="ghost"
									size="icon"
									class="h-8 w-8 shrink-0 text-destructive hover:text-destructive"
									onclick={() => (rules = rules.filter((_, idx) => idx !== i))}
								>
									<X class="h-3.5 w-3.5" />
								</Button>
							</div>
						{/each}
						<Button variant="outline" size="sm" onclick={() => (rules = [...rules, { pattern: '', replace: '' }])}>
							<Plus class="h-3.5 w-3.5 mr-1.5" />Add Rule
						</Button>
					</div>
				</FormField>
			</div>
		</FormCard>

		<div class="flex items-center justify-end gap-2 pt-1">
			<Button variant="outline" onclick={goBack}>Cancel</Button>
			<Button onclick={submit} disabled={submitting || !formValid}>
				{#if portal}
					{submitting ? 'Saving...' : 'Save Changes'}
				{:else}
					{submitting ? 'Creating...' : 'Create Portal'}
				{/if}
			</Button>
		</div>
	</div>

	<aside class="lg:sticky lg:top-20 rounded-xl border border-border/60 bg-card overflow-hidden">
		<div class="px-5 py-3.5 border-b border-border/40 bg-muted/20 flex items-center gap-2">
			<Globe class="h-4 w-4 text-primary" />
			<h3 class="text-sm font-semibold">This portal serves</h3>
		</div>
		<div class="p-5 space-y-4">
			{#if previewAddress}
				<div>
					<p class="detail-label mb-1">Address</p>
					<div class="flex items-center gap-1 min-w-0">
						<span class="font-mono text-sm font-medium truncate">{previewAddress}</span>
						<CopyButton text={previewAddress} label="Address copied" />
					</div>
					{#if portText.trim() === ''}
						<p class="text-xs text-muted-foreground/70 mt-1">
							On the app's own port{mainPort ? ` (${mainPort})` : ''}.
						</p>
					{:else if hostname.trim() === ''}
						<p class="text-xs text-muted-foreground/70 mt-1">Any hostname reaching port {portText}.</p>
					{/if}
				</div>

				<div class="space-y-2.5 text-[13px]">
					<div>
						<p class="detail-label mb-0.5">Web UI</p>
						<p class="font-mono break-all">{scheme}://{previewAddress}</p>
					</div>
					<div>
						<p class="detail-label mb-0.5">Pull</p>
						<p class="font-mono break-all">docker pull {previewAddress}/{previewImage}</p>
					</div>
					{#if allowPush}
						<div>
							<p class="detail-label mb-0.5">Push</p>
							<p class="font-mono break-all">docker push {previewAddress}/{previewImage}</p>
						</div>
					{/if}
				</div>
			{:else}
				<p class="text-[13px] text-muted-foreground">
					Set a hostname or port to see the resulting address.
				</p>
			{/if}

			<div class="flex flex-wrap gap-1 pt-1 border-t border-border/40">
				<Badge variant="outline" class="text-xs font-normal mt-2">Scoped to {orgName}</Badge>
				<Badge variant="outline" class="text-xs font-normal mt-2">{allowPush ? 'Push enabled' : 'Pull only'}</Badge>
				{#if requireAuth}
					<Badge variant="outline" class="text-xs font-normal mt-2">Sign-in required</Badge>
				{/if}
				{#if mapUnqualified}
					<Badge variant="outline" class="text-xs font-normal mt-2">Bare names</Badge>
				{/if}
				{#if ruleCount > 0}
					<Badge variant="outline" class="text-xs font-normal mt-2">{ruleCount} rewrite{ruleCount !== 1 ? 's' : ''}</Badge>
				{/if}
			</div>
		</div>
	</aside>
</div>

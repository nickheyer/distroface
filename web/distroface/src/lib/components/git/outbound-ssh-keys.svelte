<script lang="ts">
	import { toast } from 'svelte-sonner';
	import { rpcClient } from '$lib/api/rpc-client';
	import { Button } from '$lib/components/ui/button';
	import FormField from '$lib/components/form-field.svelte';
	import CopyButton from '$lib/components/copy-button.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import { Key, Loader, RefreshCw } from '$lib/icons';

	type Scope = 'instance' | 'organization' | 'repository';

	let {
		instance = '',
		organization = '',
		repository = '',
		namespace = '',
		name = '',
		regenerate = [],
		idPrefix = 'outbound-key',
		onRegenerated
	}: {
		instance?: string;
		organization?: string;
		repository?: string;
		namespace?: string;
		name?: string;
		regenerate?: Scope[];
		idPrefix?: string;
		onRegenerated?: (scope: Scope, publicKey: string) => void;
	} = $props();

	let fresh = $state<Partial<Record<Scope, string>>>({});
	let pending = $state<Scope | null>(null);
	let confirmOpen = $state(false);
	let working = $state(false);

	const rows = $derived(
		(
			[
				{
					scope: 'instance',
					label: 'Instance key',
					help: 'One install covers every repository on this instance',
					key: fresh.instance ?? instance
				},
				{
					scope: 'organization',
					label: 'Organization key',
					help: `One install covers every repository under ${namespace || 'the organization'}`,
					key: fresh.organization ?? organization
				},
				{
					scope: 'repository',
					label: 'Repository key',
					help: 'Covers this repository only, fits a deploy key',
					key: fresh.repository ?? repository
				}
			] as { scope: Scope; label: string; help: string; key: string }[]
		).filter((r) => r.key)
	);

	function ask(scope: Scope) {
		pending = scope;
		confirmOpen = true;
	}

	async function run() {
		if (!pending) return;
		const scope = pending;
		working = true;
		try {
			const resp = await rpcClient.git.generateMirrorKey({
				namespace: scope === 'instance' ? '' : namespace,
				name: scope === 'repository' ? name : ''
			});
			fresh = { ...fresh, [scope]: resp.publicKey };
			onRegenerated?.(scope, resp.publicKey);
			toast.success('SSH key regenerated');
			confirmOpen = false;
		} catch {
			// Error interceptor already toasted
		} finally {
			working = false;
		}
	}
</script>

{#each rows as row (row.scope)}
	<FormField label={row.label} id="{idPrefix}-{row.scope}" help={row.help}>
		<div class="flex items-start gap-2">
			<pre
				id="{idPrefix}-{row.scope}"
				class="code-inline min-w-0 flex-1 whitespace-pre-wrap">{row.key}</pre>
			<CopyButton text={row.key} label="Public key copied" />
			{#if regenerate.includes(row.scope)}
				<Button
					variant="outline"
					size="sm"
					onclick={() => ask(row.scope)}
					disabled={working}
					title="Regenerate key"
				>
					{#if working && pending === row.scope}<Loader class="animate-spin" />{:else}<RefreshCw
						/>{/if}
					Regenerate
				</Button>
			{/if}
		</div>
	</FormField>
{:else}
	<p class="text-sm text-muted-foreground">SSH keys are unavailable on this instance.</p>
{/each}

<ConfirmDialog
	bind:open={confirmOpen}
	title="Regenerate {pending ?? ''} key"
	confirmLabel={working ? 'Regenerating' : 'Regenerate key'}
	onConfirm={run}
	loading={working}
	icon={Key}
>
	{#snippet description()}
		The current key stops working at once. Every remote that trusts it rejects this
		{pending === 'repository' ? 'repository' : pending} until the new public key is installed there.
	{/snippet}
</ConfirmDialog>

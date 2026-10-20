<script lang="ts">
	import { resolve } from '$app/paths';
	import { AlertTriangle, Key, Loader, Terminal } from '$lib/icons';
	import { toast } from 'svelte-sonner';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { portalStore } from '$lib/stores/portal.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import CopyButton from '$lib/components/copy-button.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';

	const expiryOptions = [
		{ value: '7', label: '7 days' },
		{ value: '30', label: '30 days' },
		{ value: '90', label: '90 days' },
		{ value: '365', label: '1 year' },
		{ value: 'never', label: 'No expiration' }
	];

	let tokenName = $state('');
	let tokenExpiryOption = $state('never');
	let creating = $state(false);
	let newPlaintextToken = $state<string | null>(null);

	const listHref = resolve('/settings/tokens');
	const canCreate = $derived(authStore.hasPermission('tokens', 'create'));
	const registryHost = $derived(portalStore.host(configStore.publicHostname));
	const dockerLoginExample = $derived(
		`docker login ${registryHost} \\\n  -u ${authStore.user?.username} \\\n  -p ${newPlaintextToken ?? 'YOUR_TOKEN'}`
	);
	const apiCurlExample = $derived(
		`curl -X POST \\\n  ${portalStore.scheme()}://${registryHost}/distroface.v1.RepositoryService/ListRepositories \\\n  -H "authorization: Bearer ${newPlaintextToken ?? 'YOUR_TOKEN'}" \\\n  -H "content-type: application/json" \\\n  -d '{}'`
	);

	async function createToken() {
		if (!tokenName.trim() || creating) return;
		creating = true;
		const resp = await rpcClient.token
			.createAPIToken({
				name: tokenName.trim(),
				expiresInDays: tokenExpiryOption !== 'never' ? Number(tokenExpiryOption) : undefined
			})
			.catch(() => null);
		creating = false;
		if (!resp) return;
		newPlaintextToken = resp.plaintextToken;
		toast.success('Token created');
	}
</script>

<svelte:head>
	<title>Create token</title>
</svelte:head>

{#if !canCreate}
	<EmptyState
		icon={Key}
		message="You cannot create tokens"
		description="Ask an administrator for the tokens permission."
	>
		{#snippet actions()}
			<Button variant="outline" href={listHref}>Back to tokens</Button>
		{/snippet}
	</EmptyState>
{:else}
	<FormPage
		level="section"
		title={newPlaintextToken ? 'Token created' : 'Create token'}
		description={newPlaintextToken
			? 'Copy it now, it will not be shown again.'
			: 'Authenticates with the API and Docker registry.'}
		backHref={listHref}
		backLabel="API tokens"
	>
		{#if newPlaintextToken}
			<div class="space-y-4">
				<Alert variant="destructive">
					<AlertTriangle />
					<AlertDescription
						>Copy your token now. You won't be able to see it again.</AlertDescription
					>
				</Alert>

				<div class="flex items-center gap-2">
					<code class="code-inline min-w-0 flex-1">{newPlaintextToken}</code>
					<CopyButton text={newPlaintextToken} label="Token copied" />
				</div>

				<div class="box">
					<div class="box-header">
						<span class="flex items-center gap-2"
							><Terminal class="size-3.5 text-muted-foreground" />Docker registry login</span
						>
						<CopyButton text={dockerLoginExample} label="Command copied" />
					</div>
					<div class="p-4">
						<pre class="code-inline wrap-break-word whitespace-pre-wrap">{dockerLoginExample}</pre>
					</div>
				</div>

				<div class="box">
					<div class="box-header">
						<span class="flex items-center gap-2"
							><Terminal class="size-3.5 text-muted-foreground" />API request</span
						>
						<CopyButton text={apiCurlExample} label="Command copied" />
					</div>
					<div class="p-4">
						<pre class="code-inline wrap-break-word whitespace-pre-wrap">{apiCurlExample}</pre>
					</div>
				</div>
			</div>
		{:else}
			<FormSection title="Token" description="Name and lifetime of the token.">
				<FormField label="Token name" id="token-name" required span="half">
					<Input id="token-name" bind:value={tokenName} placeholder="CI/CD Pipeline" />
				</FormField>
				<FormField
					label="Expiration"
					id="token-expiry"
					help="No expiration means valid until revoked"
					span="half"
				>
					<Select
						type="single"
						value={tokenExpiryOption}
						onValueChange={(v) => {
							if (v) tokenExpiryOption = v;
						}}
					>
						<SelectTrigger class="w-full" id="token-expiry">
							{expiryOptions.find((o) => o.value === tokenExpiryOption)?.label}
						</SelectTrigger>
						<SelectContent>
							{#each expiryOptions as option (option.value)}
								<SelectItem value={option.value}>{option.label}</SelectItem>
							{/each}
						</SelectContent>
					</Select>
				</FormField>
			</FormSection>
		{/if}

		{#snippet footer()}
			{#if newPlaintextToken}
				<Button href={listHref}>Back to tokens</Button>
			{:else}
				<Button onclick={createToken} disabled={creating || !tokenName.trim()}>
					{#if creating}<Loader class="animate-spin" />{/if}
					{creating ? 'Creating' : 'Create token'}
				</Button>
				<Button variant="ghost-muted" href={listHref}>Cancel</Button>
			{/if}
		{/snippet}
	</FormPage>
{/if}

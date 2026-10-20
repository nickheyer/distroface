<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { AlertTriangle, Loader2 } from '@lucide/svelte';
	import { toast } from 'svelte-sonner';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { portalStore } from '$lib/stores/portal.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Switch } from '$lib/components/ui/switch';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import CopyButton from '$lib/components/copy-button.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import { CredentialScopeType } from '$lib/proto/distroface/v1/types_pb';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const fullName = $derived(`${namespace}/${name}`);
	const listHref = $derived(resolve('/[namespace]/[name]/settings/tokens', { namespace, name }));

	let tokenName = $state('');
	let readOnly = $state(true);
	let expiry = $state('never');
	let creating = $state(false);
	let plaintext = $state<string | null>(null);

	const expiryOptions = [
		{ value: '7', label: '7 days' },
		{ value: '30', label: '30 days' },
		{ value: '90', label: '90 days' },
		{ value: '365', label: '1 year' },
		{ value: 'never', label: 'No expiration' }
	];

	const host = $derived(portalStore.host(configStore.publicHostname));
	const scheme = $derived(portalStore.scheme());
	const username = $derived(authStore.user?.username ?? 'USER');
	const cloneExample = $derived(
		`git clone ${scheme}://${username}:${plaintext ?? 'YOUR_TOKEN'}@${host}/${fullName}.git`
	);
	const helperExample = $derived(
		`dfcli --server ${scheme}://${host} login --token ${plaintext ?? 'YOUR_TOKEN'}\ngit config --global credential.helper "dfcli git credential"`
	);
	const dockerExample = $derived(
		`docker login ${host} -u ${username} -p ${plaintext ?? 'YOUR_TOKEN'}`
	);
	const examples = $derived([
		{ title: 'Clone over https', text: cloneExample },
		{ title: 'Log in with dfcli and the git credential helper', text: helperExample },
		{ title: 'Push images to linked packages', text: dockerExample }
	]);

	async function create() {
		if (!tokenName.trim() || creating) return;
		creating = true;
		const resp = await rpcClient.token
			.createAPIToken({
				name: tokenName.trim(),
				expiresInDays: expiry === 'never' ? undefined : Number(expiry),
				scope: { type: CredentialScopeType.GIT_REPO, id: fullName, readOnly }
			})
			.then(
				(r) => r,
				() => null
			);
		creating = false;
		if (!resp) return;
		plaintext = resp.plaintextToken;
		toast.success('Deploy token created');
	}
</script>

<svelte:head>
	<title>New deploy token - {namespace}/{name}</title>
</svelte:head>

{#if plaintext}
	<FormPage
		level="section"
		title="Token created"
		description="Copy it now, it will not be shown again."
		backHref={listHref}
		backLabel="Deploy tokens"
	>
		<div class="space-y-4">
			<Alert variant="destructive">
				<AlertTriangle />
				<AlertDescription>Copy your token now. You won't be able to see it again.</AlertDescription>
			</Alert>
			<div class="flex items-center gap-2">
				<code class="code-inline min-w-0 flex-1">{plaintext}</code>
				<CopyButton text={plaintext} label="Token copied" />
			</div>
			{#each examples as block (block.title)}
				<div class="space-y-2">
					<p class="eyebrow">{block.title}</p>
					<div class="relative">
						<pre class="code-inline pr-10 leading-relaxed whitespace-pre-wrap">{block.text}</pre>
						<div class="absolute top-1.5 right-1.5">
							<CopyButton text={block.text} label="Command copied" />
						</div>
					</div>
				</div>
			{/each}
		</div>

		{#snippet footer()}
			<Button href={listHref}>Back to deploy tokens</Button>
		{/snippet}
	</FormPage>
{:else}
	<FormPage
		level="section"
		title="New deploy token"
		description="Scoped to {fullName} and its linked packages, owned by you."
		backHref={listHref}
		backLabel="Deploy tokens"
	>
		<FormSection title="Token" description="Name, access level, and lifetime.">
			<FormField label="Name" id="deploy-token-name" required span="half">
				<Input id="deploy-token-name" bind:value={tokenName} placeholder="CI publish" />
			</FormField>
			<FormField
				label="Read only"
				layout="toggle"
				help="Off allows pushes to the repository and its packages"
			>
				<Switch bind:checked={readOnly} />
			</FormField>
			<FormField
				label="Expiration"
				id="deploy-token-expiry"
				help="No expiration means valid until revoked"
				span="half"
			>
				<Select
					type="single"
					value={expiry}
					onValueChange={(v) => {
						if (v) expiry = v;
					}}
				>
					<SelectTrigger id="deploy-token-expiry" class="w-full"
						>{expiryOptions.find((o) => o.value === expiry)?.label}</SelectTrigger
					>
					<SelectContent>
						{#each expiryOptions as o (o.value)}
							<SelectItem value={o.value}>{o.label}</SelectItem>
						{/each}
					</SelectContent>
				</Select>
			</FormField>
		</FormSection>

		{#snippet footer()}
			<Button onclick={create} disabled={creating || !tokenName.trim()}>
				{#if creating}<Loader2 class="animate-spin" />{/if}
				{creating ? 'Creating' : 'Create token'}
			</Button>
			<Button variant="ghost-muted" href={listHref}>Cancel</Button>
		{/snippet}
	</FormPage>
{/if}

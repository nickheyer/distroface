<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { Loader2 } from '@lucide/svelte';
	import { toast } from 'svelte-sonner';
	import { rpcClient } from '$lib/api/rpc-client';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Switch } from '$lib/components/ui/switch';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import { SSHKeyUsage } from '$lib/proto/distroface/v1/keys_pb';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const listHref = $derived(resolve('/[namespace]/[name]/settings/keys', { namespace, name }));

	let title = $state('');
	let publicKey = $state('');
	let readOnly = $state(true);
	let expiry = $state('never');
	let adding = $state(false);

	const expiryOptions = [
		{ value: '30', label: '30 days' },
		{ value: '90', label: '90 days' },
		{ value: '365', label: '1 year' },
		{ value: 'never', label: 'No expiration' }
	];

	const keyLooksValid = $derived(
		/^(ssh-(rsa|ed25519|dss)|ecdsa-sha2-nistp\d+|sk-)\S*\s+\S+/.test(publicKey.trim())
	);

	async function add() {
		if (!title.trim() || !keyLooksValid || adding) return;
		adding = true;
		try {
			await rpcClient.key.addSSHKey({
				namespace,
				name,
				title: title.trim(),
				publicKey: publicKey.trim(),
				usage: SSHKeyUsage.SSH_KEY_USAGE_AUTH,
				readOnly,
				expiresInDays: expiry === 'never' ? undefined : Number(expiry)
			});
			toast.success('Deploy key added');
			await goto(resolve('/[namespace]/[name]/settings/keys', { namespace, name }));
		} catch {
			// Error interceptor already toasted
		} finally {
			adding = false;
		}
	}
</script>

<svelte:head>
	<title>Add deploy key - {namespace}/{name}</title>
</svelte:head>

<FormPage
	level="section"
	title="Add deploy key"
	description="Paste the public half of an SSH key. It can clone this repository and nothing else."
	backHref={listHref}
	backLabel="Deploy keys"
>
	<FormSection title="Key" description="The public half and what it may do.">
		<FormField label="Title" id="key-title" required span="half">
			<Input id="key-title" bind:value={title} placeholder="Production deploy" />
		</FormField>
		<FormField
			label="Public key"
			id="key-public"
			required
			help="Starts with ssh-ed25519, ssh-rsa, or ecdsa-sha2"
			error={publicKey && !keyLooksValid ? 'This does not look like an SSH public key' : ''}
		>
			<Textarea
				id="key-public"
				bind:value={publicKey}
				rows={5}
				class="font-mono"
				placeholder="ssh-ed25519 AAAA"
				spellcheck={false}
				aria-invalid={!!publicKey && !keyLooksValid}
			/>
		</FormField>
		<FormField
			label="Allow write access"
			layout="toggle"
			help="Lets this key push to the repository"
		>
			<Switch checked={!readOnly} onCheckedChange={(v) => (readOnly = !v)} />
		</FormField>
		<FormField label="Expiration" id="key-expiry" span="half">
			<Select
				type="single"
				value={expiry}
				onValueChange={(v) => {
					if (v) expiry = v;
				}}
			>
				<SelectTrigger id="key-expiry" class="w-full"
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
		<Button onclick={add} disabled={adding || !title.trim() || !keyLooksValid}>
			{#if adding}<Loader2 class="animate-spin" />{/if}
			{adding ? 'Adding' : 'Add key'}
		</Button>
		<Button variant="ghost-muted" href={listHref}>Cancel</Button>
	{/snippet}
</FormPage>

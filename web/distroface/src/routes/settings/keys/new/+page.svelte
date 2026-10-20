<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { Fingerprint, KeyRound, Loader2 } from '@lucide/svelte';
	import { toast } from 'svelte-sonner';
	import { rpcClient } from '$lib/api/rpc-client';
	import { configStore } from '$lib/stores/config.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import * as RadioGroup from '$lib/components/ui/radio-group';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import { SSHKeyUsage } from '$lib/proto/distroface/v1/keys_pb';

	type KeyKind = 'ssh' | 'gpg';
	const usageOptions = [
		{ value: SSHKeyUsage.SSH_KEY_USAGE_AUTH, label: 'Authentication' },
		{ value: SSHKeyUsage.SSH_KEY_USAGE_SIGNING, label: 'Signing' },
		{ value: SSHKeyUsage.SSH_KEY_USAGE_BOTH, label: 'Authentication and signing' }
	];
	const expiryOptions = [
		{ value: 'never', label: 'No expiration' },
		{ value: '30', label: '30 days' },
		{ value: '90', label: '90 days' },
		{ value: '365', label: '1 year' }
	];

	let kind = $state<KeyKind>(page.url.searchParams.get('type') === 'gpg' ? 'gpg' : 'ssh');
	let sshTitle = $state('');
	let sshText = $state('');
	let sshUsage = $state<SSHKeyUsage>(SSHKeyUsage.SSH_KEY_USAGE_AUTH);
	let sshExpiry = $state('never');
	let gpgArmored = $state('');
	let adding = $state(false);

	const listHref = resolve('/settings/keys');
	const sshTransport = $derived(configStore.sshEnabled);
	const sshValid = $derived(
		/^(ssh-(rsa|ed25519|dss)|ecdsa-sha2-nistp\d+|sk-(ssh-ed25519|ecdsa-sha2-nistp256)@openssh\.com)\s+[A-Za-z0-9+/=]+/.test(
			sshText.trim()
		)
	);
	const gpgValid = $derived(gpgArmored.includes('-----BEGIN PGP PUBLIC KEY BLOCK-----'));
	const canSubmit = $derived(kind === 'ssh' ? sshValid : gpgValid);

	function usageLabel(u: SSHKeyUsage): string {
		return usageOptions.find((o) => o.value === u)?.label ?? 'Authentication';
	}

	function sshPasted() {
		if (sshTitle.trim()) return;
		const parts = sshText.trim().split(/\s+/);
		if (parts.length >= 3) sshTitle = parts.slice(2).join(' ');
	}

	async function add() {
		if (!canSubmit || adding) return;
		adding = true;
		if (kind === 'ssh') {
			const resp = await rpcClient.key
				.addSSHKey({
					title: sshTitle.trim() || 'SSH key',
					publicKey: sshText.trim(),
					usage: sshUsage,
					expiresInDays: sshExpiry !== 'never' ? Number(sshExpiry) : undefined
				})
				.catch(() => null);
			if (!resp) {
				adding = false;
				return;
			}
			toast.success('SSH key added');
			await goto(resolve('/settings/keys'));
		} else {
			const resp = await rpcClient.key
				.addGPGKey({ armoredPublicKey: gpgArmored.trim() })
				.catch(() => null);
			if (!resp) {
				adding = false;
				return;
			}
			toast.success('GPG key added');
			if (resp.key && resp.challenge)
				await goto(resolve('/settings/keys/[id]/verify', { id: resp.key.id }));
			else await goto(resolve('/settings/keys'));
		}
		adding = false;
	}
</script>

<svelte:head>
	<title>Add key</title>
</svelte:head>

<FormPage
	level="section"
	title="Add key"
	description="Paste a public key to attach it to your account."
	backHref={listHref}
	backLabel="Keys"
>
	<FormSection title="Key type" description="What the key is used for.">
		<RadioGroup.Root
			value={kind}
			onValueChange={(v) => {
				if (v) kind = v as KeyKind;
			}}
			class="gap-0 divide-y rounded-lg border"
			aria-label="Key type"
		>
			<label class="flex cursor-pointer items-start gap-3 px-4 py-3" for="key-kind-ssh">
				<RadioGroup.Item value="ssh" id="key-kind-ssh" class="mt-0.5" />
				<KeyRound class="mt-0.5 size-4 shrink-0 text-muted-foreground" />
				<span class="min-w-0">
					<span class="block text-sm font-medium">SSH key</span>
					<span class="block text-xs text-muted-foreground"
						>{sshTransport
							? 'Authenticates git over SSH or signs commits'
							: 'Authenticates clones and pushes over SSH'}</span
					>
				</span>
			</label>
			<label class="flex cursor-pointer items-start gap-3 px-4 py-3" for="key-kind-gpg">
				<RadioGroup.Item value="gpg" id="key-kind-gpg" class="mt-0.5" />
				<Fingerprint class="mt-0.5 size-4 shrink-0 text-muted-foreground" />
				<span class="min-w-0">
					<span class="block text-sm font-medium">GPG key</span>
					<span class="block text-xs text-muted-foreground"
						>Marks your signed commits and tags as verified</span
					>
				</span>
			</label>
		</RadioGroup.Root>
	</FormSection>

	{#if kind === 'ssh'}
		<FormSection
			title="Key"
			description="Paste the contents of your public key file, usually id_ed25519.pub."
		>
			<FormField
				label="Public key"
				id="ssh-key"
				required
				error={sshText && !sshValid ? 'Expected an OpenSSH public key' : undefined}
			>
				<Textarea
					id="ssh-key"
					bind:value={sshText}
					oninput={sshPasted}
					rows={4}
					class="font-mono"
					placeholder="ssh-ed25519 AAAA you@host"
					spellcheck={false}
					aria-invalid={!!sshText && !sshValid}
				/>
			</FormField>
			<FormField label="Title" id="ssh-title" help="Defaults to the key comment" span="half">
				<Input id="ssh-title" bind:value={sshTitle} placeholder="Work laptop" />
			</FormField>
			<FormField label="Usage" id="ssh-usage" span="half">
				<Select
					type="single"
					value={String(sshUsage)}
					onValueChange={(v) => {
						if (v) sshUsage = Number(v) as SSHKeyUsage;
					}}
				>
					<SelectTrigger id="ssh-usage" class="w-full">{usageLabel(sshUsage)}</SelectTrigger>
					<SelectContent>
						{#each usageOptions as o (o.value)}
							<SelectItem value={String(o.value)}>{o.label}</SelectItem>
						{/each}
					</SelectContent>
				</Select>
			</FormField>
			<FormField label="Expiration" id="ssh-expiry" span="half">
				<Select
					type="single"
					value={sshExpiry}
					onValueChange={(v) => {
						if (v) sshExpiry = v;
					}}
				>
					<SelectTrigger id="ssh-expiry" class="w-full"
						>{expiryOptions.find((o) => o.value === sshExpiry)?.label}</SelectTrigger
					>
					<SelectContent>
						{#each expiryOptions as o (o.value)}
							<SelectItem value={o.value}>{o.label}</SelectItem>
						{/each}
					</SelectContent>
				</Select>
			</FormField>
		</FormSection>
	{:else}
		<FormSection
			title="Armored public key"
			description="Export with gpg --armor --export KEYID and paste the block."
		>
			<FormField
				label="Key"
				id="gpg-key"
				required
				error={gpgArmored && !gpgValid ? 'Expected a PGP PUBLIC KEY BLOCK' : undefined}
			>
				<Textarea
					id="gpg-key"
					bind:value={gpgArmored}
					rows={10}
					class="font-mono"
					placeholder="-----BEGIN PGP PUBLIC KEY BLOCK-----"
					spellcheck={false}
					aria-invalid={!!gpgArmored && !gpgValid}
				/>
			</FormField>
		</FormSection>
	{/if}

	{#snippet footer()}
		<Button onclick={add} disabled={adding || !canSubmit}>
			{#if adding}<Loader2 class="animate-spin" />{/if}
			{adding ? 'Adding' : 'Add key'}
		</Button>
		<Button variant="ghost-muted" href={listHref}>Cancel</Button>
	{/snippet}
</FormPage>

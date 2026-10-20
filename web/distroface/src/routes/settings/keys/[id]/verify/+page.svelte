<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { ConnectError } from '@connectrpc/connect';
	import { CircleAlert, Fingerprint, Loader2, Terminal } from '@lucide/svelte';
	import { toast } from 'svelte-sonner';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { errText } from '$lib/act.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import CopyButton from '$lib/components/copy-button.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import type { GPGKey } from '$lib/proto/distroface/v1/keys_pb';

	const keyId = $derived(page.params.id ?? '');
	const listHref = resolve('/settings/keys');

	let key = $state<GPGKey | null>(null);
	let challenge = $state('');
	let signature = $state('');
	let loading = $state(true);
	let verifying = $state(false);
	let verifyError = $state('');

	const signCommand = $derived(
		`printf '%s' '${challenge}' | gpg --armor --detach-sign --default-key ${key?.keyId ?? 'KEYID'}`
	);

	// Walks every page of the caller's keys until the id matches
	async function findKey(id: string): Promise<GPGKey | null> {
		let pageToken = '';
		do {
			const resp = await rpcClient.key.listGPGKeys(
				{ page: { pageSize: 100, pageToken } },
				silentCallOptions
			);
			const hit = resp.keys.find((k) => k.id === id);
			if (hit) return hit;
			pageToken = resp.page?.nextPageToken ?? '';
		} while (pageToken);
		return null;
	}

	// An empty signature makes the server answer with a fresh challenge
	async function loadChallenge(id: string) {
		try {
			const resp = await rpcClient.key.verifyGPGKey(
				{ id, armoredSignature: '' },
				silentCallOptions
			);
			if (resp.key?.verified) {
				toast.success('Key already verified');
				await goto(listHref);
			}
		} catch (err) {
			const msg = err instanceof ConnectError ? err.rawMessage : '';
			const idx = msg.lastIndexOf(': ');
			if (idx > -1 && msg.includes('challenge')) challenge = msg.slice(idx + 2).trim();
			else verifyError = errText(err);
		}
	}

	async function submit() {
		if (!key || !signature.trim() || verifying) return;
		verifying = true;
		verifyError = '';
		try {
			await rpcClient.key.verifyGPGKey(
				{ id: key.id, armoredSignature: signature.trim() },
				silentCallOptions
			);
			toast.success('GPG key verified');
			await goto(listHref);
		} catch (err) {
			verifyError = errText(err);
		} finally {
			verifying = false;
		}
	}

	onMount(async () => {
		try {
			key = await findKey(keyId);
			if (key && !key.verified) await loadChallenge(key.id);
			else if (key?.verified) {
				toast.success('Key already verified');
				await goto(listHref);
			}
		} finally {
			loading = false;
		}
	});
</script>

<svelte:head>
	<title>Verify GPG key</title>
</svelte:head>

{#if loading}
	<div class="space-y-4">
		<Skeleton class="h-5 w-40" />
		<Skeleton class="h-64 w-full rounded-lg" />
	</div>
{:else if !key}
	<EmptyState icon={Fingerprint} message="Key not found" description="It may have been removed.">
		{#snippet actions()}
			<Button variant="outline" href={listHref}>Back to keys</Button>
		{/snippet}
	</EmptyState>
{:else}
	<FormPage
		level="section"
		title="Verify GPG key"
		description="Sign the challenge with {key.keyId} to prove you hold its private half."
		backHref={listHref}
		backLabel="Keys"
	>
		<Alert variant="warning">
			<CircleAlert />
			<AlertDescription
				>Until verified, commits signed with {key.keyId} show as unverified.</AlertDescription
			>
		</Alert>

		<FormSection title="Challenge" description="Run this on the machine that holds the key.">
			<div class="space-y-1.5">
				<span class="eyebrow flex items-center gap-1.5"
					><Terminal class="size-3.5" />Sign the challenge</span
				>
				<div class="flex items-start gap-2">
					<pre class="code-inline min-w-0 flex-1 whitespace-pre-wrap">{signCommand}</pre>
					<CopyButton text={signCommand} label="Command copied" />
				</div>
			</div>
		</FormSection>

		<FormSection title="Signature" description="Paste the armored output of the command.">
			<FormField label="Armored signature" id="gpg-sig" required error={verifyError || undefined}>
				<Textarea
					id="gpg-sig"
					bind:value={signature}
					rows={8}
					class="font-mono"
					placeholder="-----BEGIN PGP SIGNATURE-----"
					spellcheck={false}
					aria-invalid={!!verifyError}
				/>
			</FormField>
		</FormSection>

		{#snippet footer()}
			<Button onclick={submit} disabled={verifying || !signature.trim() || !challenge}>
				{#if verifying}<Loader2 class="animate-spin" />{/if}
				{verifying ? 'Verifying' : 'Verify key'}
			</Button>
			<Button variant="ghost-muted" href={listHref}>Later</Button>
		{/snippet}
	</FormPage>
{/if}

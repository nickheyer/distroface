<script lang="ts">
	import { Button } from '$lib/components/ui/button';
	import PemInput from '$lib/components/pem-input.svelte';
	import FormPanel from '$lib/components/form-panel.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import { errText } from '$lib/act.svelte';

	let {
		open = $bindable(false),
		title,
		description = '',
		onSubmit
	}: {
		open: boolean;
		title: string;
		description?: string;
		onSubmit: (certPem: string, keyPem: string) => Promise<void>;
	} = $props();

	let certPem = $state('');
	let keyPem = $state('');
	let busy = $state(false);
	let error = $state('');

	$effect(() => {
		if (open) {
			certPem = '';
			keyPem = '';
			error = '';
		}
	});

	async function submit() {
		busy = true;
		error = '';
		try {
			await onSubmit(certPem, keyPem);
			open = false;
		} catch (err) {
			error = errText(err);
		} finally {
			busy = false;
		}
	}
</script>

<FormPanel bind:open {title} {description}>
	<div class="space-y-4">
		<FormField label="Certificate (PEM)" id="upload-cert-pem" required help="Full chain leaf first, paste or upload">
			<PemInput id="upload-cert-pem" bind:value={certPem} rows={6} placeholder="-----BEGIN CERTIFICATE-----" disabled={busy} />
		</FormField>
		<FormField label="Private key (PEM)" id="upload-key-pem" required help="Paste or upload, stored server side">
			<PemInput id="upload-key-pem" bind:value={keyPem} rows={6} placeholder="-----BEGIN PRIVATE KEY-----" secret disabled={busy} />
		</FormField>
		{#if error}
			<p class="text-[13px] text-destructive">{error}</p>
		{/if}
	</div>

	{#snippet footer()}
		<Button variant="outline" onclick={() => (open = false)}>Cancel</Button>
		<Button onclick={submit} disabled={busy || !certPem.trim() || !keyPem.trim()}>
			{busy ? 'Uploading...' : 'Upload'}
		</Button>
	{/snippet}
</FormPanel>

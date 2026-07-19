<script lang="ts">
	import { Button } from '$lib/components/ui/button';
	import { Textarea } from '$lib/components/ui/textarea';
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
		<FormField label="Certificate (PEM)" id="upload-cert-pem" required help="Full chain leaf first">
			<Textarea
				id="upload-cert-pem"
				bind:value={certPem}
				class="font-mono text-xs"
				rows={6}
				placeholder="-----BEGIN CERTIFICATE-----"
			/>
		</FormField>
		<FormField label="Private key (PEM)" id="upload-key-pem" required>
			<Textarea
				id="upload-key-pem"
				bind:value={keyPem}
				class="font-mono text-xs"
				rows={6}
				placeholder="-----BEGIN PRIVATE KEY-----"
			/>
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

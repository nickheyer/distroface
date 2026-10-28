<script lang="ts">
	import { Button } from '$lib/components/ui/button';
	import { Loader } from '$lib/icons';
	import PemInput from '$lib/components/pem-input.svelte';
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

{#if open}
	<div class="box p-4">
		<div class="space-y-4">
			<div>
				<p class="text-sm font-semibold">{title}</p>
				{#if description}
					<p class="mt-0.5 text-xs text-muted-foreground">{description}</p>
				{/if}
			</div>
			<div class="grid gap-4 md:grid-cols-2">
				<FormField
					label="Certificate (PEM)"
					id="upload-cert-pem"
					required
					help="Full chain leaf first, paste or upload"
				>
					<PemInput
						id="upload-cert-pem"
						bind:value={certPem}
						rows={6}
						placeholder="-----BEGIN CERTIFICATE-----"
						disabled={busy}
					/>
				</FormField>
				<FormField
					label="Private key (PEM)"
					id="upload-key-pem"
					required
					help="Paste or upload, stored server side"
				>
					<PemInput
						id="upload-key-pem"
						bind:value={keyPem}
						rows={6}
						placeholder="-----BEGIN PRIVATE KEY-----"
						secret
						disabled={busy}
					/>
				</FormField>
			</div>
			{#if error}
				<p class="text-xs text-destructive" role="alert">{error}</p>
			{/if}
			<div class="flex items-center justify-end gap-2 border-t pt-4">
				<Button variant="outline" onclick={() => (open = false)} disabled={busy}>Cancel</Button>
				<Button onclick={submit} disabled={busy || !certPem.trim() || !keyPem.trim()}>
					{#if busy}<Loader class="animate-spin" />{/if}
					{busy ? 'Uploading' : 'Upload'}
				</Button>
			</div>
		</div>
	</div>
{/if}

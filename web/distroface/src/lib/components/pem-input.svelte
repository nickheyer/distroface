<script lang="ts">
	import { Button } from '$lib/components/ui/button';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Upload } from '@lucide/svelte';
	import { cn } from '$lib/utils.js';

	// Paste or upload, either way the bound value is plain pem text
	let {
		id,
		value = $bindable(''),
		placeholder = '',
		rows = 5,
		disabled = false,
		secret = false,
		class: className = ''
	}: {
		id: string;
		value: string;
		placeholder?: string;
		rows?: number;
		disabled?: boolean;
		secret?: boolean;
		class?: string;
	} = $props();

	const accept = '.pem,.crt,.cer,.key,.csr,.txt,application/x-pem-file,application/pkcs8,application/x-x509-ca-cert';

	// Password managers must leave key material alone
	const secretAttrs = $derived(
		secret
			? { autocomplete: 'new-password' as const, 'data-1p-ignore': true, 'data-lpignore': 'true', 'data-bwignore': true }
			: {}
	);

	let picker = $state<HTMLInputElement | null>(null);
	let fileName = $state('');
	let readError = $state('');
	let dragging = $state(false);

	async function load(file: File | undefined) {
		if (!file) return;
		readError = '';
		try {
			value = (await file.text()).replace(/\r\n/g, '\n');
			fileName = file.name;
		} catch {
			readError = `Could not read ${file.name}`;
		}
	}

	function onPick(e: Event) {
		const input = e.currentTarget as HTMLInputElement;
		load(input.files?.[0]);
		// Same file picked twice still fires change
		input.value = '';
	}

	function onDragOver(e: DragEvent) {
		e.preventDefault();
		if (!disabled) dragging = true;
	}

	function onDrop(e: DragEvent) {
		e.preventDefault();
		dragging = false;
		if (!disabled) load(e.dataTransfer?.files?.[0]);
	}
</script>

<div class="space-y-1.5">
	<Textarea
		{id}
		bind:value
		{rows}
		{placeholder}
		{disabled}
		class={cn('font-mono text-xs', dragging && 'ring-[3px] ring-ring/50', className)}
		oninput={() => (fileName = '')}
		ondragover={onDragOver}
		ondragleave={() => (dragging = false)}
		ondrop={onDrop}
		{...secretAttrs}
	/>
	<div class="flex items-center gap-2 text-xs text-muted-foreground min-w-0">
		<Button type="button" variant="outline" size="sm" class="h-7 gap-1.5 shrink-0" {disabled} onclick={() => picker?.click()}>
			<Upload class="h-3.5 w-3.5" />
			Upload file
		</Button>
		<input bind:this={picker} type="file" {accept} class="sr-only" tabindex={-1} onchange={onPick} />
		{#if readError}
			<span class="text-destructive truncate">{readError}</span>
		{:else if fileName}
			<span class="truncate">Loaded {fileName}</span>
		{:else}
			<span class="truncate">or drop a file on the field</span>
		{/if}
	</div>
</div>

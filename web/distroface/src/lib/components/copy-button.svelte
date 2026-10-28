<script lang="ts">
	import { Check, Copy } from '$lib/icons';
	import { Button } from '$lib/components/ui/button';
	import { toast } from 'svelte-sonner';

	let { text, label = 'Copied' }: { text: string; label?: string } = $props();

	let copied = $state(false);
	let timeout: ReturnType<typeof setTimeout> | undefined;

	function copy(e: MouseEvent) {
		e.stopPropagation();
		navigator.clipboard.writeText(text).then(() => {
			copied = true;
			toast.success(label);
			clearTimeout(timeout);
			timeout = setTimeout(() => (copied = false), 2000);
		});
	}
</script>

<Button variant="ghost-muted" size="icon-sm" class="shrink-0" onclick={copy} aria-label="Copy">
	{#if copied}
		<Check class="text-success" />
	{:else}
		<Copy />
	{/if}
</Button>

<script lang="ts">
	import { Check, Copy } from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { toast } from 'svelte-sonner';

	let { text, label = 'Copied!' }: { text: string; label?: string } = $props();

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

<Button variant="ghost" size="icon" class="h-7 w-7 shrink-0" onclick={copy}>
	{#if copied}
		<Check class="h-3.5 w-3.5 text-success" />
	{:else}
		<Copy class="h-3.5 w-3.5 text-muted-foreground" />
	{/if}
</Button>

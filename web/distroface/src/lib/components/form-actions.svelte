<script lang="ts">
	import type { Snippet } from 'svelte';
	import { cn } from '$lib/utils';

	let { children, class: className }: { children: Snippet; class?: string } = $props();
</script>

<div
	data-slot="form-actions"
	class={cn(
		'mt-6 flex flex-row-reverse flex-wrap items-center justify-start gap-2 border-t pt-5',
		className
	)}
>
	{@render children()}
</div>

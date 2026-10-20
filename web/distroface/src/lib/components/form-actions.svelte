<script lang="ts">
	import type { Snippet } from 'svelte';
	import { cn } from '$lib/utils';

	let { children, class: className }: { children: Snippet; class?: string } = $props();
</script>

<div class={cn('form-split', className)}>
	<div class="hidden lg:block" aria-hidden="true"></div>
	<div class="flex flex-wrap items-center gap-2">
		{@render children()}
	</div>
</div>

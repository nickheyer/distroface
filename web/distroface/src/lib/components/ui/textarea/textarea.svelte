<script lang="ts">
	import { cn, type WithElementRef, type WithoutChildren } from '$lib/utils.js';
	import type { HTMLTextareaAttributes } from 'svelte/elements';

	let {
		ref = $bindable(null),
		value = $bindable(),
		class: className,
		...restProps
	}: WithoutChildren<WithElementRef<HTMLTextareaAttributes>> = $props();
</script>

<textarea
	bind:this={ref}
	data-slot="textarea"
	class={cn(
		'flex field-sizing-content min-h-20 w-full rounded-md border border-input bg-field px-2.5 py-1.5 text-base leading-relaxed text-foreground shadow-[inset_0_1px_1px_0_rgb(0_0_0/0.04)] transition-[border-color,box-shadow] duration-150 outline-none selection:bg-primary/20 hover:border-border-strong focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/30 disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive aria-invalid:ring-2 aria-invalid:ring-destructive/25 md:text-sm dark:shadow-[inset_0_1px_1px_0_rgb(0_0_0/0.2)]',
		className
	)}
	bind:value
	{...restProps}></textarea>

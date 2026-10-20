<script lang="ts" module>
	export const inputClass =
		'flex h-8 w-full min-w-0 rounded-md border border-input bg-field px-2.5 py-1 text-base text-foreground shadow-[inset_0_1px_1px_0_rgb(0_0_0/0.04)] outline-none transition-[border-color,box-shadow] duration-150 selection:bg-primary/20 hover:border-border-strong focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/30 disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive aria-invalid:ring-2 aria-invalid:ring-destructive/25 md:text-sm dark:shadow-[inset_0_1px_1px_0_rgb(0_0_0/0.2)]';
</script>

<script lang="ts">
	import type { HTMLInputAttributes, HTMLInputTypeAttribute } from 'svelte/elements';
	import { cn, type WithElementRef } from '$lib/utils.js';

	type InputType = Exclude<HTMLInputTypeAttribute, 'file'>;

	type Props = WithElementRef<
		Omit<HTMLInputAttributes, 'type'> &
			({ type: 'file'; files?: FileList } | { type?: InputType; files?: undefined })
	>;

	let {
		ref = $bindable(null),
		value = $bindable(),
		type,
		files = $bindable(),
		class: className,
		...restProps
	}: Props = $props();
</script>

{#if type === 'file'}
	<input
		bind:this={ref}
		data-slot="input"
		class={cn(
			inputClass,
			'pt-1.5 text-sm file:mr-3 file:rounded-sm file:border-0 file:bg-muted file:px-2 file:py-0.5 file:text-xs file:font-medium file:text-foreground',
			className
		)}
		type="file"
		bind:files
		bind:value
		{...restProps}
	/>
{:else}
	<input
		bind:this={ref}
		data-slot="input"
		class={cn(inputClass, className)}
		{type}
		bind:value
		{...restProps}
	/>
{/if}

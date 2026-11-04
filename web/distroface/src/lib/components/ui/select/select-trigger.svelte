<script lang="ts">
	import { Select as SelectPrimitive } from 'bits-ui';
	import { ChevronDown as ChevronDownIcon } from '$lib/icons';
	import { cn, type WithoutChild } from '$lib/utils.js';

	let {
		ref = $bindable(null),
		class: className,
		children,
		size = 'default',
		...restProps
	}: WithoutChild<SelectPrimitive.TriggerProps> & {
		size?: 'sm' | 'default';
	} = $props();
</script>

<SelectPrimitive.Trigger
	bind:ref
	data-slot="select-trigger"
	data-size={size}
	class={cn(
		"flex w-fit cursor-pointer items-center justify-between gap-2 rounded-md border border-input bg-field px-2.5 text-sm whitespace-nowrap text-foreground shadow-[inset_0_1px_1px_0_rgb(0_0_0/0.04)] transition-[border-color,box-shadow] duration-150 outline-none select-none hover:border-border-strong focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/30 disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive aria-invalid:ring-2 aria-invalid:ring-destructive/25 data-[placeholder]:text-muted-foreground data-[size=default]:h-8 data-[size=sm]:h-7 data-[size=sm]:text-xs *:data-[slot=select-value]:line-clamp-1 *:data-[slot=select-value]:flex *:data-[slot=select-value]:items-center *:data-[slot=select-value]:gap-2 dark:shadow-[inset_0_1px_1px_0_rgb(0_0_0/0.2)] [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4 [&_svg:not([class*='text-'])]:text-muted-foreground",
		className
	)}
	{...restProps}
>
	{@render children?.()}
	<ChevronDownIcon class="size-3.5 opacity-70" />
</SelectPrimitive.Trigger>

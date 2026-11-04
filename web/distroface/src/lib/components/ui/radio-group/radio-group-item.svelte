<script lang="ts">
	import { RadioGroup as RadioGroupPrimitive } from 'bits-ui';
	import { cn, type WithoutChildrenOrChild } from '$lib/utils.js';

	let {
		ref = $bindable(null),
		class: className,
		...restProps
	}: WithoutChildrenOrChild<RadioGroupPrimitive.ItemProps> = $props();
</script>

<RadioGroupPrimitive.Item
	bind:ref
	data-slot="radio-group-item"
	class={cn(
		'aspect-square size-4 shrink-0 cursor-pointer rounded-full border border-input bg-field shadow-[inset_0_1px_1px_0_rgb(0_0_0/0.04)] transition-[border-color,box-shadow] duration-150 outline-none hover:border-border-strong focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-1 focus-visible:ring-offset-background disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive data-[state=checked]:border-primary',
		className
	)}
	{...restProps}
>
	{#snippet children({ checked })}
		<div
			data-slot="radio-group-indicator"
			class="relative flex size-full items-center justify-center"
		>
			{#if checked}
				<span class="size-2 rounded-full bg-primary"></span>
			{/if}
		</div>
	{/snippet}
</RadioGroupPrimitive.Item>

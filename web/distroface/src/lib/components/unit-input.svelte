<script lang="ts">
	import { Input } from '$lib/components/ui/input';
	import { cn } from '$lib/utils';
	import type { HTMLInputAttributes } from 'svelte/elements';

	let {
		value = $bindable(),
		unit,
		id,
		class: className,
		...restProps
	}: {
		value?: number | string | null;
		unit: string;
		id?: string;
		class?: string;
	} & Omit<HTMLInputAttributes, 'type' | 'value' | 'class' | 'files'> = $props();
</script>

<div class={cn('relative', className)}>
	<Input
		{id}
		type="number"
		bind:value
		style="padding-right: {unit.length + 3}ch"
		class="tabular-nums [appearance:textfield] [&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
		{...restProps}
	/>
	<span
		class="pointer-events-none absolute inset-y-0 right-3 flex items-center text-[13px] text-muted-foreground select-none"
	>
		{unit}
	</span>
</div>

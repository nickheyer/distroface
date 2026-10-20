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
		style="padding-right: {unit.length + 2.5}ch"
		class="[appearance:textfield] tabular-nums [&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
		{...restProps}
	/>
	<span
		class="pointer-events-none absolute inset-y-0 right-2.5 flex items-center text-xs text-muted-foreground select-none"
	>
		{unit}
	</span>
</div>

<script lang="ts">
	import { Checkbox } from '$lib/components/ui/checkbox';
	import { cn } from '$lib/utils';

	let {
		items,
		selected = $bindable([]),
		columns = 1,
		disabled = false,
		class: className
	}: {
		items: { value: string; label: string; description?: string }[];
		selected: string[];
		columns?: 1 | 2 | 3;
		disabled?: boolean;
		class?: string;
	} = $props();

	function toggle(value: string) {
		if (disabled) return;
		if (selected.includes(value)) {
			selected = selected.filter((v) => v !== value);
		} else {
			selected = [...selected, value];
		}
	}

	const gridClass = $derived(
		columns === 3
			? 'sm:grid-cols-3'
			: columns === 2
				? 'sm:grid-cols-2'
				: ''
	);
</script>

{#if items.length === 0}
	<p class="text-xs text-muted-foreground py-2">No options available</p>
{:else}
	<div class={cn('grid gap-2', gridClass, className)}>
		{#each items as item}
			{@const isChecked = selected.includes(item.value)}
			<button
				type="button"
				class={cn(
					'flex items-start gap-3 px-3 py-2.5 rounded-lg border text-left transition-all',
					isChecked
						? 'border-primary/40 bg-primary/5 shadow-sm shadow-primary/5'
						: 'border-border/60 hover:border-border hover:bg-muted/30',
					disabled && 'opacity-50 cursor-not-allowed'
				)}
				onclick={() => toggle(item.value)}
				{disabled}
			>
				<Checkbox checked={isChecked} class="mt-0.5 shrink-0" {disabled} />
				<div class="space-y-0.5 min-w-0">
					<span class="text-sm font-medium leading-none">{item.label}</span>
					{#if item.description}
						<p class="text-xs text-muted-foreground">{item.description}</p>
					{/if}
				</div>
			</button>
		{/each}
	</div>
{/if}

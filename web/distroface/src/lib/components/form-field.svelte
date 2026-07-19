<script lang="ts">
	import type { Snippet } from 'svelte';
	import { Label } from '$lib/components/ui/label';
	import { cn } from '$lib/utils';

	let {
		label,
		id,
		help,
		error,
		tag,
		required = false,
		horizontal = false,
		bordered = true,
		class: className,
		children
	}: {
		label: string;
		id?: string;
		help?: string;
		error?: string;
		tag?: string;
		required?: boolean;
		horizontal?: boolean;
		bordered?: boolean;
		class?: string;
		children: Snippet;
	} = $props();
</script>

{#snippet labelRow()}
	<div class="flex items-center gap-2">
		<Label for={id} class="text-sm font-medium leading-none">
			{label}{#if required}<span class="text-destructive ml-0.5">*</span>{/if}
		</Label>
		{#if tag}
			<span class="rounded-full border border-primary/30 bg-primary/5 px-1.5 py-px text-[10px] font-medium text-primary">
				{tag}
			</span>
		{/if}
	</div>
{/snippet}

{#if horizontal}
	<div
		class={cn(
			'flex items-center justify-between gap-6',
			bordered ? 'rounded-lg border border-border/60 px-4 py-3.5' : 'py-3',
			className
		)}
	>
		<div class="space-y-0.5 min-w-0">
			{@render labelRow()}
			{#if error}
				<p class="text-[13px] text-destructive leading-snug">{error}</p>
			{:else if help}
				<p class="text-[13px] text-muted-foreground leading-snug">{help}</p>
			{/if}
		</div>
		<div class="shrink-0">
			{@render children()}
		</div>
	</div>
{:else}
	<div class={cn(bordered ? 'rounded-lg border border-border/60 p-4 space-y-2' : 'space-y-1.5', className)}>
		{@render labelRow()}
		{@render children()}
		{#if error}
			<p class="text-[13px] text-destructive">{error}</p>
		{:else if help}
			<p class="text-[13px] text-muted-foreground leading-snug">{help}</p>
		{/if}
	</div>
{/if}

<script lang="ts">
	import type { Snippet } from 'svelte';
	import { Label } from '$lib/components/ui/label';
	import { cn } from '$lib/utils';

	type Span = 'full' | 'half' | 'third' | 'two-thirds';
	type Layout = 'stack' | 'toggle' | 'inline';
	const spanClass: Record<Span, string> = {
		full: '',
		half: 'span-half',
		third: 'span-third',
		'two-thirds': 'span-two-thirds'
	};

	let {
		label,
		id,
		help,
		error,
		tag,
		required = false,
		layout = 'stack',
		span = 'full',
		newRow = false,
		class: className,
		children
	}: {
		label: string;
		id?: string;
		help?: string;
		error?: string;
		tag?: string;
		required?: boolean;
		layout?: Layout;
		span?: Span;
		newRow?: boolean;
		class?: string;
		children: Snippet;
	} = $props();
</script>

{#snippet text()}
	<span
		>{label}{#if required}<span class="ml-0.5 text-destructive" aria-hidden="true">*</span
			>{/if}</span
	>
	{#if tag}
		<span class="form-tag">{tag}</span>
	{/if}
{/snippet}

{#snippet caption()}
	{#if error}
		<span class="form-caption text-destructive" role="alert">{error}</span>
	{:else if help}
		<span class="form-caption">{help}</span>
	{/if}
{/snippet}

{#if layout === 'toggle'}
	<Label
		for={id}
		class={cn(
			'group form-toggle cursor-pointer items-start gap-3 has-[[data-disabled]]:cursor-not-allowed',
			spanClass[span],
			newRow && 'span-break',
			className
		)}
	>
		<span class="flex h-5 shrink-0 items-center">
			{@render children()}
		</span>
		<span class="min-w-0 flex-1 space-y-1 group-has-[[data-disabled]]:opacity-60">
			<span class="flex items-center gap-2">{@render text()}</span>
			{@render caption()}
		</span>
	</Label>
{:else if layout === 'inline'}
	<div class={cn('min-w-0 space-y-1.5', spanClass[span], newRow && 'span-break', className)}>
		<div class="flex flex-wrap items-center gap-x-3 gap-y-1.5">
			<Label for={id} class="gap-2">{@render text()}</Label>
			{@render children()}
		</div>
		{@render caption()}
	</div>
{:else}
	<div class={cn('min-w-0 space-y-1.5', spanClass[span], newRow && 'span-break', className)}>
		<Label for={id} class="gap-2">{@render text()}</Label>
		{@render children()}
		{@render caption()}
	</div>
{/if}

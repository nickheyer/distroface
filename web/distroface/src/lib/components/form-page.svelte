<script lang="ts">
	import type { Snippet } from 'svelte';
	import { ArrowLeft } from '@lucide/svelte';
	import FormActions from '$lib/components/form-actions.svelte';
	import { cn } from '$lib/utils';

	let {
		title,
		description,
		backHref,
		backLabel = 'Back',
		level = 'page',
		actions,
		children,
		footer
	}: {
		title: string;
		description?: string;
		backHref?: string;
		backLabel?: string;
		level?: 'page' | 'section';
		actions?: Snippet;
		children: Snippet;
		footer?: Snippet;
	} = $props();
</script>

<div class={cn('w-full', level === 'page' && 'mx-auto max-w-5xl')}>
	{#if backHref}
		<!-- eslint-disable svelte/no-navigation-without-resolve -->
		<a
			href={backHref}
			class="mb-4 inline-flex items-center gap-1 text-sm text-muted-foreground transition-colors hover:text-foreground"
		>
			<ArrowLeft class="size-3.5" />
			{backLabel}
		</a>
		<!-- eslint-enable svelte/no-navigation-without-resolve -->
	{/if}
	<div class="flex items-start justify-between gap-4">
		<div class="min-w-0">
			{#if level === 'page'}
				<h1 class="text-2xl font-semibold tracking-tight">{title}</h1>
			{:else}
				<h2 class="text-lg font-semibold tracking-tight">{title}</h2>
			{/if}
			{#if description}
				<p class="mt-1 text-sm text-muted-foreground">{description}</p>
			{/if}
		</div>
		{#if actions}
			<div class="flex shrink-0 items-center gap-2">
				{@render actions()}
			</div>
		{/if}
	</div>
	<div class="form-stack mt-6 border-t pt-6">
		{@render children()}
	</div>
	{#if footer}
		<FormActions class="mt-8 border-t pt-6">
			{@render footer()}
		</FormActions>
	{/if}
</div>

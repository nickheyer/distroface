<script lang="ts">
	import type { Snippet, Component } from 'svelte';
	import {
		Sheet,
		SheetContent,
		SheetHeader,
		SheetTitle,
		SheetDescription
	} from '$lib/components/ui/sheet';
	import { Button } from '$lib/components/ui/button';
	import { X } from '@lucide/svelte';
	import { cn } from '$lib/utils';

	let {
		open = $bindable(false),
		title,
		description = '',
		icon,
		wide = false,
		children,
		footer,
		onOpenChange
	}: {
		open: boolean;
		title: string;
		description?: string;
		icon?: Component<{ class?: string }>;
		wide?: boolean;
		children: Snippet;
		footer?: Snippet;
		onOpenChange?: (open: boolean) => void;
	} = $props();

	function handleOpenChange(value: boolean) {
		open = value;
		onOpenChange?.(value);
	}
</script>

<Sheet bind:open onOpenChange={handleOpenChange}>
	<SheetContent
		side="right"
		class={cn(
			'flex flex-col gap-0 p-0 overflow-hidden',
			wide ? 'w-full sm:max-w-2xl' : 'w-full sm:max-w-lg'
		)}
	>
		<div class="flex items-start gap-3 px-6 py-5 border-b border-border/40 bg-muted/20">
			{#if icon}
				{@const Icon = icon}
				<div class="h-10 w-10 rounded-xl bg-primary/10 flex items-center justify-center shrink-0 mt-0.5">
					<Icon class="h-5 w-5 text-primary" />
				</div>
			{/if}
			<div class="flex-1 min-w-0">
				<SheetTitle class="text-lg font-semibold tracking-tight">{title}</SheetTitle>
				{#if description}
					<SheetDescription class="text-[13px] text-muted-foreground mt-1">{description}</SheetDescription>
				{:else}
					<SheetDescription class="sr-only">{title}</SheetDescription>
				{/if}
			</div>
		</div>

		<div class="flex-1 overflow-y-auto px-6 py-6">
			{@render children()}
		</div>

		{#if footer}
			<div class="flex items-center justify-end gap-3 px-6 py-4 border-t border-border/40 bg-muted/20">
				{@render footer()}
			</div>
		{/if}
	</SheetContent>
</Sheet>

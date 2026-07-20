<script lang="ts">
	import type { Snippet, Component } from 'svelte';
	import { Dialog as DialogPrimitive } from 'bits-ui';
	import * as Dialog from '$lib/components/ui/dialog';
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

<Dialog.Root bind:open onOpenChange={handleOpenChange}>
	<Dialog.Portal>
		<Dialog.Overlay />
		<DialogPrimitive.Content
			data-slot="dialog-content"
			class={cn(
				'bg-background data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95 fixed left-[50%] top-[50%] z-50 flex flex-col w-full max-w-[calc(100%-2rem)] translate-x-[-50%] translate-y-[-50%] max-h-[min(85vh,52rem)] overflow-hidden rounded-xl border shadow-lg duration-200',
				wide ? 'sm:max-w-3xl' : 'sm:max-w-xl'
			)}
		>
			<div class="flex items-start gap-3 px-6 py-5 border-b border-border/40 bg-muted/20 shrink-0">
				{#if icon}
					{@const Icon = icon}
					<div class="h-10 w-10 rounded-xl bg-primary/10 flex items-center justify-center shrink-0 mt-0.5">
						<Icon class="h-5 w-5 text-primary" />
					</div>
				{/if}
				<div class="flex-1 min-w-0">
					<Dialog.Title class="text-lg font-semibold tracking-tight">{title}</Dialog.Title>
					{#if description}
						<Dialog.Description class="text-[13px] text-muted-foreground mt-1">{description}</Dialog.Description>
					{:else}
						<Dialog.Description class="sr-only">{title}</Dialog.Description>
					{/if}
				</div>
			</div>

			<div class="flex-1 overflow-y-auto px-6 py-6 min-h-0">
				{@render children()}
			</div>

			{#if footer}
				<div class="flex items-center justify-end gap-3 px-6 py-4 border-t border-border/40 bg-muted/20 shrink-0">
					{@render footer()}
				</div>
			{/if}
		</DialogPrimitive.Content>
	</Dialog.Portal>
</Dialog.Root>

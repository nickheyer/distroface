<script lang="ts">
	import type { Snippet, Component } from 'svelte';
	import { Dialog as DialogPrimitive } from 'bits-ui';
	import { AlertTriangle } from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';

	let {
		open = $bindable(false),
		title = 'Are you sure?',
		description,
		confirmLabel = 'Confirm',
		cancelLabel = 'Cancel',
		onConfirm,
		loading = false,
		variant = 'destructive',
		icon
	}: {
		open: boolean;
		title?: string;
		description?: Snippet;
		confirmLabel?: string;
		cancelLabel?: string;
		onConfirm: () => void;
		loading?: boolean;
		variant?: 'destructive' | 'default';
		icon?: Component<{ class?: string }>;
	} = $props();
</script>

<Dialog.Root bind:open>
	<Dialog.Portal>
		<Dialog.Overlay />
		<DialogPrimitive.Content
			data-slot="dialog-content"
			class="bg-background data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95 fixed left-[50%] top-[50%] z-50 flex flex-col w-full max-w-[calc(100%-2rem)] translate-x-[-50%] translate-y-[-50%] overflow-hidden rounded-lg border shadow-lg duration-200 sm:max-w-md"
		>
			<div class="flex flex-col items-center text-center px-6 pt-6 pb-4">
				<div class="h-12 w-12 rounded-full {variant === 'destructive' ? 'bg-destructive/10' : 'bg-primary/10'} flex items-center justify-center mb-4">
					{#if icon}
						{@const Icon = icon}
						<Icon class="h-6 w-6 {variant === 'destructive' ? 'text-destructive' : 'text-primary'}" />
					{:else}
						<AlertTriangle class="h-6 w-6 {variant === 'destructive' ? 'text-destructive' : 'text-primary'}" />
					{/if}
				</div>
				<h2 class="text-lg font-semibold">{title}</h2>
				{#if description}
					<p class="text-sm text-muted-foreground mt-2">
						{@render description()}
					</p>
				{/if}
			</div>

			<div class="flex items-center justify-end gap-3 px-6 py-4 border-t">
				<Button variant="outline" onclick={() => (open = false)}>{cancelLabel}</Button>
				<Button {variant} onclick={onConfirm} disabled={loading}>
					{loading ? `${confirmLabel.replace(/e$/, '')}ing...` : confirmLabel}
				</Button>
			</div>
		</DialogPrimitive.Content>
	</Dialog.Portal>
</Dialog.Root>

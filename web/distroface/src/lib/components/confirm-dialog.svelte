<script lang="ts">
	import type { Snippet, Component } from 'svelte';
	import { Dialog as DialogPrimitive } from 'bits-ui';
	import { AlertTriangle, Loader2 } from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';
	import { dialogShellClass } from '$lib/components/ui/dialog/dialog-content.svelte';
	import { cn } from '$lib/utils';

	let {
		open = $bindable(false),
		title = 'Are you sure?',
		description,
		children,
		confirmLabel = 'Confirm',
		cancelLabel = 'Cancel',
		confirmDisabled = false,
		onConfirm,
		loading = false,
		variant = 'destructive',
		icon
	}: {
		open: boolean;
		title?: string;
		description?: Snippet;
		children?: Snippet;
		confirmLabel?: string;
		cancelLabel?: string;
		confirmDisabled?: boolean;
		onConfirm: () => void;
		loading?: boolean;
		variant?: 'destructive' | 'default';
		icon?: Component<{ class?: string }>;
	} = $props();

	const Icon = $derived(icon ?? AlertTriangle);
</script>

<Dialog.Root bind:open>
	<Dialog.Portal>
		<Dialog.Overlay />
		<DialogPrimitive.Content data-slot="dialog-content" class={cn(dialogShellClass, 'sm:max-w-md')}>
			<div class="flex items-start gap-3.5 px-6 pt-5 pb-5">
				<div
					class={cn(
						'flex size-8 shrink-0 items-center justify-center rounded-full',
						variant === 'destructive'
							? 'bg-destructive/10 text-destructive'
							: 'bg-primary/10 text-primary'
					)}
				>
					<Icon class="size-4" />
				</div>
				<div class="min-w-0 flex-1 pt-1">
					<Dialog.Title>{title}</Dialog.Title>
					{#if description}
						<Dialog.Description class="mt-1">
							{@render description()}
						</Dialog.Description>
					{:else}
						<Dialog.Description class="sr-only">{title}</Dialog.Description>
					{/if}
				</div>
			</div>

			{#if children}
				<div class="space-y-4 border-t px-6 py-5">
					{@render children()}
				</div>
			{/if}

			<div class="flex items-center justify-end gap-2 border-t bg-muted/30 px-6 py-3.5">
				<Button variant="outline" onclick={() => (open = false)} disabled={loading}
					>{cancelLabel}</Button
				>
				<Button {variant} onclick={onConfirm} disabled={loading || confirmDisabled}>
					{#if loading}
						<Loader2 class="animate-spin" />
					{/if}
					{confirmLabel}
				</Button>
			</div>
		</DialogPrimitive.Content>
	</Dialog.Portal>
</Dialog.Root>

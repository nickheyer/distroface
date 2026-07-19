<script lang="ts">
	import type { Snippet, Component } from 'svelte';
	import { Dialog as DialogPrimitive } from 'bits-ui';
	import { X } from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';
	import { cn } from '$lib/utils';

	let {
		open = $bindable(false),
		title = '',
		description = '',
		size = 'md',
		icon,
		sidebarTitle = '',
		sidebarSubtitle = '',
		onOpenChange,
		children,
		sidebar,
		footer
	}: {
		open: boolean;
		title?: string;
		description?: string;
		size?: 'sm' | 'md' | 'lg' | 'xl' | 'full';
		icon?: Component<{ class?: string }>;
		sidebarTitle?: string;
		sidebarSubtitle?: string;
		onOpenChange?: (open: boolean) => void;
		children: Snippet;
		sidebar?: Snippet;
		footer?: Snippet;
	} = $props();

	const hasSidebar = $derived(!!sidebar);

	const sizeClasses: Record<string, string> = {
		sm: 'sm:max-w-lg',
		md: 'sm:max-w-xl',
		lg: 'sm:max-w-4xl sm:h-[70vh]',
		xl: 'sm:max-w-5xl sm:h-[80vh]',
		full: 'sm:max-w-6xl w-[95vw] h-[85vh]'
	};
</script>

<Dialog.Root bind:open {onOpenChange}>
	<Dialog.Portal>
		<Dialog.Overlay />
		<DialogPrimitive.Content
			data-slot="dialog-content"
			class={cn(
				'bg-background data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95 fixed left-[50%] top-[50%] z-50 flex w-full max-w-[calc(100%-2rem)] translate-x-[-50%] translate-y-[-50%] overflow-hidden rounded-lg border shadow-lg duration-200',
				sizeClasses[size]
			)}
		>
			<div class="flex h-full w-full {hasSidebar ? '' : 'flex-col'}">
				{#if hasSidebar}
					<!-- Sidebar layout -->
					<div class="w-64 border-r bg-muted/30 flex-col shrink-0 hidden md:flex">
						<div class="p-6 border-b">
							<div class="flex items-center gap-3">
								<div class="h-12 w-12 rounded-xl bg-primary/10 flex items-center justify-center">
									{#if icon}
										{@const Icon = icon}
										<Icon class="h-6 w-6 text-primary" />
									{/if}
								</div>
								<div class="flex-1 min-w-0">
									<h3 class="font-semibold truncate">{sidebarTitle || title}</h3>
									{#if sidebarSubtitle}
										<p class="text-xs text-muted-foreground mt-0.5">{sidebarSubtitle}</p>
									{/if}
								</div>
							</div>
						</div>

						<div class="flex-1 overflow-y-auto">
							{@render sidebar?.()}
						</div>

						{#if description}
							<div class="p-4 border-t">
								<div class="p-4 rounded-lg bg-muted/50">
									<p class="text-xs text-muted-foreground">{description}</p>
								</div>
							</div>
						{/if}
					</div>

					<!-- Main Content (with sidebar) -->
					<div class="flex-1 flex flex-col min-w-0">
						<div class="flex items-center justify-between px-6 py-4 border-b bg-muted/30">
							<h2 class="text-lg font-semibold tracking-tight">{title}</h2>
							<Button
								variant="ghost"
								size="icon"
								class="h-8 w-8"
								onclick={() => (open = false)}
							>
								<X class="h-4 w-4" />
							</Button>
						</div>

						<div class="flex-1 overflow-y-auto p-6">
							{@render children()}
						</div>

						{#if footer}
							<div class="flex items-center justify-end gap-3 px-6 py-4 border-t bg-muted/30">
								{@render footer()}
							</div>
						{/if}
					</div>
				{:else}
					<!-- No sidebar layout -->
					<div class="flex items-start justify-between gap-4 px-6 pt-6 pb-4">
						<div class="flex items-center gap-3">
							{#if icon}
								{@const Icon = icon}
								<div class="h-9 w-9 rounded-lg bg-primary/10 flex items-center justify-center shrink-0">
									<Icon class="h-4.5 w-4.5 text-primary" />
								</div>
							{/if}
							<div>
								<h2 class="text-lg font-semibold tracking-tight">{title}</h2>
								{#if description}
									<p class="text-sm text-muted-foreground mt-0.5">{description}</p>
								{/if}
							</div>
						</div>
						<Button
							variant="ghost"
							size="icon"
							class="h-8 w-8 shrink-0"
							onclick={() => (open = false)}
						>
							<X class="h-4 w-4" />
						</Button>
					</div>

					<div class="flex-1 overflow-y-auto px-6 pb-2">
						{@render children()}
					</div>

					{#if footer}
						<div class="flex items-center justify-end gap-3 px-6 py-4 border-t">
							{@render footer()}
						</div>
					{/if}
				{/if}
			</div>
		</DialogPrimitive.Content>
	</Dialog.Portal>
</Dialog.Root>

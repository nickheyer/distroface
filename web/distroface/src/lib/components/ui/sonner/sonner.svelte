<script lang="ts">
	import { Toaster as Sonner, type ToasterProps as SonnerProps } from 'svelte-sonner';
	import { mode } from 'mode-watcher';

	let { ...restProps }: SonnerProps = $props();
</script>

<Sonner
	theme={mode.current}
	class="toaster group"
	toastOptions={{
		classes: {
			toast:
				'group toast !rounded-lg !border-border !bg-popover !text-popover-foreground !shadow-lg !text-sm',
			description: '!text-muted-foreground !text-xs',
			actionButton: '!bg-primary !text-primary-foreground !rounded-md !text-xs !font-medium',
			cancelButton: '!bg-muted !text-foreground !rounded-md !text-xs !font-medium'
		}
	}}
	style="--normal-bg: var(--color-popover); --normal-text: var(--color-popover-foreground); --normal-border: var(--color-border); --border-radius: var(--radius);"
	{...restProps}
/>

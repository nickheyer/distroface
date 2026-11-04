<script lang="ts" module>
	import { type VariantProps, tv } from 'tailwind-variants';

	export const badgeVariants = tv({
		base: 'inline-flex h-5 w-fit shrink-0 items-center justify-center gap-1 overflow-hidden whitespace-nowrap rounded-full border px-2 text-xs font-medium leading-none transition-colors [&>svg]:pointer-events-none [&>svg]:size-3',
		variants: {
			variant: {
				default: 'border-primary/20 bg-primary/10 text-primary [a&]:hover:bg-primary/15',
				secondary: 'border-transparent bg-muted text-muted-foreground [a&]:hover:bg-accent',
				destructive:
					'border-destructive/20 bg-destructive/10 text-destructive [a&]:hover:bg-destructive/15',
				success: 'border-success/25 bg-success/10 text-success [a&]:hover:bg-success/15',
				warning: 'border-warning/30 bg-warning/10 text-warning [a&]:hover:bg-warning/15',
				done: 'border-done/25 bg-done/10 text-done [a&]:hover:bg-done/15',
				info: 'border-info/25 bg-info/10 text-info [a&]:hover:bg-info/15',
				outline: 'border-border bg-transparent text-muted-foreground [a&]:hover:bg-muted',
				solid: 'border-transparent bg-foreground text-background'
			}
		},
		defaultVariants: {
			variant: 'default'
		}
	});

	export type BadgeVariant = VariantProps<typeof badgeVariants>['variant'];
</script>

<script lang="ts">
	import type { HTMLAnchorAttributes } from 'svelte/elements';
	import { cn, type WithElementRef } from '$lib/utils.js';

	let {
		ref = $bindable(null),
		href,
		class: className,
		variant = 'default',
		children,
		...restProps
	}: WithElementRef<HTMLAnchorAttributes> & {
		variant?: BadgeVariant;
	} = $props();
</script>

<svelte:element
	this={href ? 'a' : 'span'}
	bind:this={ref}
	data-slot="badge"
	{href}
	class={cn(badgeVariants({ variant }), className)}
	{...restProps}
>
	{@render children?.()}
</svelte:element>

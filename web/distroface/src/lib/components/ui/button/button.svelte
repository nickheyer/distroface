<script lang="ts" module>
	import { cn, type WithElementRef } from '$lib/utils.js';
	import type { HTMLAnchorAttributes, HTMLButtonAttributes } from 'svelte/elements';
	import { type VariantProps, tv } from 'tailwind-variants';

	export const buttonVariants = tv({
		base: "inline-flex shrink-0 cursor-pointer select-none items-center justify-center gap-1.5 whitespace-nowrap rounded-md border border-transparent text-sm font-medium outline-none transition-[background-color,border-color,color,box-shadow] duration-150 focus-visible:ring-2 focus-visible:ring-ring/60 focus-visible:ring-offset-1 focus-visible:ring-offset-background disabled:pointer-events-none disabled:opacity-50 aria-disabled:pointer-events-none aria-disabled:opacity-50 aria-invalid:border-destructive [&_svg:not([class*='size-'])]:size-4 [&_svg]:pointer-events-none [&_svg]:shrink-0",
		variants: {
			variant: {
				default:
					'bg-primary text-primary-foreground shadow-[inset_0_1px_0_0_rgb(255_255_255/0.14),0_1px_2px_0_rgb(0_0_0/0.08)] hover:bg-primary/92 active:bg-primary/85 active:shadow-none',
				destructive:
					'bg-destructive text-destructive-foreground shadow-[inset_0_1px_0_0_rgb(255_255_255/0.14),0_1px_2px_0_rgb(0_0_0/0.08)] hover:bg-destructive/92 active:bg-destructive/85 active:shadow-none focus-visible:ring-destructive/50',
				outline:
					'border-border bg-card text-foreground shadow-xs hover:border-border-strong hover:bg-muted/60 active:bg-muted',
				secondary:
					'bg-secondary text-secondary-foreground hover:bg-secondary/70 active:bg-secondary/60',
				ghost: 'text-foreground hover:bg-muted active:bg-muted/80',
				'ghost-muted':
					'text-muted-foreground hover:bg-muted hover:text-foreground active:bg-muted/80',
				'outline-destructive':
					'border-border bg-card text-destructive shadow-xs hover:border-destructive/40 hover:bg-destructive/8 active:bg-destructive/12 focus-visible:ring-destructive/50',
				link: 'text-primary underline-offset-2 hover:underline'
			},
			size: {
				xs: 'h-6 gap-1 rounded-sm px-2 text-xs [&_svg:not([class*="size-"])]:size-3',
				sm: 'h-7 gap-1 px-2.5 text-xs [&_svg:not([class*="size-"])]:size-3.5',
				default: 'h-8 px-3',
				lg: 'h-9 px-4',
				icon: 'size-8',
				'icon-sm': 'size-7 [&_svg:not([class*="size-"])]:size-3.5',
				'icon-xs': 'size-6 rounded-sm [&_svg:not([class*="size-"])]:size-3.5'
			}
		},
		defaultVariants: {
			variant: 'default',
			size: 'default'
		}
	});

	export type ButtonVariant = VariantProps<typeof buttonVariants>['variant'];
	export type ButtonSize = VariantProps<typeof buttonVariants>['size'];

	export type ButtonProps = WithElementRef<HTMLButtonAttributes> &
		WithElementRef<HTMLAnchorAttributes> & {
			variant?: ButtonVariant;
			size?: ButtonSize;
		};
</script>

<script lang="ts">
	let {
		class: className,
		variant = 'default',
		size = 'default',
		ref = $bindable(null),
		href = undefined,
		type = 'button',
		disabled,
		children,
		...restProps
	}: ButtonProps = $props();
</script>

{#if href}
	<!-- eslint-disable svelte/no-navigation-without-resolve -->
	<a
		bind:this={ref}
		data-slot="button"
		class={cn(buttonVariants({ variant, size }), className)}
		href={disabled ? undefined : href}
		aria-disabled={disabled}
		role={disabled ? 'link' : undefined}
		tabindex={disabled ? -1 : undefined}
		{...restProps}
	>
		{@render children?.()}
	</a>
	<!-- eslint-enable svelte/no-navigation-without-resolve -->
{:else}
	<button
		bind:this={ref}
		data-slot="button"
		class={cn(buttonVariants({ variant, size }), className)}
		{type}
		{disabled}
		{...restProps}
	>
		{@render children?.()}
	</button>
{/if}

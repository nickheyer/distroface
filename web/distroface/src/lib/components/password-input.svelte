<script lang="ts">
	import { Input } from '$lib/components/ui/input';
	import { Eye, EyeOff } from '$lib/icons';
	import { cn } from '$lib/utils';
	import type { HTMLInputAttributes } from 'svelte/elements';

	let {
		value = $bindable(''),
		error,
		id,
		placeholder = 'Password',
		autocomplete = 'current-password',
		class: className,
		...restProps
	}: {
		value: string;
		error?: boolean;
		id?: string;
		placeholder?: string;
		autocomplete?: HTMLInputAttributes['autocomplete'];
		class?: string;
	} & Record<string, unknown> = $props();

	let visible = $state(false);
</script>

<div class="relative">
	<Input
		{id}
		type={visible ? 'text' : 'password'}
		{placeholder}
		{autocomplete}
		class={cn('pr-8', className)}
		bind:value
		aria-invalid={error}
		{...restProps}
	/>
	<button
		type="button"
		class="absolute inset-y-0 right-0 flex w-8 items-center justify-center rounded-r-md text-muted-foreground transition-colors hover:text-foreground"
		onclick={() => (visible = !visible)}
		tabindex={-1}
		aria-label={visible ? 'Hide password' : 'Show password'}
	>
		{#if visible}
			<EyeOff class="size-3.5" />
		{:else}
			<Eye class="size-3.5" />
		{/if}
	</button>
</div>

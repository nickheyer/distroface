<script lang="ts">
	import { Input } from '$lib/components/ui/input';
	import { Eye, EyeOff } from '@lucide/svelte';
	import type { HTMLInputAttributes } from 'svelte/elements';

	let {
		value = $bindable(''),
		error,
		id,
		placeholder = 'Enter password',
		autocomplete = 'current-password',
		...restProps
	}: {
		value: string;
		error?: boolean;
		id?: string;
		placeholder?: string;
		autocomplete?: HTMLInputAttributes['autocomplete'];
	} & Record<string, unknown> = $props();

	let visible = $state(false);
</script>

<div class="relative">
	<Input
		{id}
		type={visible ? 'text' : 'password'}
		{placeholder}
		{autocomplete}
		class="pr-10"
		bind:value
		aria-invalid={error}
		{...restProps}
	/>
	<button
		type="button"
		class="absolute right-0 top-0 h-10 w-10 flex items-center justify-center text-muted-foreground hover:text-foreground transition-colors"
		onclick={() => visible = !visible}
		tabindex={-1}
	>
		{#if visible}
			<EyeOff class="h-4 w-4" />
		{:else}
			<Eye class="h-4 w-4" />
		{/if}
	</button>
</div>

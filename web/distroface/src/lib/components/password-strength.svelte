<script lang="ts">
	import { Check, X } from '@lucide/svelte';

	let { password }: { password: string } = $props();

	const checks = $derived({
		length: password.length >= 8,
		upper: /[A-Z]/.test(password),
		lower: /[a-z]/.test(password),
		number: /[0-9]/.test(password)
	});

	const strength = $derived(Object.values(checks).filter(Boolean).length);
</script>

{#if password.length > 0}
	<div class="space-y-2 pt-1">
		<div class="flex gap-1">
			{#each [1, 2, 3, 4] as level (level)}
				<div
					class="h-1 flex-1 rounded-full transition-colors {strength >= level
						? strength <= 2
							? 'bg-destructive/70'
							: strength === 3
								? 'bg-amber-500'
								: 'bg-success'
						: 'bg-border'}"
				></div>
			{/each}
		</div>
		<div class="grid grid-cols-2 gap-x-3 gap-y-0.5">
			{#each [
				{ passed: checks.length, label: '8+ characters' },
				{ passed: checks.upper, label: 'Uppercase' },
				{ passed: checks.lower, label: 'Lowercase' },
				{ passed: checks.number, label: 'Number' }
			] as { passed, label } (label)}
				<span class="flex items-center gap-1.5 text-[11px] {passed ? 'text-success' : 'text-muted-foreground/70'}">
					{#if passed}
						<Check class="h-3 w-3" />
					{:else}
						<X class="h-3 w-3" />
					{/if}
					{label}
				</span>
			{/each}
		</div>
	</div>
{/if}

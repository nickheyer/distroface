<script module lang="ts">
	// Six digit hex with a leading hash, gray when unparsable
	export function normalizeHex(color: string): string {
		const c = (color ?? '').trim().replace(/^#/, '');
		return /^[0-9a-fA-F]{6}$/.test(c) ? `#${c.toLowerCase()}` : '#6b7280';
	}

	// Dark or light text that stays readable on the color
	export function labelTextColor(color: string): string {
		const hex = normalizeHex(color).slice(1);
		const r = parseInt(hex.slice(0, 2), 16);
		const g = parseInt(hex.slice(2, 4), 16);
		const b = parseInt(hex.slice(4, 6), 16);
		const luminance = 0.299 * r + 0.587 * g + 0.114 * b;
		return luminance > 150 ? '#111827' : '#ffffff';
	}
</script>

<script lang="ts">
	import { cn } from '$lib/utils';

	let {
		name,
		color,
		description,
		size = 'md',
		class: className
	}: {
		name: string;
		color: string;
		description?: string;
		size?: 'sm' | 'md';
		class?: string;
	} = $props();

	const bg = $derived(normalizeHex(color));
	const fg = $derived(labelTextColor(color));
</script>

<span
	class={cn(
		'inline-flex items-center rounded-full font-medium whitespace-nowrap border border-black/10 dark:border-white/10 leading-none',
		size === 'sm' ? 'px-2 py-0.5 text-[11px]' : 'px-2.5 py-1 text-xs',
		className
	)}
	style="background-color: {bg}; color: {fg};"
	title={description || name}
>
	{name}
</span>

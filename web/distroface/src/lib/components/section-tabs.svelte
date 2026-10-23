<script lang="ts" module>
	export type SectionTab = { href: string; label: string };
</script>

<script lang="ts">
	import { page } from '$app/state';
	import { cn } from '$lib/utils';

	let {
		tabs,
		label = 'Section tabs',
		class: className
	}: { tabs: SectionTab[]; label?: string; class?: string } = $props();

	const trim = (p: string) => p.replace(/\/$/, '');

	// Longest tab prefixing the current path wins
	const activeHref = $derived.by(() => {
		const current = trim(page.url.pathname);
		let best = '';
		for (const t of tabs) {
			const href = trim(t.href);
			if ((current === href || current.startsWith(`${href}/`)) && href.length > best.length) {
				best = href;
			}
		}
		return best;
	});
</script>

<nav
	class={cn('scrollbar-none flex items-center gap-0.5 overflow-x-auto border-b', className)}
	aria-label={label}
>
	{#each tabs as tab (tab.href)}
		{@const active = trim(tab.href) === activeHref}
		<!-- eslint-disable svelte/no-navigation-without-resolve -->
		<a
			href={tab.href}
			aria-current={active ? 'page' : undefined}
			class={cn(
				'-mb-px flex shrink-0 items-center border-b-2 px-3 py-2 text-sm font-medium whitespace-nowrap transition-colors',
				active
					? 'border-primary text-foreground'
					: 'border-transparent text-muted-foreground hover:text-foreground'
			)}
		>
			{tab.label}
		</a>
		<!-- eslint-enable svelte/no-navigation-without-resolve -->
	{/each}
</nav>

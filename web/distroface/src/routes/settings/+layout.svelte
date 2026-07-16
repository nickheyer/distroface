<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { authStore } from '$lib/stores/auth.svelte';
	import { Settings, User, Lock, Key } from '@lucide/svelte';
	import PageHeader from '$lib/components/page-header.svelte';

	let { children } = $props();

	const navItems = $derived([
		{ href: resolve('/settings/profile'), label: 'Profile', icon: User },
		{ href: resolve('/settings/security'), label: 'Security', icon: Lock },
		...(authStore.canReadTokens
			? [{ href: resolve('/settings/tokens'), label: 'API Tokens', icon: Key }]
			: [])
	]);

	function isActive(href: string): boolean {
		return page.url.pathname === href;
	}
</script>

<PageHeader title="Settings" subtitle="Manage your account" icon={Settings} />

<div class="flex flex-col md:flex-row gap-8 mt-2">
	<nav class="md:w-52 shrink-0">
		<div class="flex md:flex-col gap-0.5 overflow-x-auto md:overflow-visible pb-2 md:pb-0 md:sticky md:top-20">
			{#each navItems as item (item.href)}
				<!-- eslint-disable svelte/no-navigation-without-resolve -->
				<a
					href={item.href}
					class="flex items-center gap-2.5 px-3 py-2 rounded-lg text-sm font-medium whitespace-nowrap transition-colors {isActive(item.href)
						? 'bg-accent text-accent-foreground'
						: 'text-muted-foreground hover:text-foreground hover:bg-accent/50'}"
				>
					<item.icon class="h-4 w-4" />
					{item.label}
				</a>
				<!-- eslint-enable svelte/no-navigation-without-resolve -->
			{/each}
		</div>
	</nav>

	<div class="flex-1 min-w-0">
		{#key page.url.pathname}
			<div class="page-enter">
				{@render children?.()}
			</div>
		{/key}
	</div>
</div>

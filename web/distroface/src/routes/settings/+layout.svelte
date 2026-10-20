<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { authStore } from '$lib/stores/auth.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { Settings, User, Lock, Key, KeyRound, Bell } from '@lucide/svelte';
	import PageHeader from '$lib/components/page-header.svelte';
	import SideNav from '$lib/components/side-nav.svelte';

	let { children } = $props();

	const navItems = $derived([
		{ href: resolve('/settings/profile'), label: 'Profile', icon: User },
		{ href: resolve('/settings/security'), label: 'Security', icon: Lock },
		...(authStore.canReadTokens
			? [{ href: resolve('/settings/tokens'), label: 'API tokens', icon: Key }]
			: []),
		...(configStore.gitEnabled
			? [
					{ href: resolve('/settings/keys'), label: 'Keys', icon: KeyRound },
					{ href: resolve('/settings/notifications'), label: 'Notifications', icon: Bell }
				]
			: [])
	]);

	function isActive(href: string): boolean {
		const current = page.url.pathname.replace(/\/$/, '');
		return current === href || current.startsWith(`${href}/`);
	}
</script>

<PageHeader title="Settings" subtitle="Manage your account" icon={Settings} />

<div class="flex flex-col gap-8 md:flex-row">
	<SideNav items={navItems} {isActive} />
	<div class="min-w-0 flex-1">
		{#key page.url.pathname}
			<div class="page-enter">
				{@render children?.()}
			</div>
		{/key}
	</div>
</div>

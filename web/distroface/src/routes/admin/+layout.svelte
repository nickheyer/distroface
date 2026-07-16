<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import type { Pathname } from '$app/types';
	import { onMount } from 'svelte';
	import { authStore } from '$lib/stores/auth.svelte';
	import { LayoutDashboard, Shield, Users, Key, Ticket } from '@lucide/svelte';
	import PageHeader from '$lib/components/page-header.svelte';

	let { children } = $props();

	type NavItem = { href: Pathname; label: string; icon: typeof Shield };

	const navItems: NavItem[] = $derived([
		...(authStore.canAccessAdmin
			? [{ href: '/admin', label: 'Overview', icon: LayoutDashboard } satisfies NavItem]
			: []),
		...(authStore.canReadSettings
			? [{ href: '/admin/settings', label: 'Authentication', icon: Shield } satisfies NavItem]
			: []),
		...(authStore.canReadUsers
			? [{ href: '/admin/users', label: 'Users', icon: Users } satisfies NavItem]
			: []),
		...(authStore.canReadRoles
			? [{ href: '/admin/roles', label: 'Roles', icon: Key } satisfies NavItem]
			: []),
		...(authStore.canReadSettings
			? [{ href: '/admin/invites', label: 'Invites', icon: Ticket } satisfies NavItem]
			: [])
	]);

	function isActive(href: string): boolean {
		return page.url.pathname === href;
	}

	onMount(() => {
		if (!authStore.canAccessAdmin) {
			goto(resolve('/'));
		}
	});
</script>

<PageHeader title="Administration" subtitle="Manage your Distroface instance" icon={Shield} />

<div class="flex flex-col md:flex-row gap-8 mt-2">
	<nav class="md:w-52 shrink-0">
		<div class="flex md:flex-col gap-0.5 overflow-x-auto md:overflow-visible pb-2 md:pb-0 md:sticky md:top-20">
			{#each navItems as item (item.href)}
				<a
					href={resolve(item.href)}
					class="flex items-center gap-2.5 px-3 py-2 rounded-lg text-sm font-medium whitespace-nowrap transition-colors {isActive(item.href)
						? 'bg-accent text-accent-foreground'
						: 'text-muted-foreground hover:text-foreground hover:bg-accent/50'}"
				>
					<item.icon class="h-4 w-4" />
					{item.label}
				</a>
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

<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import type { Pathname } from '$app/types';
	import { onMount } from 'svelte';
	import { authStore } from '$lib/stores/auth.svelte';
	import {
		LayoutDashboard,
		Shield,
		ShieldCheck,
		Lock,
		Globe,
		ScrollText,
		HardDrive,
		HardDriveDownload,
		Users,
		Key,
		Ticket,
		GitBranch,
		Mail,
		Cpu
	} from '@lucide/svelte';
	import PageHeader from '$lib/components/page-header.svelte';
	import SideNav from '$lib/components/side-nav.svelte';

	let { children } = $props();

	type NavItem = { href: Pathname; label: string; icon: typeof Shield };

	const navItems: NavItem[] = $derived([
		...(authStore.canAccessAdmin
			? [{ href: '/admin', label: 'Overview', icon: LayoutDashboard } satisfies NavItem]
			: []),
		...(authStore.canReadSettings
			? [{ href: '/admin/auth', label: 'Authentication', icon: Shield } satisfies NavItem]
			: []),
		...(authStore.canManageSettings
			? [
					{ href: '/admin/network', label: 'Network security', icon: Lock } satisfies NavItem,
					{
						href: '/admin/pki',
						label: 'Certificate authority',
						icon: ShieldCheck
					} satisfies NavItem,
					{ href: '/admin/portals', label: 'Portals', icon: Globe } satisfies NavItem
				]
			: []),
		...(authStore.canReadSettings
			? [{ href: '/admin/audit', label: 'Audit log', icon: ScrollText } satisfies NavItem]
			: []),
		...(authStore.canReadSettings
			? [{ href: '/admin/storage', label: 'Storage', icon: HardDrive } satisfies NavItem]
			: []),
		...(authStore.canReadSettings
			? [
					{
						href: '/admin/mirroring',
						label: 'Mirroring',
						icon: HardDriveDownload
					} satisfies NavItem
				]
			: []),
		...(authStore.canReadSettings
			? [
					{ href: '/admin/git', label: 'Git', icon: GitBranch } satisfies NavItem,
					{ href: '/admin/mail', label: 'Mail', icon: Mail } satisfies NavItem,
					{ href: '/admin/runners', label: 'Runners', icon: Cpu } satisfies NavItem
				]
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

	const sideNavItems = $derived(
		navItems.map((i) => ({ href: resolve(i.href), label: i.label, icon: i.icon }))
	);

	function isActive(href: string): boolean {
		const current = page.url.pathname.replace(/\/$/, '');
		if (href === '/admin') return current === href;
		return current === href || current.startsWith(`${href}/`);
	}

	onMount(() => {
		if (!authStore.canAccessAdmin) {
			goto(resolve('/'));
		}
	});
</script>

<PageHeader title="Administration" subtitle="Manage this Distroface instance" />

<div class="flex flex-col gap-8 md:flex-row">
	<SideNav items={sideNavItems} {isActive} />
	<div class="min-w-0 flex-1">
		{#key page.url.pathname}
			<div class="page-enter">
				{@render children?.()}
			</div>
		{/key}
	</div>
</div>

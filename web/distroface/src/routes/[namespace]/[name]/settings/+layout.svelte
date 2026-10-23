<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import SideNav from '$lib/components/side-nav.svelte';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import {
		Settings,
		GitBranch,
		Tag,
		Users,
		KeyRound,
		Key,
		Webhook,
		Database,
		HardDriveDownload,
		Upload,
		Mail,
		Play,
		Bot,
		LockKeyhole,
		AlertTriangle
	} from '@lucide/svelte';

	let { children } = $props();

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');

	$effect(() => {
		void gitRepoStore.load(namespace, name);
	});

	// Non admins bounce back to the code tab
	$effect(() => {
		if (!gitRepoStore.loading && gitRepoStore.repo && !gitRepoStore.canAdmin) {
			void goto(resolve('/[namespace]/[name]', { namespace, name }));
		}
	});

	type SettingsRoute =
		| '/[namespace]/[name]/settings'
		| '/[namespace]/[name]/settings/branches'
		| '/[namespace]/[name]/settings/tags'
		| '/[namespace]/[name]/settings/collaborators'
		| '/[namespace]/[name]/settings/keys'
		| '/[namespace]/[name]/settings/tokens'
		| '/[namespace]/[name]/settings/webhooks'
		| '/[namespace]/[name]/settings/lfs'
		| '/[namespace]/[name]/settings/mirror'
		| '/[namespace]/[name]/settings/push-mirrors'
		| '/[namespace]/[name]/settings/email'
		| '/[namespace]/[name]/settings/actions'
		| '/[namespace]/[name]/settings/agents'
		| '/[namespace]/[name]/settings/secrets'
		| '/[namespace]/[name]/settings/danger';
	type NavItem = { route: SettingsRoute; label: string; icon: typeof Settings; danger?: boolean };

	const navItems: NavItem[] = [
		{ route: '/[namespace]/[name]/settings', label: 'General', icon: Settings },
		{ route: '/[namespace]/[name]/settings/branches', label: 'Branches', icon: GitBranch },
		{ route: '/[namespace]/[name]/settings/tags', label: 'Tags', icon: Tag },
		{ route: '/[namespace]/[name]/settings/collaborators', label: 'Collaborators', icon: Users },
		{ route: '/[namespace]/[name]/settings/keys', label: 'Deploy keys', icon: KeyRound },
		{ route: '/[namespace]/[name]/settings/tokens', label: 'Deploy tokens', icon: Key },
		{ route: '/[namespace]/[name]/settings/webhooks', label: 'Webhooks', icon: Webhook },
		{ route: '/[namespace]/[name]/settings/lfs', label: 'LFS', icon: Database },
		{ route: '/[namespace]/[name]/settings/mirror', label: 'Mirror', icon: HardDriveDownload },
		{ route: '/[namespace]/[name]/settings/push-mirrors', label: 'Push mirrors', icon: Upload },
		{ route: '/[namespace]/[name]/settings/email', label: 'Email', icon: Mail },
		{ route: '/[namespace]/[name]/settings/actions', label: 'Actions', icon: Play },
		{ route: '/[namespace]/[name]/settings/agents', label: 'Agents', icon: Bot },
		{ route: '/[namespace]/[name]/settings/secrets', label: 'Secrets', icon: LockKeyhole },
		{
			route: '/[namespace]/[name]/settings/danger',
			label: 'Danger zone',
			icon: AlertTriangle,
			danger: true
		}
	];

	const isActive = (href: string) => {
		const current = page.url.pathname.replace(/\/$/, '');
		if (href.endsWith('/settings')) return current === href;
		return current === href || current.startsWith(`${href}/`);
	};

	const sideNavItems = $derived(
		navItems.map((i) => ({
			href: resolve(i.route, { namespace, name }),
			label: i.label,
			icon: i.icon,
			danger: i.danger
		}))
	);
</script>

{#if gitRepoStore.loading && !gitRepoStore.repo}
	<Skeleton class="h-72 w-full rounded-lg" />
{:else if gitRepoStore.canAdmin}
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
{/if}

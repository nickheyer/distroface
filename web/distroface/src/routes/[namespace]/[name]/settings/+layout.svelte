<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import {
		Settings, GitBranch, Tag, Users, KeyRound, Key, Webhook, Database,
		HardDriveDownload, Upload, Mail, Play, AlertTriangle
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
		{ route: '/[namespace]/[name]/settings/danger', label: 'Danger zone', icon: AlertTriangle, danger: true }
	];

	const isActive = (href: string) => page.url.pathname.replace(/\/$/, '') === href;
</script>

{#if gitRepoStore.loading && !gitRepoStore.repo}
	<Skeleton class="h-72 w-full rounded-xl" />
{:else if gitRepoStore.canAdmin}
	<div class="flex flex-col md:flex-row gap-8">
		<nav class="md:w-52 shrink-0">
			<div class="flex md:flex-col gap-0.5 overflow-x-auto md:overflow-visible pb-2 md:pb-0 md:sticky md:top-20">
				{#each navItems as item (item.route)}
					{@const href = resolve(item.route, { namespace, name })}
					<a
						href={resolve(item.route, { namespace, name })}
						class="flex items-center gap-2.5 px-3 py-2 rounded-lg text-sm font-medium whitespace-nowrap transition-colors {isActive(href)
							? item.danger
								? 'bg-destructive/10 text-destructive'
								: 'bg-accent text-accent-foreground'
							: item.danger
								? 'text-destructive/70 hover:text-destructive hover:bg-destructive/5'
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
{/if}

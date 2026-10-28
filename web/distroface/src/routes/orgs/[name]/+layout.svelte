<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { setContext } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import {
		Building,
		Package,
		Archive,
		Users,
		Webhook,
		Globe,
		ShieldCheck,
		Settings,
		GitBranch,
		Users as UsersRound,
		Tag,
		Kanban,
		Play,
		Bot,
		Key,
		Mail
	} from '$lib/icons';
	import { OrgContext, ORG_CONTEXT_KEY } from '$lib/org-context.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import SideNav from '$lib/components/side-nav.svelte';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime } from '$lib/utils';
	import { LayoutDashboard, Calendar } from '$lib/icons';

	let { children } = $props();

	const ctx = new OrgContext();
	setContext(ORG_CONTEXT_KEY, ctx);

	$effect(() => {
		const name = page.params.name;
		if (name && name !== ctx.name) ctx.load(name);
	});

	type NavItem = { href: string; label: string; icon: typeof Package };

	const navItems: NavItem[] = $derived.by(() => {
		const name = page.params.name ?? '';
		const git = configStore.gitEnabled;
		const items: NavItem[] = [
			{ href: resolve('/orgs/[name]', { name }), label: 'Overview', icon: LayoutDashboard },
			{ href: resolve('/orgs/[name]/images', { name }), label: 'Images', icon: Package },
			{ href: resolve('/orgs/[name]/artifacts', { name }), label: 'Artifacts', icon: Archive }
		];
		if (git) {
			items.push({ href: `/orgs/${name}/repos`, label: 'Git', icon: GitBranch });
		}
		items.push({ href: resolve('/orgs/[name]/members', { name }), label: 'Members', icon: Users });
		if (git) {
			items.push({
				href: resolve('/orgs/[name]/teams', { name }),
				label: 'Teams',
				icon: UsersRound
			});
		}
		if (ctx.canAdmin) {
			if (git) {
				items.push(
					{ href: resolve('/orgs/[name]/labels', { name }), label: 'Labels', icon: Tag },
					{
						href: resolve('/orgs/[name]/projects', { name }),
						label: 'Projects',
						icon: Kanban
					},
					{ href: resolve('/orgs/[name]/actions', { name }), label: 'Actions', icon: Play },
					{ href: resolve('/orgs/[name]/agents', { name }), label: 'Agents', icon: Bot },
					{ href: resolve('/orgs/[name]/secrets', { name }), label: 'Secrets', icon: Key }
				);
			}
			items.push(
				{ href: resolve('/orgs/[name]/email', { name }), label: 'Email', icon: Mail },
				{ href: resolve('/orgs/[name]/webhooks', { name }), label: 'Webhooks', icon: Webhook },
				{ href: resolve('/orgs/[name]/portals', { name }), label: 'Portals', icon: Globe },
				{
					href: resolve('/orgs/[name]/certificates', { name }),
					label: 'Certificates',
					icon: ShieldCheck
				},
				{ href: resolve('/orgs/[name]/settings', { name }), label: 'Settings', icon: Settings }
			);
		} else if (ctx.isMember) {
			items.push({
				href: resolve('/orgs/[name]/settings', { name }),
				label: 'Membership',
				icon: Settings
			});
		}
		return items;
	});

	function isActive(href: string): boolean {
		const current = page.url.pathname.replace(/\/$/, '');
		if (href === resolve('/orgs/[name]', { name: page.params.name ?? '' })) return current === href;
		return current === href || current.startsWith(`${href}/`);
	}
</script>

<div class="space-y-6">
	<nav class="flex items-center gap-1.5 text-sm text-muted-foreground" aria-label="Breadcrumb">
		<a href={resolve('/orgs')} class="transition-colors hover:text-foreground">Organizations</a>
		<span class="text-muted-foreground/50">/</span>
		<span class="font-medium text-foreground">{ctx.name}</span>
	</nav>

	{#if ctx.loading}
		<div class="flex items-center gap-4 border-b pb-5">
			<Skeleton class="size-12 rounded-lg" />
			<div class="space-y-2">
				<Skeleton class="h-6 w-48" />
				<Skeleton class="h-4 w-32" />
			</div>
		</div>
	{:else if ctx.org}
		<div class="flex items-start gap-4 border-b pb-5">
			<div
				class="flex size-16 shrink-0 items-center justify-center rounded-xl border bg-muted text-lg font-semibold text-muted-foreground uppercase"
				aria-hidden="true"
			>
				{ctx.org.name.slice(0, 2)}
			</div>
			<div class="min-w-0 flex-1">
				<div class="flex flex-wrap items-baseline gap-x-2">
					<h1 class="truncate text-xl leading-7 font-semibold tracking-tight">
						{ctx.org.displayName || ctx.org.name}
					</h1>
					{#if ctx.org.displayName && ctx.org.displayName !== ctx.org.name}
						<span class="truncate text-sm text-muted-foreground">{ctx.org.name}</span>
					{/if}
				</div>
				{#if ctx.org.description}
					<p class="mt-0.5 line-clamp-2 text-sm text-muted-foreground">{ctx.org.description}</p>
				{/if}
				<div
					class="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground"
				>
					<a
						href={resolve('/orgs/[name]/members', { name: ctx.org.name })}
						class="flex shrink-0 items-center gap-1 hover:text-foreground"
					>
						<Users class="size-3.5" />
						{ctx.org.memberCount} member{ctx.org.memberCount !== 1 ? 's' : ''}
					</a>
					{#if ctx.org.teamsCount > 0}
						<span class="flex shrink-0 items-center gap-1">
							<Building class="size-3.5" />
							{ctx.org.teamsCount} team{ctx.org.teamsCount !== 1 ? 's' : ''}
						</span>
					{/if}
					{#if ctx.org.createdAt}
						<span class="flex shrink-0 items-center gap-1">
							<Calendar class="size-3.5" />
							Created {relativeTime(timestampDate(ctx.org.createdAt))}
						</span>
					{/if}
				</div>
			</div>
		</div>

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
	{:else}
		<div class="flex flex-col items-center rounded-lg border bg-card px-6 py-14 text-center">
			<Building class="mb-3 size-6 text-muted-foreground/70" />
			<h2 class="text-sm font-semibold">Organization not found</h2>
			<p class="mt-1 text-sm text-muted-foreground">
				{ctx.name} does not exist or you don't have access.
			</p>
			<Button variant="outline" class="mt-4" onclick={() => goto(resolve('/orgs'))}
				>Back to organizations</Button
			>
		</div>
	{/if}
</div>

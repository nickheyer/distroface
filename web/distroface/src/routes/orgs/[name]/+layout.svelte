<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { setContext } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import {
		Building2,
		Package,
		Archive,
		Users,
		Webhook,
		Globe,
		ShieldCheck,
		Settings,
		GitBranch,
		UsersRound,
		Tag,
		SquareKanban,
		Play,
		Bot,
		KeyRound,
		Mail
	} from '@lucide/svelte';
	import { OrgContext, ORG_CONTEXT_KEY } from '$lib/org-context.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import SideNav from '$lib/components/side-nav.svelte';

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
			{ href: resolve('/orgs/[name]', { name }), label: 'Images', icon: Package },
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
						icon: SquareKanban
					},
					{ href: resolve('/orgs/[name]/actions', { name }), label: 'Actions', icon: Play },
					{ href: resolve('/orgs/[name]/agents', { name }), label: 'Agents', icon: Bot },
					{ href: resolve('/orgs/[name]/secrets', { name }), label: 'Secrets', icon: KeyRound }
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
				class="flex size-12 shrink-0 items-center justify-center rounded-lg border bg-muted text-muted-foreground"
			>
				<Building2 class="size-5" />
			</div>
			<div class="min-w-0 flex-1">
				<h1 class="truncate text-xl leading-7 font-semibold tracking-tight">
					{ctx.org.displayName || ctx.org.name}
				</h1>
				<div
					class="mt-0.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-muted-foreground"
				>
					{#if ctx.org.description}
						<span class="truncate">{ctx.org.description}</span>
					{/if}
					<span class="flex shrink-0 items-center gap-1">
						<Users class="size-3.5" />
						{ctx.org.memberCount} member{ctx.org.memberCount !== 1 ? 's' : ''}
					</span>
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
			<Building2 class="mb-3 size-6 text-muted-foreground/70" />
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

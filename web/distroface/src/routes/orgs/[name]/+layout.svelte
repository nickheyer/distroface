<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { setContext } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import {
		Building2, Package, Archive, Users, Webhook, Globe, ShieldCheck, Settings
	} from '@lucide/svelte';
	import { OrgContext, ORG_CONTEXT_KEY } from '$lib/org-context.svelte';

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
		const items: NavItem[] = [
			{ href: resolve('/orgs/[name]', { name }), label: 'Repositories', icon: Package },
			{ href: resolve('/orgs/[name]/artifacts', { name }), label: 'Artifacts', icon: Archive },
			{ href: resolve('/orgs/[name]/members', { name }), label: 'Members', icon: Users }
		];
		if (ctx.canAdmin) {
			items.push(
				{ href: resolve('/orgs/[name]/webhooks', { name }), label: 'Webhooks', icon: Webhook },
				{ href: resolve('/orgs/[name]/portals', { name }), label: 'Portals', icon: Globe },
				{ href: resolve('/orgs/[name]/certificates', { name }), label: 'Certificates', icon: ShieldCheck },
				{ href: resolve('/orgs/[name]/settings', { name }), label: 'Settings', icon: Settings }
			);
		}
		return items;
	});

	function isActive(href: string): boolean {
		return page.url.pathname === href;
	}
</script>

<div class="space-y-6">
	<nav class="flex items-center gap-1.5 text-sm text-muted-foreground">
		<a href={resolve('/orgs')} class="hover:text-foreground transition-colors">Organizations</a>
		<span>/</span>
		<span class="text-foreground font-medium">{ctx.name}</span>
	</nav>

	{#if ctx.loading}
		<div class="flex items-center gap-4">
			<Skeleton class="h-14 w-14 rounded-xl" />
			<div class="space-y-2">
				<Skeleton class="h-7 w-48" />
				<Skeleton class="h-4 w-32" />
			</div>
		</div>
	{:else if ctx.org}
		<div class="flex items-start gap-4">
			<div class="h-14 w-14 rounded-xl bg-primary/10 flex items-center justify-center shrink-0">
				<Building2 class="h-7 w-7 text-primary" />
			</div>
			<div class="flex-1 min-w-0 space-y-1">
				<h1 class="text-2xl font-bold tracking-tight">{ctx.org.displayName || ctx.org.name}</h1>
				<div class="flex items-center gap-3 text-sm text-muted-foreground">
					{#if ctx.org.description}
						<span class="truncate">{ctx.org.description}</span>
					{/if}
					<span class="flex items-center gap-1 shrink-0">
						<Users class="h-3.5 w-3.5" />
						{ctx.org.memberCount} member{ctx.org.memberCount !== 1 ? 's' : ''}
					</span>
				</div>
			</div>
		</div>

		<div class="flex flex-col md:flex-row gap-8">
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
	{:else}
		<div class="text-center py-12">
			<div class="h-12 w-12 rounded-xl bg-muted/50 flex items-center justify-center mx-auto mb-4">
				<Building2 class="h-6 w-6 text-muted-foreground/50" />
			</div>
			<h2 class="text-lg font-semibold">Organization not found</h2>
			<p class="text-[13px] text-muted-foreground mt-1">
				{ctx.name} does not exist or you don't have access.
			</p>
			<Button variant="outline" class="mt-4" onclick={() => goto(resolve('/orgs'))}>
				Back to Organizations
			</Button>
		</div>
	{/if}
</div>

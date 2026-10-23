<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { getContext } from 'svelte';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import FormPage from '$lib/components/form-page.svelte';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';

	let { children } = $props();

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgName = $derived(page.params.name ?? '');
	const tabs = $derived([
		{ href: resolve('/orgs/[name]/agents', { name: orgName }), label: 'Settings' },
		{ href: resolve('/orgs/[name]/agents/definitions', { name: orgName }), label: 'Agents' },
		{ href: resolve('/orgs/[name]/agents/connectors', { name: orgName }), label: 'Connectors' },
		{ href: resolve('/orgs/[name]/agents/usage', { name: orgName }), label: 'Usage' }
	]);

	// Create and edit pages bring their own header
	const nested = $derived(/\/agents\/(definitions|connectors)\/[^/]+\/?$/.test(page.url.pathname));

	$effect(() => {
		if (!ctx.loading && ctx.org && !ctx.canAdmin) {
			goto(resolve('/orgs/[name]', { name: orgName }));
		}
	});
</script>

{#if ctx.loading || !ctx.org}
	<Skeleton class="h-72 w-full rounded-lg" />
{:else if !ctx.canAdmin}
	<Skeleton class="h-72 w-full rounded-lg" />
{:else if nested}
	{#key ctx.org.id}
		{@render children?.()}
	{/key}
{:else}
	<FormPage
		level="section"
		title="Agents"
		description="Policy, agents, and connectors every repository of this organization inherits."
		{tabs}
	>
		{#key ctx.org.id}
			{@render children?.()}
		{/key}
	</FormPage>
{/if}

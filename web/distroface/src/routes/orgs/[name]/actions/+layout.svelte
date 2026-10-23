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
		{ href: resolve('/orgs/[name]/actions', { name: orgName }), label: 'Settings' },
		{ href: resolve('/orgs/[name]/actions/runners', { name: orgName }), label: 'Runners' }
	]);

	$effect(() => {
		if (!ctx.loading && ctx.org && !ctx.canAdmin) {
			goto(resolve('/orgs/[name]', { name: orgName }));
		}
	});
</script>

{#if ctx.loading || !ctx.org}
	<Skeleton class="h-72 w-full rounded-lg" />
{:else if ctx.canAdmin}
	<FormPage
		level="section"
		title="Actions"
		description="Workflow policy and runners shared by every repository of this organization."
		{tabs}
	>
		{#key ctx.org.id}
			{@render children?.()}
		{/key}
	</FormPage>
{/if}

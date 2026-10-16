<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { getContext } from 'svelte';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import RunnerManager from '$lib/components/git/runner-manager.svelte';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgName = $derived(page.params.name ?? '');
	const orgId = $derived(ctx.org?.id ?? '');

	$effect(() => {
		if (!ctx.loading && ctx.org && !ctx.canAdmin) {
			goto(resolve('/orgs/[name]', { name: orgName }));
		}
	});
</script>

{#if ctx.loading || !ctx.org}
	<Skeleton class="h-48 w-full rounded-xl" />
{:else if ctx.canAdmin}
	{#key orgId}
		<RunnerManager
			scope={{ kind: 'org', id: orgId }}
			cliScope={`org:${orgName}`}
			canManage={ctx.canAdmin}
			description="Executors shared by every repository in this organization."
		/>
	{/key}
{/if}

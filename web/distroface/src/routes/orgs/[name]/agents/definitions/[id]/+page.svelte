<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { getContext } from 'svelte';
	import AgentFormPage from '$lib/components/git/agent-form-page.svelte';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgName = $derived(page.params.name ?? '');
	const orgId = $derived(ctx.org?.id ?? '');
	const id = $derived(page.params.id ?? '');
	const listHref = $derived(resolve('/orgs/[name]/agents/definitions', { name: orgName }));
</script>

<svelte:head>
	<title>Edit agent - {orgName}</title>
</svelte:head>

<AgentFormPage
	scope={{ kind: 'org', id: orgId }}
	{id}
	{listHref}
	canManage={ctx.canAdmin}
	{orgId}
/>

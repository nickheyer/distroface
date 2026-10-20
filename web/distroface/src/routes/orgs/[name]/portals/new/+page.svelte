<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { getContext } from 'svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import PortalForm from '../portal-form.svelte';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';
	import { configStore } from '$lib/stores/config.svelte';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgName = $derived(page.params.name ?? '');
	const orgId = $derived(ctx.org?.id ?? '');
	const mainPort = $derived(configStore.mainPort);
	const listHref = $derived(resolve('/orgs/[name]/portals', { name: orgName }));

	$effect(() => {
		if (!ctx.loading && ctx.org && !ctx.canAdmin) {
			goto(resolve('/orgs/[name]', { name: orgName }));
		}
	});
</script>

<svelte:head>
	<title>New portal - {orgName}</title>
</svelte:head>

<FormPage
	level="section"
	title="New portal"
	description="Give {orgName} its own address. Clients on it only ever see this organization."
	backHref={listHref}
	backLabel="Portals"
>
	<PortalForm {orgName} {orgId} {mainPort} />
</FormPage>

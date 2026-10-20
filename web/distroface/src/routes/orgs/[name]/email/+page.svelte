<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { getContext } from 'svelte';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import FormPage from '$lib/components/form-page.svelte';
	import MailSettingsCard from '$lib/components/git/mail-settings-card.svelte';
	import { orgScope, systemScope } from '$lib/settings-utils';
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
	<Skeleton class="h-72 w-full rounded-lg" />
{:else if ctx.canAdmin}
	<FormPage
		level="section"
		title="Email"
		description="Send from this organization's own relay and sender, or inherit the instance mailer."
	>
		{#key orgId}
			<MailSettingsCard scope={orgScope(orgId)} parentScope={systemScope} canEdit={ctx.canAdmin} />
		{/key}
	</FormPage>
{/if}

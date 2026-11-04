<script lang="ts">
	import { getContext } from 'svelte';
	import AgentsSettingsCard from '$lib/components/git/agents-settings-card.svelte';
	import { orgScope, systemScope } from '$lib/settings-utils';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgId = $derived(ctx.org?.id ?? '');
</script>

<AgentsSettingsCard scope={orgScope(orgId)} parentScope={systemScope} canEdit={ctx.canAdmin} />

<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { getContext } from 'svelte';
	import AgentProviderManager from '$lib/components/git/agent-provider-manager.svelte';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgName = $derived(page.params.name ?? '');
	const orgId = $derived(ctx.org?.id ?? '');
</script>

<AgentProviderManager
	scope={{ kind: 'org', id: orgId }}
	newHref={resolve('/orgs/[name]/agents/connectors/new', { name: orgName })}
	editHref={(id) => resolve('/orgs/[name]/agents/connectors/[id]', { name: orgName, id })}
	includeInherited
	canManage={ctx.canAdmin}
	description="Inference endpoints shared by every repository in this organization."
/>

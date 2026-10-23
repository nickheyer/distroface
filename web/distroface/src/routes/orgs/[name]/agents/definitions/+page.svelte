<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { getContext } from 'svelte';
	import AgentManager from '$lib/components/git/agent-manager.svelte';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgName = $derived(page.params.name ?? '');
	const orgId = $derived(ctx.org?.id ?? '');
</script>

<AgentManager
	scope={{ kind: 'org', id: orgId }}
	newHref={resolve('/orgs/[name]/agents/definitions/new', { name: orgName })}
	editHref={(id) => resolve('/orgs/[name]/agents/definitions/[id]', { name: orgName, id })}
	includeInherited
	canManage={ctx.canAdmin}
	description="Agents available to every repository in this organization."
/>

<script lang="ts">
	import { getContext } from 'svelte';
	import AgentUsage from '$lib/components/git/agent-usage.svelte';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgId = $derived(ctx.org?.id ?? '');
</script>

<AgentUsage scope={{ kind: 'org', id: orgId }} />

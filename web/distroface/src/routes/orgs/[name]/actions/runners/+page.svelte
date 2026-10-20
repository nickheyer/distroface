<script lang="ts">
	import { getContext } from 'svelte';
	import RunnerManager from '$lib/components/git/runner-manager.svelte';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgId = $derived(ctx.org?.id ?? '');
</script>

<RunnerManager
	scope={{ kind: 'org', id: orgId }}
	canManage={ctx.canAdmin}
	description="Executors shared by every repository in this organization."
/>

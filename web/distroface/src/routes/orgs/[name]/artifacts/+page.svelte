<script lang="ts">
	import { page } from '$app/state';
	import { getContext } from 'svelte';
	import ArtifactRepoList from '$lib/components/artifact-repo-list.svelte';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgName = $derived(page.params.name ?? '');
</script>

<ArtifactRepoList namespace={orgName} canCreate={ctx.canAdmin} />

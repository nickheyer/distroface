<script lang="ts">
	import { GitBranch } from '$lib/icons';
	import { configStore } from '$lib/stores/config.svelte';
	import RepoExplore from '$lib/components/git/repo-explore.svelte';
	import PageHeader from '$lib/components/page-header.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
</script>

<svelte:head>
	<title>Git - Distroface</title>
</svelte:head>

{#if configStore.gitEnabled}
	<RepoExplore />
{:else}
	<PageHeader title="Git" subtitle="Git hosting is turned off on this instance" icon={GitBranch} />
	<EmptyState
		icon={GitBranch}
		message="Git is disabled"
		description="An administrator can enable git hosting under Admin, Git."
	/>
{/if}

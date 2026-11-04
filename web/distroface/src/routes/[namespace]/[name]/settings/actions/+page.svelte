<script lang="ts">
	import { Skeleton } from '$lib/components/ui/skeleton';
	import ActionsSettingsCard from '$lib/components/git/actions-settings-card.svelte';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { repoParentScope, type ScopeInit } from '$lib/settings-utils';
	import { SettingsScopeType } from '$lib/proto/distroface/v1/settings_pb';
	import { GitRepoType } from '$lib/proto/distroface/v1/types_pb';

	const repo = $derived(gitRepoStore.repo);
	const scope = $derived<ScopeInit>({ type: SettingsScopeType.REPO, scopeId: repo?.id ?? '' });

	let parentScope = $state<ScopeInit | undefined>();
	let resolvedFor = $state('');

	$effect(() => {
		if (repo && repo.id !== resolvedFor) {
			const id = repo.id;
			resolvedFor = id;
			parentScope = undefined;
			void repoParentScope(repo.namespace, repo.isOrgNamespace).then((p) => {
				if (resolvedFor === id) parentScope = p.scope;
			});
		}
	});
</script>

{#if !repo || !parentScope}
	<Skeleton class="h-56 w-full rounded-lg" />
{:else}
	<ActionsSettingsCard
		{scope}
		{parentScope}
		canEdit={gitRepoStore.canAdmin}
		showMirror={repo.type === GitRepoType.MIRROR}
	/>
{/if}

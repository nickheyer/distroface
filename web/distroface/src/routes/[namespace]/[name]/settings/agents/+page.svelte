<script lang="ts">
	import { getContext } from 'svelte';
	import AgentsSettingsCard from '$lib/components/git/agents-settings-card.svelte';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import {
		REPO_AGENTS_CONTEXT_KEY,
		type RepoAgentsContext
	} from '$lib/components/git/repo-agents-context.svelte';
	import type { ScopeInit } from '$lib/settings-utils';
	import { SettingsScopeType } from '$lib/proto/distroface/v1/settings_pb';

	const ctx = getContext<RepoAgentsContext>(REPO_AGENTS_CONTEXT_KEY);
	const scope = $derived<ScopeInit>({
		type: SettingsScopeType.REPO,
		scopeId: gitRepoStore.repo?.id ?? ''
	});
</script>

<AgentsSettingsCard {scope} parentScope={ctx.parentScope} canEdit={gitRepoStore.canAdmin} />

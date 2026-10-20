<script lang="ts">
	import { page } from '$app/state';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import ActionsSettingsCard from '$lib/components/git/actions-settings-card.svelte';
	import RunnerManager from '$lib/components/git/runner-manager.svelte';
	import { orgScope, systemScope, type ScopeInit } from '$lib/settings-utils';
	import { SettingsScopeType } from '$lib/proto/distroface/v1/settings_pb';
	import { GitRepoType } from '$lib/proto/distroface/v1/types_pb';
	import { Info } from '@lucide/svelte';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);
	const runnerScope = $derived({ kind: 'repo', id: `${namespace}/${name}` });
	const settingsScope = $derived<ScopeInit>({
		type: SettingsScopeType.REPO,
		scopeId: repo?.id ?? ''
	});

	let parentScope = $state<ScopeInit | undefined>();
	let resolvedFor = $state('');

	// Org repos inherit the org tier, user repos the instance
	async function resolveParent(ns: string, isOrg: boolean) {
		if (!isOrg) {
			parentScope = systemScope;
			return;
		}
		try {
			const resp = await rpcClient.organization.getOrganization({ name: ns }, silentCallOptions);
			parentScope = resp.organization ? orgScope(resp.organization.id) : systemScope;
		} catch {
			parentScope = systemScope;
		}
	}

	$effect(() => {
		if (repo && repo.id !== resolvedFor) {
			resolvedFor = repo.id;
			parentScope = undefined;
			void resolveParent(repo.namespace, repo.isOrgNamespace);
		}
	});
</script>

{#if !repo || !parentScope}
	<Skeleton class="h-72 w-full rounded-lg" />
{:else}
	<div class="space-y-6">
		<div>
			<h2 class="section-title">Actions</h2>
			<p class="section-subtitle">
				Runners and workflow defaults for this repository.
			</p>
		</div>

		{#if !configStore.actionsEnabled}
			<Alert>
				<Info />
				<AlertDescription
					>Workflows are turned off for this instance. Settings saved here apply once an
					administrator enables Actions.</AlertDescription
				>
			</Alert>
		{:else if !repo.units?.actions}
			<Alert>
				<Info />
				<AlertDescription
					>The Actions unit is disabled for this repository. Turn it on under General, Features.</AlertDescription
				>
			</Alert>
		{/if}

		{#key repo.id}
			<ActionsSettingsCard
				scope={settingsScope}
				{parentScope}
				canEdit={gitRepoStore.canAdmin}
				showMirror={repo.type === GitRepoType.MIRROR}
			/>
		{/key}

		<section class="border-t pt-6">
			{#key `${namespace}/${name}`}
				<RunnerManager
					scope={runnerScope}
					includeInherited
					canManage={gitRepoStore.canAdmin}
					description="Every runner that can pick up this repository's jobs."
				/>
			{/key}
		</section>
	</div>
{/if}

<script lang="ts">
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import MailSettingsCard from '$lib/components/git/mail-settings-card.svelte';
	import { orgScope, systemScope, type ScopeInit } from '$lib/settings-utils';
	import { SettingsScopeType } from '$lib/proto/distroface/v1/settings_pb';

	const repo = $derived(gitRepoStore.repo);
	const scope = $derived<ScopeInit>({ type: SettingsScopeType.REPO, scopeId: repo?.id ?? '' });

	let parentScope = $state<ScopeInit | undefined>();
	let resolvedFor = $state('');

	// Org repos inherit the org tier, user repos the instance
	async function resolveParent(namespace: string, isOrg: boolean) {
		if (!isOrg) {
			parentScope = systemScope;
			return;
		}
		try {
			const resp = await rpcClient.organization.getOrganization({ name: namespace }, silentCallOptions);
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
	<Skeleton class="h-72 w-full rounded-xl" />
{:else}
	<div class="space-y-4">
		<div>
			<h2 class="section-title">Email</h2>
			<p class="section-subtitle">Notifications for this repository go out through this relay. Fields inherit from the organization or instance until changed.</p>
		</div>
		{#key repo.id}
			<MailSettingsCard {scope} {parentScope} canEdit={gitRepoStore.canAdmin} />
		{/key}
	</div>
{/if}

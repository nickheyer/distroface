<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { Lock, ChevronLeft, Rocket } from '$lib/icons';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import EmptyState from '$lib/components/empty-state.svelte';
	import ReleaseEditor from '$lib/components/git/release-editor.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { GitUnit } from '$lib/proto/distroface/v1/git_pb';
	import type { Release } from '$lib/proto/distroface/v1/release_pb';
	import { ConnectError } from '@connectrpc/connect';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const tag = $derived(decodeURIComponent(page.params.tag ?? ''));
	const repo = $derived(gitRepoStore.repo);
	const base = $derived(resolve('/[namespace]/[name]/releases', { namespace, name }));
	const releaseHref = $derived(resolve('/[namespace]/[name]/releases/[tag]', { namespace, name, tag: encodeURIComponent(tag) }));
	const canWrite = $derived(gitRepoStore.canWrite(GitUnit.RELEASES) && !repo?.isArchived);

	let release = $state<Release | undefined>();
	let loading = $state(true);
	let error = $state('');
	let loadedKey = $state('');

	$effect(() => {
		void gitRepoStore.load(namespace, name);
	});

	$effect(() => {
		const key = `${namespace}/${name}@${tag}`;
		if (key !== loadedKey) {
			loadedKey = key;
			void load();
		}
	});

	async function load() {
		loading = true;
		error = '';
		try {
			const resp = await rpcClient.release.getRelease({ namespace, name, tag }, silentCallOptions);
			release = resp.release;
		} catch (err) {
			release = undefined;
			error = err instanceof ConnectError ? err.rawMessage || err.message : 'Failed to load release';
		} finally {
			loading = false;
		}
	}

	function onSaved(r: Release) {
		void goto(resolve('/[namespace]/[name]/releases/[tag]', { namespace, name, tag: encodeURIComponent(r.tag) }));
	}

	function onCancel() {
		void goto(resolve('/[namespace]/[name]/releases/[tag]', { namespace, name, tag: encodeURIComponent(tag) }));
	}
</script>

<svelte:head>
	<title>Edit {tag} - {namespace}/{name}</title>
</svelte:head>

<div class="space-y-4">
	<div class="section-header">
		<div class="min-w-0">
			<h2 class="section-title truncate">Edit release <span class="font-mono">{tag}</span></h2>
			<p class="section-subtitle">{release?.draft ? 'Draft, not yet visible to readers' : 'Changes apply to the published release'}</p>
		</div>
		<Button variant="outline" size="sm" class="h-8 shrink-0" href={releaseHref}>
			<ChevronLeft class="h-3.5 w-3.5 mr-1" />Release
		</Button>
	</div>

	{#if repo && !canWrite}
		<Alert>
			<Lock class="h-4 w-4" />
			<AlertDescription>
				{repo.isArchived ? 'This repository is archived and read only.' : 'You need write access to the releases unit to edit a release.'}
			</AlertDescription>
		</Alert>
	{:else if loading || !repo}
		<Skeleton class="h-96 w-full rounded-xl" />
	{:else if !release}
		<EmptyState icon={Rocket} message="Release not found" description={error || `No release is tagged ${tag}.`}>
			{#snippet actions()}
				<Button variant="outline" href={base}>Back to releases</Button>
			{/snippet}
		</EmptyState>
	{:else}
		{#key release.id}
			<ReleaseEditor {namespace} {name} defaultBranch={repo.defaultBranch} {release} {onSaved} {onCancel} />
		{/key}
	{/if}
</div>

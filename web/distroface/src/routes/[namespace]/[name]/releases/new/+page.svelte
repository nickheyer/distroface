<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { Lock, ChevronLeft } from '$lib/icons';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import ReleaseEditor from '$lib/components/git/release-editor.svelte';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { GitUnit } from '$lib/proto/distroface/v1/git_pb';
	import type { Release } from '$lib/proto/distroface/v1/release_pb';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);
	const base = $derived(resolve('/[namespace]/[name]/releases', { namespace, name }));
	const canWrite = $derived(gitRepoStore.canWrite(GitUnit.RELEASES) && !gitRepoStore.readOnly);
	const presetTag = $derived(page.url.searchParams.get('tag') ?? '');

	$effect(() => {
		void gitRepoStore.load(namespace, name);
	});

	// Drafts continue on the edit page where assets attach
	function onCreated(r: Release) {
		void goto(
			resolve('/[namespace]/[name]/releases/[tag]/edit', {
				namespace,
				name,
				tag: encodeURIComponent(r.tag)
			}),
			{ replaceState: true }
		);
	}

	function onSaved(r: Release) {
		void goto(
			resolve('/[namespace]/[name]/releases/[tag]', {
				namespace,
				name,
				tag: encodeURIComponent(r.tag)
			})
		);
	}

	function onCancel() {
		void goto(resolve('/[namespace]/[name]/releases', { namespace, name }));
	}
</script>

<svelte:head>
	<title>New release - {namespace}/{name}</title>
</svelte:head>

<div class="space-y-4">
	<div class="section-header">
		<div class="min-w-0">
			<h2 class="section-title">New release</h2>
			<p class="section-subtitle">Tag a version with notes, assets, and images</p>
		</div>
		<Button variant="outline" size="sm" class="h-8 shrink-0" href={base}>
			<ChevronLeft class="mr-1 h-3.5 w-3.5" />Releases
		</Button>
	</div>

	{#if repo && !canWrite}
		<Alert>
			<Lock class="h-4 w-4" />
			<AlertDescription>
				{gitRepoStore.readOnlyReason ||
					'You need write access to the releases unit to draft a release.'}
			</AlertDescription>
		</Alert>
	{:else if repo}
		<ReleaseEditor
			{namespace}
			{name}
			defaultBranch={repo.defaultBranch}
			initialTag={presetTag}
			{onCreated}
			{onSaved}
			{onCancel}
		/>
	{:else}
		<Skeleton class="h-96 w-full rounded-xl" />
	{/if}
</div>

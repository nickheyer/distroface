<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { Lock, ChevronLeft } from '@lucide/svelte';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import WikiEditor from '$lib/components/git/wiki-editor.svelte';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { GitUnit } from '$lib/proto/distroface/v1/git_pb';
	import type { WikiPage } from '$lib/proto/distroface/v1/wiki_pb';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);
	const base = $derived(resolve('/[namespace]/[name]/wiki', { namespace, name }));
	const canWrite = $derived(gitRepoStore.canWrite(GitUnit.WIKI) && !repo?.isArchived);
	const initialTitle = $derived(page.url.searchParams.get('title') ?? '');

	$effect(() => {
		void gitRepoStore.load(namespace, name);
	});

	function onSaved(p: WikiPage) {
		void goto(
			resolve('/[namespace]/[name]/wiki/[page]', {
				namespace,
				name,
				page: encodeURIComponent(p.title)
			})
		);
	}

	function onCancel() {
		void goto(resolve('/[namespace]/[name]/wiki', { namespace, name }));
	}
</script>

<svelte:head>
	<title>New wiki page - {namespace}/{name}</title>
</svelte:head>

<div class="space-y-4">
	<div class="section-header">
		<div class="min-w-0">
			<h2 class="section-title">New wiki page</h2>
			<p class="section-subtitle">Pages are markdown files in the wiki repository</p>
		</div>
		<Button variant="outline" class="shrink-0" href={base}>
			<ChevronLeft />Wiki
		</Button>
	</div>

	{#if repo && !canWrite}
		<Alert>
			<Lock />
			<AlertDescription>
				{repo.isArchived
					? 'This repository is archived and read only.'
					: 'You need write access to the wiki unit to add pages.'}
			</AlertDescription>
		</Alert>
	{:else if repo}
		{#key initialTitle}
			<WikiEditor {namespace} {name} {initialTitle} {onSaved} {onCancel} />
		{/key}
	{:else}
		<Skeleton class="h-96 w-full rounded-lg" />
	{/if}
</div>

<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { Lock, ChevronLeft, BookOpen } from '@lucide/svelte';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import EmptyState from '$lib/components/empty-state.svelte';
	import WikiEditor from '$lib/components/git/wiki-editor.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { GitUnit } from '$lib/proto/distroface/v1/git_pb';
	import type { WikiPage } from '$lib/proto/distroface/v1/wiki_pb';
	import { ConnectError } from '@connectrpc/connect';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const title = $derived(decodeURIComponent(page.params.page ?? ''));
	const repo = $derived(gitRepoStore.repo);
	const base = $derived(resolve('/[namespace]/[name]/wiki', { namespace, name }));
	const pageHref = $derived(
		resolve('/[namespace]/[name]/wiki/[page]', { namespace, name, page: encodeURIComponent(title) })
	);
	const canWrite = $derived(gitRepoStore.canWrite(GitUnit.WIKI) && !repo?.isArchived);

	let current = $state<WikiPage | undefined>();
	let loading = $state(true);
	let error = $state('');
	let loadedKey = $state('');

	$effect(() => {
		void gitRepoStore.load(namespace, name);
	});

	$effect(() => {
		const key = `${namespace}/${name}:${title}`;
		if (key !== loadedKey) {
			loadedKey = key;
			void load();
		}
	});

	async function load() {
		loading = true;
		error = '';
		try {
			const resp = await rpcClient.wiki.getWikiPage(
				{ namespace, name, title, sha: '' },
				silentCallOptions
			);
			current = resp.page;
		} catch (err) {
			current = undefined;
			error = err instanceof ConnectError ? err.rawMessage || err.message : 'Failed to load page';
		} finally {
			loading = false;
		}
	}

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
		void goto(
			resolve('/[namespace]/[name]/wiki/[page]', {
				namespace,
				name,
				page: encodeURIComponent(title)
			})
		);
	}
</script>

<svelte:head>
	<title>Edit {title} - Wiki - {namespace}/{name}</title>
</svelte:head>

<div class="space-y-4">
	<div class="section-header">
		<div class="min-w-0">
			<h2 class="section-title truncate">Edit {title}</h2>
			<p class="section-subtitle">Pages are markdown files in the wiki repository</p>
		</div>
		<Button variant="outline" class="shrink-0" href={pageHref}>
			<ChevronLeft />Page
		</Button>
	</div>

	{#if repo && !canWrite}
		<Alert>
			<Lock />
			<AlertDescription>
				{repo.isArchived
					? 'This repository is archived and read only.'
					: 'You need write access to the wiki unit to edit pages.'}
			</AlertDescription>
		</Alert>
	{:else if loading || !repo}
		<Skeleton class="h-96 w-full rounded-lg" />
	{:else if !current}
		<EmptyState
			icon={BookOpen}
			message="Page not found"
			description={error || `There is no wiki page titled ${title}.`}
		>
			{#snippet actions()}
				<Button variant="outline" href={base}>Back to wiki</Button>
				<Button
					href={resolve(
						`${resolve('/[namespace]/[name]/wiki/new', { namespace, name })}?title=${encodeURIComponent(title)}` as `/${string}`
					)}>Create page</Button
				>
			{/snippet}
		</EmptyState>
	{:else}
		{#key current.lastCommit?.sha ?? current.title}
			<WikiEditor {namespace} {name} page={current} {onSaved} {onCancel} />
		{/key}
	{/if}
</div>

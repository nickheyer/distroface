<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { FileX } from '$lib/icons';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import EmptyState from '$lib/components/empty-state.svelte';
	import RefPicker from '$lib/components/git/ref-picker.svelte';
	import FileView from '$lib/components/git/file-view.svelte';
	import LatestCommitBar from '$lib/components/git/latest-commit-bar.svelte';
	import PathBreadcrumb from '$lib/components/git/path-breadcrumb.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { GitUnit } from '$lib/proto/distroface/v1/git_pb';
	import type { GitBlob, GitCommit } from '$lib/proto/distroface/v1/git_pb';

	const repo = $derived(gitRepoStore.repo!);
	const namespace = $derived(repo.namespace);
	const name = $derived(repo.name);
	const ref = $derived(page.params.ref ?? '');
	const path = $derived(page.params.path ?? '');
	const canEdit = $derived(gitRepoStore.canWrite(GitUnit.CODE) && !repo.isArchived);

	let blob = $state<GitBlob | undefined>();
	let lastCommit = $state<GitCommit | undefined>();
	let loading = $state(true);
	let missing = $state(false);
	let seq = 0;

	async function load() {
		const mine = ++seq;
		loading = true;
		missing = false;
		try {
			const resp = await rpcClient.gitFiles.getBlob(
				{ namespace, name, ref, path },
				silentCallOptions
			);
			if (mine !== seq) return;
			blob = resp.blob;
			lastCommit = resp.lastCommit;
			missing = !resp.blob;
		} catch {
			if (mine !== seq) return;
			blob = undefined;
			lastCommit = undefined;
			missing = true;
		} finally {
			if (mine === seq) loading = false;
		}
	}

	$effect(() => {
		void ref;
		void path;
		void repo.id;
		load();
	});

	function onRef(next: string) {
		goto(
			resolve('/[namespace]/[name]/blob/[ref]/[...path]', {
				namespace,
				name,
				ref: encodeURIComponent(next),
				path
			})
		);
	}
</script>

<div class="space-y-4">
	<div class="flex flex-wrap items-center gap-3">
		<RefPicker
			{namespace}
			{name}
			current={ref}
			defaultBranch={repo.defaultBranch}
			onSelect={onRef}
		/>
		<PathBreadcrumb {namespace} {name} {ref} {path} leafIsFile />
	</div>

	{#if loading && !blob}
		<Skeleton class="h-12 w-full rounded-lg" />
		<Skeleton class="h-96 w-full rounded-lg" />
	{:else if missing || !blob}
		<EmptyState
			icon={FileX}
			message="File not found"
			description="{path} does not exist at {ref}."
		/>
	{:else}
		<div class="box">
			<LatestCommitBar commit={lastCommit} {namespace} {name} {ref} {path} />
		</div>
		{#key blob.sha + blob.path}
			<FileView {blob} {namespace} {name} {ref} {canEdit} />
		{/key}
	{/if}
</div>

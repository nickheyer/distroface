<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { FileX } from '@lucide/svelte';
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
			const resp = await rpcClient.gitFiles.getBlob({ namespace, name, ref, path }, silentCallOptions);
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
		goto(resolve('/[namespace]/[name]/blob/[ref]/[...path]', { namespace, name, ref: encodeURIComponent(next), path }));
	}

	function onDeleted(branch: string) {
		const dir = path.includes('/') ? path.slice(0, path.lastIndexOf('/')) : '';
		gitRepoStore.reload();
		goto(resolve('/[namespace]/[name]/tree/[ref]/[...path]', { namespace, name, ref: encodeURIComponent(branch), path: dir }));
	}
</script>

<div class="space-y-4">
	<div class="flex items-center gap-3 flex-wrap">
		<RefPicker {namespace} {name} current={ref} defaultBranch={repo.defaultBranch} onSelect={onRef} />
		<PathBreadcrumb {namespace} {name} {ref} {path} leafIsFile />
	</div>

	{#if loading && !blob}
		<Skeleton class="h-12 w-full rounded-xl" />
		<Skeleton class="h-96 w-full rounded-xl" />
	{:else if missing || !blob}
		<EmptyState icon={FileX} message="File not found" description="{path} does not exist at {ref}." />
	{:else}
		<LatestCommitBar commit={lastCommit} {namespace} {name} {ref} {path} />
		{#key blob.sha + blob.path}
			<FileView {blob} {namespace} {name} {ref} {canEdit} {onDeleted} />
		{/key}
	{/if}
</div>

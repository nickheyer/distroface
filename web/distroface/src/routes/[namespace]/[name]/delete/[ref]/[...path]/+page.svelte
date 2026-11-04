<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { toast } from 'svelte-sonner';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import { Button } from '$lib/components/ui/button';
	import CommitForm from '$lib/components/git/commit-form.svelte';
	import { AlertTriangle, Lock, ChevronLeft, FileX } from '$lib/icons';
	import { formatBytes } from '$lib/utils';
	import { GitUnit, FileOperationKind } from '$lib/proto/distroface/v1/git_pb';
	import { ConnectError } from '@connectrpc/connect';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const ref = $derived(page.params.ref ?? '');
	const path = $derived(page.params.path ?? '');
	const blobHref = $derived(
		resolve('/[namespace]/[name]/blob/[ref]/[...path]', {
			namespace,
			name,
			ref: encodeURIComponent(ref),
			path
		})
	);
	const canWrite = $derived(gitRepoStore.canWrite(GitUnit.CODE) && !gitRepoStore.readOnly);

	let size = $state(0);
	let headSha = $state('');
	let loading = $state(true);
	let loadError = $state('');
	let loadedKey = $state('');

	let summary = $state('');
	let description = $state('');
	let mode = $state<'direct' | 'branch'>('direct');
	let newBranch = $state('');
	let submitting = $state(false);

	const fileName = $derived(path.split('/').pop() ?? path);
	const parentDir = $derived(path.includes('/') ? path.slice(0, path.lastIndexOf('/')) : '');

	$effect(() => {
		void gitRepoStore.load(namespace, name);
	});

	$effect(() => {
		const key = `${namespace}/${name}@${ref}:${path}`;
		if (key !== loadedKey) {
			loadedKey = key;
			void load();
		}
	});

	async function load() {
		loading = true;
		loadError = '';
		try {
			const resp = await rpcClient.gitFiles.getBlob(
				{ namespace, name, ref, path },
				silentCallOptions
			);
			headSha = resp.sha;
			if (!resp.blob) loadError = 'File not found';
			else size = Number(resp.blob.size);
		} catch (err) {
			loadError =
				err instanceof ConnectError ? err.rawMessage || err.message : 'Failed to load file';
		} finally {
			loading = false;
		}
	}

	async function commit() {
		submitting = true;
		try {
			const message = [summary.trim() || `Delete ${fileName}`, description.trim()]
				.filter(Boolean)
				.join('\n\n');
			const target = mode === 'branch' ? newBranch.trim() : '';
			const resp = await rpcClient.gitFiles.updateFiles({
				namespace,
				name,
				branch: ref,
				newBranch: target,
				message,
				expectedHeadSha: headSha,
				operations: [{ kind: FileOperationKind.DELETE, path }]
			});
			toast.success('File deleted');
			gitRepoStore.reload();
			const landed = resp.branch || target || ref;
			if (target) {
				await goto(
					resolve(
						`${resolve('/[namespace]/[name]/pulls/new', { namespace, name })}?base=${encodeURIComponent(ref)}&head=${encodeURIComponent(landed)}` as `/${string}`
					)
				);
			} else {
				await goto(
					resolve('/[namespace]/[name]/tree/[ref]/[...path]', {
						namespace,
						name,
						ref: encodeURIComponent(landed),
						path: parentDir
					})
				);
			}
		} catch {
			// Error interceptor already toasted
		} finally {
			submitting = false;
		}
	}

	function cancel() {
		void goto(resolve(blobHref as `/${string}`));
	}
</script>

<svelte:head>
	<title>Delete {fileName} - {namespace}/{name}</title>
</svelte:head>

<div class="space-y-4">
	<div class="section-header">
		<div class="min-w-0">
			<h2 class="section-title truncate">Delete {fileName}</h2>
			<p class="section-subtitle truncate">
				<span class="font-mono">{path}</span> on <span class="font-mono">{ref}</span>
			</p>
		</div>
		<Button variant="outline" class="shrink-0" href={blobHref}>
			<ChevronLeft />File
		</Button>
	</div>

	{#if gitRepoStore.repo && !canWrite}
		<Alert>
			<Lock />
			<AlertDescription>
				{gitRepoStore.readOnlyReason ||
					'You need write access to delete files here. Fork the repository to propose changes.'}
			</AlertDescription>
		</Alert>
	{/if}

	{#if loading}
		<Skeleton class="h-48 w-full rounded-lg" />
	{:else if loadError}
		<Alert variant="destructive">
			<AlertTriangle />
			<AlertDescription>{loadError}</AlertDescription>
		</Alert>
		<Button variant="outline" onclick={cancel}>Back to file</Button>
	{:else}
		<Alert variant="destructive">
			<FileX />
			<AlertDescription>
				The commit removes <span class="font-mono">{path}</span> ({formatBytes(size)}) from
				<span class="font-mono">{ref}</span>. History keeps every earlier version.
			</AlertDescription>
		</Alert>
		<CommitForm
			bind:summary
			bind:description
			bind:mode
			bind:newBranch
			branch={ref}
			placeholder="Delete {fileName}"
			submitLabel="Commit deletion"
			{submitting}
			disabled={!canWrite}
			onSubmit={commit}
			onCancel={cancel}
		/>
	{/if}
</div>

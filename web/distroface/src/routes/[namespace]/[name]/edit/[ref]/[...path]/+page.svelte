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
	import FormCard from '$lib/components/form-card.svelte';
	import CodeEditor from '$lib/components/git/code-editor.svelte';
	import CommitForm from '$lib/components/git/commit-form.svelte';
	import { FileCode, AlertTriangle, Lock, ChevronLeft } from '$lib/icons';
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

	let content = $state('');
	let original = $state('');
	let headSha = $state('');
	let loading = $state(true);
	let loadError = $state('');
	let unsupported = $state('');
	let executable = $state(false);
	let loadedKey = $state('');

	let summary = $state('');
	let description = $state('');
	let mode = $state<'direct' | 'branch'>('direct');
	let newBranch = $state('');
	let submitting = $state(false);

	const dirty = $derived(content !== original);
	const fileName = $derived(path.split('/').pop() ?? path);

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
		unsupported = '';
		try {
			const resp = await rpcClient.gitFiles.getBlob(
				{ namespace, name, ref, path },
				silentCallOptions
			);
			headSha = resp.sha;
			const blob = resp.blob;
			if (!blob) {
				loadError = 'File not found';
			} else if (blob.isLfs) {
				unsupported = 'LFS pointers are edited locally with git-lfs.';
			} else if (blob.isBinary) {
				unsupported = 'Binary files cannot be edited in the browser.';
			} else if (blob.truncated) {
				unsupported = `This file is too large to edit here (${formatBytes(Number(blob.size))}).`;
			} else {
				original = new TextDecoder().decode(blob.content);
				content = original;
				executable = blob.mode === '100755';
			}
		} catch (err) {
			loadError =
				err instanceof ConnectError ? err.rawMessage || err.message : 'Failed to load file';
		} finally {
			loading = false;
		}
	}

	async function commit() {
		if (!dirty) {
			toast.error('No changes to commit');
			return;
		}
		submitting = true;
		try {
			const message = [summary.trim() || `Update ${fileName}`, description.trim()]
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
				operations: [
					{
						kind: FileOperationKind.UPDATE,
						path,
						content: new TextEncoder().encode(content),
						executable
					}
				]
			});
			toast.success('Changes committed');
			original = content;
			const landed = resp.branch || target || ref;
			if (target) {
				await goto(
					resolve(
						`${resolve('/[namespace]/[name]/pulls/new', { namespace, name })}?base=${encodeURIComponent(ref)}&head=${encodeURIComponent(landed)}` as `/${string}`
					)
				);
			} else {
				await goto(
					resolve('/[namespace]/[name]/blob/[ref]/[...path]', {
						namespace,
						name,
						ref: encodeURIComponent(landed),
						path
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
		void goto(
			resolve('/[namespace]/[name]/blob/[ref]/[...path]', {
				namespace,
				name,
				ref: encodeURIComponent(ref),
				path
			})
		);
	}
</script>

<svelte:head>
	<title>Edit {fileName} - {namespace}/{name}</title>
</svelte:head>

<div class="space-y-4">
	<div class="section-header">
		<div class="min-w-0">
			<h2 class="section-title truncate">Edit {fileName}</h2>
			<p class="section-subtitle truncate">
				<span class="font-mono">{path}</span> on <span class="font-mono">{ref}</span>{dirty
					? ', unsaved changes'
					: ''}
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
					'You need write access to edit files here. Fork the repository to propose changes.'}
			</AlertDescription>
		</Alert>
	{/if}

	{#if loading}
		<Skeleton class="h-96 w-full rounded-lg" />
	{:else if loadError}
		<Alert variant="destructive">
			<AlertTriangle />
			<AlertDescription>{loadError}</AlertDescription>
		</Alert>
	{:else if unsupported}
		<Alert>
			<AlertTriangle />
			<AlertDescription>{unsupported}</AlertDescription>
		</Alert>
		<Button variant="outline" onclick={cancel}>Back to file</Button>
	{:else}
		<FormCard title="Contents" description={fileName} icon={FileCode}>
			<CodeEditor bind:value={content} filename={path} readonly={!canWrite} />
		</FormCard>
		<CommitForm
			bind:summary
			bind:description
			bind:mode
			bind:newBranch
			branch={ref}
			placeholder="Update {fileName}"
			{submitting}
			disabled={!canWrite || !dirty}
			onSubmit={commit}
			onCancel={cancel}
		/>
	{/if}
</div>

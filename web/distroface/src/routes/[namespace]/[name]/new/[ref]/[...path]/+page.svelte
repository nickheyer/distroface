<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { toast } from 'svelte-sonner';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import FormCard from '$lib/components/form-card.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import CodeEditor from '$lib/components/git/code-editor.svelte';
	import CommitForm from '$lib/components/git/commit-form.svelte';
	import { FilePlus, Lock, ChevronLeft } from '@lucide/svelte';
	import { GitUnit, FileOperationKind } from '$lib/proto/distroface/v1/git_pb';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const ref = $derived(page.params.ref ?? '');
	const dir = $derived((page.params.path ?? '').replace(/^\/+|\/+$/g, ''));
	const treeHref = $derived(resolve('/[namespace]/[name]/tree/[ref]/[...path]', { namespace, name, ref: encodeURIComponent(ref), path: dir }));
	const canWrite = $derived(gitRepoStore.canWrite(GitUnit.CODE) && !gitRepoStore.repo?.isArchived);

	let fileName = $state('');
	let content = $state('');
	let headSha = $state('');
	let existing = $state<Set<string>>(new Set());
	let loadedKey = $state('');

	let summary = $state('');
	let description = $state('');
	let mode = $state<'direct' | 'branch'>('direct');
	let newBranch = $state('');
	let submitting = $state(false);

	const cleanName = $derived(fileName.trim().replace(/^\/+/, '').replace(/\/{2,}/g, '/'));
	const fullPath = $derived(dir ? `${dir}/${cleanName}` : cleanName);
	const nameValid = $derived(
		cleanName !== '' && !cleanName.endsWith('/') && !cleanName.split('/').some((p) => p === '' || p === '.' || p === '..')
	);
	const collides = $derived(existing.has(cleanName.split('/')[0] ?? ''));
	const baseName = $derived(cleanName.split('/').pop() ?? '');
	const nameError = $derived(
		collides ? `Something named ${cleanName.split('/')[0]} already exists here` : fileName !== '' && !nameValid ? 'Invalid file name' : ''
	);

	$effect(() => {
		void gitRepoStore.load(namespace, name);
	});

	// Head sha and sibling names come from the target directory
	$effect(() => {
		const key = `${namespace}/${name}@${ref}:${dir}`;
		if (key !== loadedKey) {
			loadedKey = key;
			void loadTree();
		}
	});

	async function loadTree() {
		try {
			const resp = await rpcClient.gitFiles.getTree({ namespace, name, ref, path: dir }, silentCallOptions);
			headSha = resp.sha;
			existing = new Set(resp.entries.map((e) => e.name));
		} catch {
			headSha = '';
			existing = new Set();
		}
	}

	async function commit() {
		if (!nameValid || collides) return;
		submitting = true;
		try {
			const message = [summary.trim() || `Create ${baseName}`, description.trim()].filter(Boolean).join('\n\n');
			const target = mode === 'branch' ? newBranch.trim() : '';
			const resp = await rpcClient.gitFiles.updateFiles({
				namespace, name,
				branch: ref,
				newBranch: target,
				message,
				expectedHeadSha: headSha,
				operations: [{
					kind: FileOperationKind.CREATE,
					path: fullPath,
					content: new TextEncoder().encode(content)
				}]
			});
			toast.success('File created');
			const landed = resp.branch || target || ref;
			if (target) {
				await goto(resolve(`${resolve('/[namespace]/[name]/pulls/new', { namespace, name })}?base=${encodeURIComponent(ref)}&head=${encodeURIComponent(landed)}` as `/${string}`));
			} else {
				await goto(resolve('/[namespace]/[name]/blob/[ref]/[...path]', { namespace, name, ref: encodeURIComponent(landed), path: fullPath }));
			}
		} catch {
			// Error interceptor already toasted
		} finally {
			submitting = false;
		}
	}

	function cancel() {
		void goto(resolve('/[namespace]/[name]/tree/[ref]/[...path]', { namespace, name, ref: encodeURIComponent(ref), path: dir }));
	}
</script>

<svelte:head>
	<title>New file - {namespace}/{name}</title>
</svelte:head>

<div class="space-y-4">
	<div class="section-header">
		<div class="min-w-0">
			<h2 class="section-title">New file</h2>
			<p class="section-subtitle truncate">
				In <span class="font-mono">{dir ? `${name}/${dir}` : name}</span> on <span class="font-mono">{ref}</span>
			</p>
		</div>
		<Button variant="outline" size="sm" class="h-8 shrink-0" href={treeHref}>
			<ChevronLeft class="h-3.5 w-3.5 mr-1" />Files
		</Button>
	</div>

	{#if gitRepoStore.repo && !canWrite}
		<Alert>
			<Lock class="h-4 w-4" />
			<AlertDescription>
				{gitRepoStore.repo.isArchived ? 'This repository is archived and read only.' : 'You need write access to add files here.'}
			</AlertDescription>
		</Alert>
	{/if}

	<FormCard title="File" description="Name the file, then write its contents" icon={FilePlus}>
		<div class="space-y-3">
			<FormField label="File name" id="new-file-name" required help="Slashes create folders" error={nameError}>
				<div class="flex items-center gap-2 min-w-0">
					{#if dir}
						<span class="font-mono text-sm text-muted-foreground shrink-0 truncate max-w-[40%]">{dir}/</span>
					{/if}
					<Input
						id="new-file-name"
						bind:value={fileName}
						placeholder="README.md"
						class="font-mono text-sm"
						aria-invalid={!!nameError}
						autocomplete="off"
						spellcheck={false}
					/>
				</div>
			</FormField>
			<FormField label="Contents" id="new-file-content" bordered={false}>
				<CodeEditor bind:value={content} filename={cleanName} readonly={!canWrite} placeholder="Start typing..." />
			</FormField>
		</div>
	</FormCard>

	<CommitForm
		bind:summary
		bind:description
		bind:mode
		bind:newBranch
		branch={ref}
		placeholder={baseName ? `Create ${baseName}` : 'Create new file'}
		submitLabel="Commit New File"
		{submitting}
		disabled={!canWrite || !nameValid || collides}
		onSubmit={commit}
		onCancel={cancel}
	/>
</div>

<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { toast } from 'svelte-sonner';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import FormCard from '$lib/components/form-card.svelte';
	import CommitForm from '$lib/components/git/commit-form.svelte';
	import { Upload, Lock, X, File as FileIcon, RefreshCw, ChevronLeft } from '$lib/icons';
	import { formatBytes } from '$lib/utils';
	import { GitUnit, FileOperationKind } from '$lib/proto/distroface/v1/git_pb';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const ref = $derived(page.params.ref ?? '');
	const dir = $derived((page.params.path ?? '').replace(/^\/+|\/+$/g, ''));
	const treeHref = $derived(
		resolve('/[namespace]/[name]/tree/[ref]/[...path]', {
			namespace,
			name,
			ref: encodeURIComponent(ref),
			path: dir
		})
	);
	const canWrite = $derived(gitRepoStore.canWrite(GitUnit.CODE) && !gitRepoStore.readOnly);

	type Picked = { key: string; relPath: string; file: File };

	let picked = $state<Picked[]>([]);
	let dragging = $state(false);
	let fileInput = $state<HTMLInputElement | null>(null);
	let headSha = $state('');
	let existing = $state<Set<string>>(new Set());
	let loadedKey = $state('');

	let summary = $state('');
	let description = $state('');
	let mode = $state<'direct' | 'branch'>('direct');
	let newBranch = $state('');
	let submitting = $state(false);

	const totalBytes = $derived(picked.reduce((n, p) => n + p.file.size, 0));

	$effect(() => {
		void gitRepoStore.load(namespace, name);
	});

	$effect(() => {
		const repo = gitRepoStore.repo;
		if (!repo || repo.namespace !== namespace || repo.name !== name) return;
		const key = `${repo.id}@${ref}:${dir}:${repo.isEmpty}`;
		if (key === loadedKey) return;
		loadedKey = key;
		if (repo.isEmpty) {
			headSha = '';
			existing = new Set();
			return;
		}
		void loadTree();
	});

	async function loadTree() {
		try {
			const resp = await rpcClient.gitFiles.getTree(
				{ namespace, name, ref, path: dir },
				silentCallOptions
			);
			headSha = resp.sha;
			existing = new Set(resp.entries.filter((e) => e.type === 'blob').map((e) => e.name));
			return;
		} catch {
			existing = new Set();
		}
		// A directory that does not exist yet still needs the branch head
		try {
			const root = dir
				? await rpcClient.gitFiles.getTree({ namespace, name, ref }, silentCallOptions)
				: undefined;
			headSha = root?.sha ?? '';
		} catch {
			headSha = '';
		}
	}

	// Folder drops keep their relative path
	function addFiles(files: FileList | File[]) {
		const next = [...picked];
		for (const file of Array.from(files)) {
			const rel = (file.webkitRelativePath || file.name).replace(/^\/+/, '');
			if (!rel || rel.endsWith('/')) continue;
			const idx = next.findIndex((p) => p.relPath === rel);
			const entry = { key: `${rel}:${file.size}:${file.lastModified}`, relPath: rel, file };
			if (idx >= 0) next[idx] = entry;
			else next.push(entry);
		}
		picked = next;
	}

	function onDrop(e: DragEvent) {
		e.preventDefault();
		dragging = false;
		if (e.dataTransfer?.files?.length) addFiles(e.dataTransfer.files);
	}

	function onPick(e: Event) {
		const input = e.currentTarget as HTMLInputElement;
		if (input.files?.length) addFiles(input.files);
		input.value = '';
	}

	const replaces = (p: Picked) => !p.relPath.includes('/') && existing.has(p.relPath);

	async function commit() {
		if (picked.length === 0) return;
		submitting = true;
		try {
			const operations = await Promise.all(
				picked.map(async (p) => ({
					kind: replaces(p) ? FileOperationKind.UPDATE : FileOperationKind.CREATE,
					path: dir ? `${dir}/${p.relPath}` : p.relPath,
					content: new Uint8Array(await p.file.arrayBuffer())
				}))
			);
			const fallback =
				picked.length === 1 ? `Add ${picked[0].relPath}` : `Add ${picked.length} files via upload`;
			const message = [summary.trim() || fallback, description.trim()].filter(Boolean).join('\n\n');
			const target = mode === 'branch' ? newBranch.trim() : '';
			const resp = await rpcClient.gitFiles.updateFiles({
				namespace,
				name,
				branch: ref,
				newBranch: target,
				message,
				expectedHeadSha: headSha,
				operations
			});
			toast.success(`${picked.length} file${picked.length === 1 ? '' : 's'} committed`);
			await gitRepoStore.reload();
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
						path: dir
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
			resolve('/[namespace]/[name]/tree/[ref]/[...path]', {
				namespace,
				name,
				ref: encodeURIComponent(ref),
				path: dir
			})
		);
	}
</script>

<svelte:head>
	<title>Upload files - {namespace}/{name}</title>
</svelte:head>

<div class="space-y-4">
	<div class="section-header">
		<div class="min-w-0">
			<h2 class="section-title">Upload files</h2>
			<p class="section-subtitle truncate">
				Into <span class="font-mono">{dir ? `${name}/${dir}` : name}</span> on
				<span class="font-mono">{ref}</span>
			</p>
		</div>
		<Button variant="outline" class="shrink-0" href={treeHref}>
			<ChevronLeft />Files
		</Button>
	</div>

	{#if gitRepoStore.repo && !canWrite}
		<Alert>
			<Lock />
			<AlertDescription>
				{gitRepoStore.readOnlyReason || 'You need write access to upload files here.'}
			</AlertDescription>
		</Alert>
	{/if}

	<FormCard
		title="Files"
		description="Existing files with the same name are replaced"
		icon={Upload}
	>
		<div class="space-y-4">
			<div
				role="button"
				tabindex="0"
				aria-label="Drop files or click to choose"
				class="flex cursor-pointer flex-col items-center justify-center gap-1 rounded-lg border border-dashed px-6 py-10 text-center transition-colors
					{dragging ? 'border-primary bg-primary/5' : 'hover:border-border-strong hover:bg-muted/30'}"
				ondragover={(e) => {
					e.preventDefault();
					dragging = true;
				}}
				ondragleave={() => (dragging = false)}
				ondrop={onDrop}
				onclick={() => fileInput?.click()}
				onkeydown={(e) => {
					if (e.key === 'Enter' || e.key === ' ') {
						e.preventDefault();
						fileInput?.click();
					}
				}}
			>
				<Upload class="mb-2 size-6 text-muted-foreground/70" />
				<p class="text-sm font-medium">Drag files here or click to choose</p>
				<p class="text-xs text-muted-foreground">
					Files land in {dir ? `${dir}/` : 'the repository root'}
				</p>
				<input bind:this={fileInput} type="file" multiple class="hidden" onchange={onPick} />
			</div>

			{#if picked.length > 0}
				<div class="box">
					<div class="box-header">
						<span>{picked.length} file{picked.length === 1 ? '' : 's'}</span>
						<span class="text-xs font-normal text-muted-foreground tabular-nums"
							>{formatBytes(totalBytes)}</span
						>
					</div>
					<ul class="max-h-80 divide-y overflow-y-auto">
						{#each picked as p (p.key)}
							<li class="flex min-w-0 items-center gap-3 px-4 py-2.5">
								<FileIcon class="size-4 shrink-0 text-muted-foreground" />
								<span class="min-w-0 flex-1 truncate font-mono text-sm">{p.relPath}</span>
								{#if replaces(p)}
									<Badge variant="warning" class="shrink-0">
										<RefreshCw />Replaces
									</Badge>
								{/if}
								<span class="hidden shrink-0 text-xs text-muted-foreground tabular-nums sm:inline"
									>{formatBytes(p.file.size)}</span
								>
								<Button
									variant="ghost-muted"
									size="icon-sm"
									class="shrink-0"
									aria-label="Remove {p.relPath}"
									onclick={() => (picked = picked.filter((x) => x.key !== p.key))}
								>
									<X />
								</Button>
							</li>
						{/each}
					</ul>
				</div>
			{/if}
		</div>
	</FormCard>

	<CommitForm
		bind:summary
		bind:description
		bind:mode
		bind:newBranch
		branch={ref}
		placeholder={picked.length === 1 ? `Add ${picked[0].relPath}` : 'Add files via upload'}
		submitLabel="Commit changes"
		{submitting}
		disabled={!canWrite || picked.length === 0}
		onSubmit={commit}
		onCancel={cancel}
	/>
</div>

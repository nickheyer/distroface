<script lang="ts">
	import { Upload, Check, X, Loader2, AlertTriangle } from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Progress } from '$lib/components/ui/progress';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { ConnectError } from '@connectrpc/connect';
	import type { ReleaseAsset } from '$lib/proto/distroface/v1/release_pb';
	import { formatBytes } from '$lib/utils';

	let {
		namespace,
		name,
		tag,
		disabled = false,
		onUploaded
	}: {
		namespace: string;
		name: string;
		tag: string;
		disabled?: boolean;
		onUploaded?: (asset: ReleaseAsset) => void;
	} = $props();

	const SESSION_KEY = 'distroface_session';

	type Stage = 'queued' | 'uploading' | 'finalizing' | 'done' | 'error';
	interface Item {
		id: number;
		file: File;
		progress: number;
		stage: Stage;
		error: string;
	}

	let items = $state<Item[]>([]);
	let dragging = $state(false);
	let input = $state<HTMLInputElement | null>(null);
	let running = false;
	let seq = 0;

	function enqueue(files: FileList | File[]) {
		if (disabled) return;
		for (const file of Array.from(files)) {
			items.push({ id: ++seq, file, progress: 0, stage: 'queued', error: '' });
		}
		void drain();
	}

	function onPick(e: Event) {
		const el = e.target as HTMLInputElement;
		if (el.files?.length) enqueue(el.files);
		el.value = '';
	}

	function onDrop(e: DragEvent) {
		e.preventDefault();
		dragging = false;
		if (e.dataTransfer?.files.length) enqueue(e.dataTransfer.files);
	}

	function onDragOver(e: DragEvent) {
		e.preventDefault();
		if (!disabled) dragging = true;
	}

	function openPicker() {
		if (!disabled) input?.click();
	}

	function patch(url: string, file: File, onProgress: (pct: number) => void): Promise<void> {
		return new Promise((resolve, reject) => {
			const xhr = new XMLHttpRequest();
			xhr.open('PATCH', url);
			const token = localStorage.getItem(SESSION_KEY);
			if (token) xhr.setRequestHeader('Authorization', `Bearer ${token}`);
			xhr.upload.onprogress = (ev) => {
				if (ev.lengthComputable) onProgress(Math.round((ev.loaded / ev.total) * 100));
			};
			xhr.onload = () => {
				if (xhr.status >= 200 && xhr.status < 300) resolve();
				else reject(new Error(`Upload failed (${xhr.status})`));
			};
			xhr.onerror = () => reject(new Error('Upload failed'));
			xhr.send(file);
		});
	}

	// Initiate, PATCH the bytes, complete, then attach
	async function upload(item: Item) {
		item.stage = 'uploading';
		try {
			const init = await rpcClient.artifact.initiateArtifactUpload({ repoName: name, namespace }, silentCallOptions);
			if (!init.uploadUrl) throw new Error('Server did not return an upload location');
			await patch(init.uploadUrl, item.file, (pct) => (item.progress = pct));
			item.stage = 'finalizing';
			const done = await rpcClient.artifact.completeArtifactUpload(
				{
					repoName: name,
					namespace,
					uploadId: init.uploadId,
					version: tag,
					path: item.file.name,
					properties: { release: 'true' }
				},
				silentCallOptions
			);
			const artifactId = done.artifact?.id ?? '';
			const attached = await rpcClient.release.attachReleaseAsset({ namespace, name, tag, artifactId }, silentCallOptions);
			item.stage = 'done';
			item.progress = 100;
			if (attached.asset) onUploaded?.(attached.asset);
		} catch (err) {
			item.stage = 'error';
			item.error = err instanceof ConnectError ? err.rawMessage || err.message : err instanceof Error ? err.message : 'Upload failed';
		}
	}

	async function drain() {
		if (running) return;
		running = true;
		try {
			for (;;) {
				const next = items.find((i) => i.stage === 'queued');
				if (!next) break;
				await upload(next);
			}
		} finally {
			running = false;
		}
	}

	function remove(id: number) {
		items = items.filter((i) => i.id !== id);
	}

	function retry(item: Item) {
		item.stage = 'queued';
		item.progress = 0;
		item.error = '';
		void drain();
	}
</script>

<div class="space-y-3">
	<div
		role="button"
		tabindex={disabled ? -1 : 0}
		aria-disabled={disabled}
		class="rounded-xl border-2 border-dashed px-5 py-6 text-center transition-colors {dragging ? 'border-primary bg-primary/5' : 'border-border/60 hover:border-primary/40'} {disabled ? 'opacity-60' : 'cursor-pointer'}"
		ondragover={onDragOver}
		ondragleave={() => (dragging = false)}
		ondrop={onDrop}
		onclick={openPicker}
		onkeydown={(e) => {
			if (e.key === 'Enter' || e.key === ' ') {
				e.preventDefault();
				openPicker();
			}
		}}
	>
		<Upload class="h-6 w-6 mx-auto text-muted-foreground/60" />
		<p class="text-[13px] font-medium mt-2">Drop files here or click to choose</p>
		<p class="text-[12px] text-muted-foreground mt-0.5">
			Stored in the <span class="font-mono">{namespace}/{name}</span> package at version <span class="font-mono">{tag}</span>
		</p>
		<input bind:this={input} type="file" multiple class="hidden" onchange={onPick} {disabled} />
	</div>

	{#if items.length > 0}
		<ul class="rounded-xl border border-border/60 divide-y divide-border/40 overflow-hidden">
			{#each items as item (item.id)}
				<li class="px-4 py-2.5 space-y-1.5">
					<div class="flex items-center gap-2 text-[13px] min-w-0">
						{#if item.stage === 'done'}
							<Check class="h-3.5 w-3.5 text-success shrink-0" />
						{:else if item.stage === 'error'}
							<AlertTriangle class="h-3.5 w-3.5 text-destructive shrink-0" />
						{:else}
							<Loader2 class="h-3.5 w-3.5 animate-spin text-muted-foreground shrink-0" />
						{/if}
						<span class="font-medium truncate min-w-0">{item.file.name}</span>
						<span class="text-[12px] text-muted-foreground tabular-nums shrink-0">{formatBytes(item.file.size)}</span>
						<span class="ml-auto text-[12px] text-muted-foreground shrink-0 tabular-nums">
							{#if item.stage === 'uploading'}
								{item.progress}%
							{:else if item.stage === 'finalizing'}
								Finalizing...
							{:else if item.stage === 'queued'}
								Queued
							{:else if item.stage === 'done'}
								Attached
							{/if}
						</span>
						{#if item.stage === 'error'}
							<Button variant="ghost" size="sm" class="h-6 px-2 text-[12px]" onclick={() => retry(item)}>Retry</Button>
						{/if}
						{#if item.stage === 'done' || item.stage === 'error'}
							<button type="button" class="text-muted-foreground hover:text-foreground" onclick={() => remove(item.id)} aria-label="Dismiss">
								<X class="h-3.5 w-3.5" />
							</button>
						{/if}
					</div>
					{#if item.stage === 'uploading' || item.stage === 'finalizing'}
						<Progress value={item.progress} class="h-1.5" />
					{/if}
					{#if item.error}
						<p class="text-[12px] text-destructive">{item.error}</p>
					{/if}
				</li>
			{/each}
		</ul>
	{/if}
</div>

<script lang="ts">
	import { Upload, Check, X, Loader, AlertTriangle } from '$lib/icons';
	import { Button } from '$lib/components/ui/button';
	import { Progress } from '$lib/components/ui/progress';
	import { fromJson } from '@bufbuild/protobuf';
	import { ReleaseAssetSchema, type ReleaseAsset } from '$lib/proto/distroface/v1/release_pb';
	import { assetUploadUrl } from '$lib/releases';
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

	type Stage = 'queued' | 'uploading' | 'done' | 'error';
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

	// One streamed POST per file, the response is the asset
	function post(file: File, onProgress: (pct: number) => void): Promise<ReleaseAsset> {
		return new Promise((resolve, reject) => {
			const xhr = new XMLHttpRequest();
			xhr.open('POST', assetUploadUrl(namespace, name, tag, file.name));
			const token = localStorage.getItem(SESSION_KEY);
			if (token) xhr.setRequestHeader('Authorization', `Bearer ${token}`);
			xhr.setRequestHeader('Content-Type', file.type || 'application/octet-stream');
			xhr.upload.onprogress = (ev) => {
				if (ev.lengthComputable) onProgress(Math.round((ev.loaded / ev.total) * 100));
			};
			xhr.onload = () => {
				if (xhr.status >= 200 && xhr.status < 300) {
					try {
						resolve(fromJson(ReleaseAssetSchema, JSON.parse(xhr.responseText)));
					} catch (err) {
						reject(err instanceof Error ? err : new Error('Unreadable server response'));
					}
				} else {
					reject(new Error(xhr.responseText.trim() || `Upload failed (${xhr.status})`));
				}
			};
			xhr.onerror = () => reject(new Error('Upload failed'));
			xhr.send(file);
		});
	}

	async function upload(item: Item) {
		item.stage = 'uploading';
		try {
			const asset = await post(item.file, (pct) => (item.progress = pct));
			item.stage = 'done';
			item.progress = 100;
			onUploaded?.(asset);
		} catch (err) {
			item.stage = 'error';
			item.error = err instanceof Error ? err.message : 'Upload failed';
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
		class="rounded-lg border border-dashed px-5 py-6 text-center transition-colors {dragging
			? 'border-primary bg-primary/5'
			: 'hover:border-border-strong'} {disabled ? 'opacity-60' : 'cursor-pointer'}"
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
		<Upload class="mx-auto size-6 text-muted-foreground/70" />
		<p class="mt-2 text-sm font-medium">Drop files here or click to choose</p>
		<p class="mt-0.5 text-xs text-muted-foreground">
			Attached to release <span class="font-mono">{tag}</span> of
			<span class="font-mono">{namespace}/{name}</span>
		</p>
		<input bind:this={input} type="file" multiple class="hidden" onchange={onPick} {disabled} />
	</div>

	{#if items.length > 0}
		<ul class="box divide-y">
			{#each items as item (item.id)}
				<li class="space-y-1.5 px-4 py-2.5">
					<div class="flex min-w-0 items-center gap-2 text-sm">
						{#if item.stage === 'done'}
							<Check class="size-3.5 shrink-0 text-success" />
						{:else if item.stage === 'error'}
							<AlertTriangle class="size-3.5 shrink-0 text-destructive" />
						{:else}
							<Loader class="size-3.5 shrink-0 animate-spin text-muted-foreground" />
						{/if}
						<span class="min-w-0 truncate font-medium">{item.file.name}</span>
						<span class="shrink-0 text-xs text-muted-foreground tabular-nums"
							>{formatBytes(item.file.size)}</span
						>
						<span class="ml-auto shrink-0 text-xs text-muted-foreground tabular-nums">
							{#if item.stage === 'uploading'}
								{item.progress}%
							{:else if item.stage === 'queued'}
								Queued
							{:else if item.stage === 'done'}
								Attached
							{/if}
						</span>
						{#if item.stage === 'error'}
							<Button variant="ghost" size="xs" onclick={() => retry(item)}>Retry</Button>
						{/if}
						{#if item.stage === 'done' || item.stage === 'error'}
							<Button
								variant="ghost-muted"
								size="icon-xs"
								onclick={() => remove(item.id)}
								aria-label="Dismiss"
							>
								<X />
							</Button>
						{/if}
					</div>
					{#if item.stage === 'uploading'}
						<Progress value={item.progress} />
					{/if}
					{#if item.error}
						<p class="text-xs text-destructive">{item.error}</p>
					{/if}
				</li>
			{/each}
		</ul>
	{/if}
</div>

<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import {
		FileText,
		Code,
		Eye,
		Download,
		ExternalLink,
		Pencil,
		Trash2,
		History,
		WrapText,
		Database,
		AlertTriangle
	} from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Alert, AlertDescription, AlertTitle } from '$lib/components/ui/alert';
	import CopyButton from '$lib/components/copy-button.svelte';
	import Markdown from './markdown.svelte';
	import { highlightLines, languageFromPath, escapeHtml } from '$lib/highlight';
	import { formatBytes } from '$lib/utils';
	import type { GitBlob } from '$lib/proto/distroface/v1/git_pb';

	let {
		blob,
		namespace,
		name,
		ref,
		canEdit = false
	}: {
		blob: GitBlob;
		namespace: string;
		name: string;
		ref: string;
		canEdit?: boolean;
	} = $props();

	const encodedRef = $derived(encodeURIComponent(ref));
	const rawUrl = $derived(blob.rawUrl || `/${namespace}/${name}/-/raw/${encodedRef}/${blob.path}`);
	const editHref = $derived(`/${namespace}/${name}/edit/${encodedRef}/${blob.path}`);
	const deleteHref = $derived(`/${namespace}/${name}/delete/${encodedRef}/${blob.path}`);
	const blameHref = $derived(`/${namespace}/${name}/blame/${encodedRef}/${blob.path}`);
	const historyHref = $derived(
		`/${namespace}/${name}/commits/${encodedRef}?path=${encodeURIComponent(blob.path)}`
	);
	const dirPath = $derived(
		blob.path.includes('/') ? blob.path.slice(0, blob.path.lastIndexOf('/')) : ''
	);
	const baseHref = $derived(
		`/${namespace}/${name}/blob/${encodedRef}/${dirPath ? `${dirPath}/` : ''}`
	);
	const rawBase = $derived(
		`/${namespace}/${name}/-/raw/${encodedRef}/${dirPath ? `${dirPath}/` : ''}`
	);

	const isImage = $derived(blob.contentType.startsWith('image/'));
	const lang = $derived(languageFromPath(blob.path) || blob.language.toLowerCase());
	const isMarkdown = $derived(lang === 'markdown' || lang === 'mdx');
	const text = $derived(
		blob.isBinary || blob.truncated ? '' : new TextDecoder().decode(blob.content)
	);
	const lines = $derived.by(() => {
		if (!text) return [] as string[];
		const body = text.endsWith('\n') ? text.slice(0, -1) : text;
		return body.split('\n');
	});

	let mode = $state<'preview' | 'code'>('preview');
	let wrap = $state(false);
	let html = $state<string[] | null>(null);
	let seq = 0;

	$effect(() => {
		const src = text;
		const l = lang;
		const mine = ++seq;
		html = null;
		if (!src || lines.length > 20000) return;
		highlightLines(src, l).then((out) => {
			if (mine === seq) html = out.length === lines.length ? out : null;
		});
	});

	// Hash of the form L10 or L10-L20 marks rows
	const range = $derived.by(() => {
		const m = /^#L(\d+)(?:-L?(\d+))?$/.exec(page.url.hash);
		if (!m) return null;
		const a = Number(m[1]);
		const b = m[2] ? Number(m[2]) : a;
		return { start: Math.min(a, b), end: Math.max(a, b) };
	});

	function inRange(n: number): boolean {
		return !!range && n >= range.start && n <= range.end;
	}

	function lineClick(e: MouseEvent, n: number) {
		if (!e.shiftKey || !range) return;
		e.preventDefault();
		const start = Math.min(range.start, n);
		const end = Math.max(range.end, n);
		const blobHref = resolve('/[namespace]/[name]/blob/[ref]/[...path]', {
			namespace,
			name,
			ref: encodedRef,
			path: blob.path
		});
		goto(resolve(`${blobHref}#L${start}-L${end}` as `/${string}`), {
			replaceState: true,
			noScroll: true,
			keepFocus: true
		});
	}

	$effect(() => {
		if (!range || !lines.length) return;
		const el = document.getElementById(`L${range.start}`);
		el?.scrollIntoView({ block: 'center' });
	});
</script>

<div class="box">
	<div class="flex flex-wrap items-center gap-2 border-b bg-muted/40 px-3 py-2">
		<div class="flex min-w-0 flex-1 items-center gap-2">
			{#if isMarkdown && !blob.isBinary}
				<div class="inline-flex overflow-hidden rounded-md border">
					<button
						type="button"
						class="inline-flex h-7 items-center gap-1 px-2 text-xs transition-colors {mode ===
						'preview'
							? 'bg-muted text-foreground'
							: 'text-muted-foreground hover:text-foreground'}"
						onclick={() => (mode = 'preview')}
						aria-pressed={mode === 'preview'}
					>
						<Eye class="size-3" />Preview
					</button>
					<button
						type="button"
						class="inline-flex h-7 items-center gap-1 border-l px-2 text-xs transition-colors {mode ===
						'code'
							? 'bg-muted text-foreground'
							: 'text-muted-foreground hover:text-foreground'}"
						onclick={() => (mode = 'code')}
						aria-pressed={mode === 'code'}
					>
						<Code class="size-3" />Code
					</button>
				</div>
			{:else}
				<FileText class="size-4 shrink-0 text-muted-foreground" />
			{/if}
			<span class="text-xs whitespace-nowrap text-muted-foreground tabular-nums">
				{#if lines.length > 0}{lines.length} line{lines.length !== 1 ? 's' : ''}
					<span class="text-muted-foreground/50">|</span>{/if}
				{formatBytes(Number(blob.size))}
				{#if blob.isLfs}<span class="text-muted-foreground/50">|</span> LFS{/if}
				{#if blob.language}<span class="text-muted-foreground/50">|</span> {blob.language}{/if}
			</span>
		</div>
		<div class="flex flex-wrap items-center gap-1">
			{#if text}
				<Button
					variant="ghost-muted"
					size="icon-sm"
					class={wrap ? 'bg-muted text-foreground' : ''}
					onclick={() => (wrap = !wrap)}
					title="Toggle line wrap"
					aria-label="Toggle line wrap"
					aria-pressed={wrap}
				>
					<WrapText />
				</Button>
				<CopyButton {text} label="File contents copied" />
			{/if}
			<Button variant="ghost-muted" size="sm" href={rawUrl} target="_blank" rel="noopener">
				<ExternalLink />Raw
			</Button>
			<Button
				variant="ghost-muted"
				size="sm"
				href={blob.isLfs && blob.lfs?.downloadUrl ? blob.lfs.downloadUrl : rawUrl}
				download={blob.name}
			>
				<Download />Download
			</Button>
			{#if !blob.isBinary}
				<Button variant="ghost-muted" size="sm" href={blameHref}>Blame</Button>
			{/if}
			<Button variant="ghost-muted" size="sm" href={historyHref}>
				<History />History
			</Button>
			{#if canEdit}
				{#if !blob.isBinary && !blob.isLfs}
					<Button variant="ghost-muted" size="sm" href={editHref}>
						<Pencil />Edit
					</Button>
				{/if}
				<Button
					variant="ghost"
					size="sm"
					class="text-destructive hover:text-destructive"
					href={deleteHref}
				>
					<Trash2 />Delete
				</Button>
			{/if}
		</div>
	</div>

	{#if blob.isLfs}
		<div class="border-b p-3">
			<Alert variant="info">
				<Database />
				<AlertTitle>Stored with Git LFS</AlertTitle>
				<AlertDescription>
					<p class="max-w-full truncate font-mono text-xs">oid {blob.lfs?.oid ?? ''}</p>
					<p>
						{formatBytes(Number(blob.lfs?.size ?? blob.size))}
						{#if blob.lfs?.present === false}
							<span class="text-warning">, object not uploaded to this server</span>
						{/if}
					</p>
				</AlertDescription>
			</Alert>
		</div>
	{/if}

	{#if blob.truncated}
		<div class="space-y-2 px-4 py-10 text-center">
			<AlertTriangle class="mx-auto size-6 text-warning" />
			<p class="text-sm font-medium">File too large to display inline</p>
			<p class="text-sm text-muted-foreground">
				{formatBytes(Number(blob.size))}, open the raw file instead.
			</p>
			<Button variant="outline" size="sm" href={rawUrl} target="_blank" rel="noopener"
				>View raw</Button
			>
		</div>
	{:else if blob.isLfs && blob.lfs?.present === false}
		<div class="px-4 py-8 text-center text-sm text-muted-foreground">
			Only the pointer file is available.
		</div>
	{:else if isImage}
		<div
			class="flex items-center justify-center bg-[repeating-conic-gradient(var(--muted)_0%_25%,transparent_0%_50%)] bg-[length:16px_16px] p-6"
		>
			<img
				src={blob.isLfs && blob.lfs?.downloadUrl ? blob.lfs.downloadUrl : rawUrl}
				alt={blob.name}
				class="max-h-[70vh] max-w-full rounded-md"
			/>
		</div>
	{:else if blob.isBinary}
		<div class="space-y-2 px-4 py-10 text-center">
			<p class="text-sm font-medium">Binary file</p>
			<p class="text-sm text-muted-foreground">
				{formatBytes(Number(blob.size))}{#if blob.contentType}, {blob.contentType}{/if}
			</p>
		</div>
	{:else if isMarkdown && mode === 'preview'}
		<div class="px-6 py-5">
			<Markdown source={text} {baseHref} {rawBase} />
		</div>
	{:else if lines.length === 0}
		<div class="px-4 py-8 text-center text-sm text-muted-foreground">Empty file</div>
	{:else}
		<div class="overflow-x-auto">
			<table class="code-table">
				<tbody>
					{#each lines as line, i (i)}
						{@const n = i + 1}
						<tr id="L{n}" class={inRange(n) ? 'highlighted' : ''}>
							<td class="line-no">
								<a href="#L{n}" onclick={(e) => lineClick(e, n)}>{n}</a>
							</td>
							<td class="code-line {wrap ? 'wrap' : ''}">
								{#if html}
									<!-- eslint-disable-next-line svelte/no-at-html-tags -->
									{@html html[i]}
								{:else}
									<!-- eslint-disable-next-line svelte/no-at-html-tags -->
									{@html escapeHtml(line)}
								{/if}
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{/if}
</div>

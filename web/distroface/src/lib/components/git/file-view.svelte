<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import {
		FileText, Code, Eye, Download, ExternalLink, Pencil, Trash2, History, WrapText, Database, AlertTriangle
	} from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import FormPanel from '$lib/components/form-panel.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import CopyButton from '$lib/components/copy-button.svelte';
	import Markdown from './markdown.svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { highlightLines, languageFromPath, escapeHtml } from '$lib/highlight';
	import { formatBytes } from '$lib/utils';
	import { toast } from 'svelte-sonner';
	import { FileOperationKind } from '$lib/proto/distroface/v1/git_pb';
	import type { GitBlob } from '$lib/proto/distroface/v1/git_pb';

	let {
		blob,
		namespace,
		name,
		ref,
		canEdit = false,
		onDeleted
	}: {
		blob: GitBlob;
		namespace: string;
		name: string;
		ref: string;
		canEdit?: boolean;
		onDeleted?: (branch: string) => void;
	} = $props();

	const encodedRef = $derived(encodeURIComponent(ref));
	const rawUrl = $derived(blob.rawUrl || `/${namespace}/${name}/-/raw/${encodedRef}/${blob.path}`);
	const editHref = $derived(`/${namespace}/${name}/edit/${encodedRef}/${blob.path}`);
	const blameHref = $derived(`/${namespace}/${name}/blame/${encodedRef}/${blob.path}`);
	const historyHref = $derived(`/${namespace}/${name}/commits/${encodedRef}?path=${encodeURIComponent(blob.path)}`);
	const dirPath = $derived(blob.path.includes('/') ? blob.path.slice(0, blob.path.lastIndexOf('/')) : '');
	const baseHref = $derived(`/${namespace}/${name}/blob/${encodedRef}/${dirPath ? `${dirPath}/` : ''}`);
	const rawBase = $derived(`/${namespace}/${name}/-/raw/${encodedRef}/${dirPath ? `${dirPath}/` : ''}`);

	const isImage = $derived(blob.contentType.startsWith('image/'));
	const lang = $derived(languageFromPath(blob.path) || blob.language.toLowerCase());
	const isMarkdown = $derived(lang === 'markdown' || lang === 'mdx');
	const text = $derived(blob.isBinary || blob.truncated ? '' : new TextDecoder().decode(blob.content));
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
		const blobHref = resolve('/[namespace]/[name]/blob/[ref]/[...path]', { namespace, name, ref: encodedRef, path: blob.path });
		goto(resolve(`${blobHref}#L${start}-L${end}` as `/${string}`), { replaceState: true, noScroll: true, keepFocus: true });
	}

	$effect(() => {
		if (!range || !lines.length) return;
		const el = document.getElementById(`L${range.start}`);
		el?.scrollIntoView({ block: 'center' });
	});

	let deleteOpen = $state(false);
	let deleteMessage = $state('');
	let deleteBranch = $state('');
	let deleting = $state(false);

	function openDelete() {
		deleteMessage = `Delete ${blob.path}`;
		deleteBranch = '';
		deleteOpen = true;
	}

	async function confirmDelete() {
		if (!deleteMessage.trim()) return;
		deleting = true;
		try {
			const resp = await rpcClient.gitFiles.updateFiles({
				namespace,
				name,
				branch: ref,
				newBranch: deleteBranch.trim(),
				message: deleteMessage.trim(),
				operations: [{ kind: FileOperationKind.DELETE, path: blob.path }]
			});
			toast.success('File deleted');
			deleteOpen = false;
			onDeleted?.(resp.branch || deleteBranch.trim() || ref);
		} catch {
			// Error interceptor already toasted
		} finally {
			deleting = false;
		}
	}
</script>

<div class="rounded-xl border border-border/60 overflow-hidden bg-card">
	<div class="flex items-center gap-2 px-3 py-2 border-b border-border/50 bg-muted/20 flex-wrap">
		<div class="flex items-center gap-2 min-w-0 flex-1">
			{#if isMarkdown && !blob.isBinary}
				<div class="inline-flex rounded-md border border-border/60 overflow-hidden">
					<button type="button" class="px-2 h-7 text-[12px] inline-flex items-center gap-1 {mode === 'preview' ? 'bg-muted text-foreground' : 'text-muted-foreground hover:text-foreground'}" onclick={() => (mode = 'preview')}>
						<Eye class="h-3 w-3" />Preview
					</button>
					<button type="button" class="px-2 h-7 text-[12px] inline-flex items-center gap-1 border-l border-border/60 {mode === 'code' ? 'bg-muted text-foreground' : 'text-muted-foreground hover:text-foreground'}" onclick={() => (mode = 'code')}>
						<Code class="h-3 w-3" />Code
					</button>
				</div>
			{:else}
				<FileText class="h-4 w-4 text-muted-foreground/60 shrink-0" />
			{/if}
			<span class="text-[12.5px] text-muted-foreground tabular-nums whitespace-nowrap">
				{#if lines.length > 0}{lines.length} line{lines.length !== 1 ? 's' : ''} <span class="text-muted-foreground/40">|</span>{/if}
				{formatBytes(Number(blob.size))}
				{#if blob.isLfs}<span class="text-muted-foreground/40">|</span> LFS{/if}
				{#if blob.language}<span class="text-muted-foreground/40">|</span> {blob.language}{/if}
			</span>
		</div>
		<div class="flex items-center gap-1 flex-wrap">
			{#if text}
				<Button variant="ghost" size="sm" class="h-7 px-2 text-[12px] {wrap ? 'bg-muted' : ''}" onclick={() => (wrap = !wrap)} title="Toggle line wrap">
					<WrapText class="h-3.5 w-3.5" />
				</Button>
				<CopyButton text={text} label="File contents copied!" />
			{/if}
			<Button variant="ghost" size="sm" class="h-7 px-2 text-[12px]" href={rawUrl} target="_blank" rel="noopener">
				<ExternalLink class="h-3.5 w-3.5 mr-1" />Raw
			</Button>
			<Button variant="ghost" size="sm" class="h-7 px-2 text-[12px]" href={blob.isLfs && blob.lfs?.downloadUrl ? blob.lfs.downloadUrl : rawUrl} download={blob.name}>
				<Download class="h-3.5 w-3.5 mr-1" />Download
			</Button>
			{#if !blob.isBinary}
				<Button variant="ghost" size="sm" class="h-7 px-2 text-[12px]" href={blameHref}>Blame</Button>
			{/if}
			<Button variant="ghost" size="sm" class="h-7 px-2 text-[12px]" href={historyHref}>
				<History class="h-3.5 w-3.5 mr-1" />History
			</Button>
			{#if canEdit}
				{#if !blob.isBinary && !blob.isLfs}
					<Button variant="ghost" size="sm" class="h-7 px-2 text-[12px]" href={editHref}>
						<Pencil class="h-3.5 w-3.5 mr-1" />Edit
					</Button>
				{/if}
				<Button variant="ghost" size="sm" class="h-7 px-2 text-[12px] text-destructive hover:text-destructive" onclick={openDelete}>
					<Trash2 class="h-3.5 w-3.5 mr-1" />Delete
				</Button>
			{/if}
		</div>
	</div>

	{#if blob.isLfs}
		<div class="px-4 py-3 border-b border-border/50 bg-sky-500/5 text-[13px] flex items-start gap-2.5">
			<Database class="h-4 w-4 text-sky-600 dark:text-sky-400 shrink-0 mt-0.5" />
			<div class="space-y-0.5 min-w-0">
				<p class="font-medium">Stored with Git LFS</p>
				<p class="text-muted-foreground font-mono text-[12px] truncate">oid {blob.lfs?.oid ?? ''}</p>
				<p class="text-muted-foreground">
					{formatBytes(Number(blob.lfs?.size ?? blob.size))}
					{#if blob.lfs?.present === false}
						<span class="text-amber-600 dark:text-amber-400">, object not uploaded to this server</span>
					{/if}
				</p>
			</div>
		</div>
	{/if}

	{#if blob.truncated}
		<div class="px-4 py-10 text-center space-y-2">
			<AlertTriangle class="h-6 w-6 mx-auto text-amber-500" />
			<p class="text-sm font-medium">File too large to display inline</p>
			<p class="text-[13px] text-muted-foreground">{formatBytes(Number(blob.size))}, open the raw file instead.</p>
			<Button variant="outline" size="sm" href={rawUrl} target="_blank" rel="noopener">View raw</Button>
		</div>
	{:else if blob.isLfs && blob.lfs?.present === false}
		<div class="px-4 py-8 text-center text-[13px] text-muted-foreground">Only the pointer file is available.</div>
	{:else if isImage}
		<div class="p-6 flex items-center justify-center bg-[repeating-conic-gradient(var(--muted)_0%_25%,transparent_0%_50%)] bg-[length:16px_16px]">
			<img src={blob.isLfs && blob.lfs?.downloadUrl ? blob.lfs.downloadUrl : rawUrl} alt={blob.name} class="max-w-full max-h-[70vh] rounded-md shadow-sm" />
		</div>
	{:else if blob.isBinary}
		<div class="px-4 py-10 text-center space-y-2">
			<p class="text-sm font-medium">Binary file</p>
			<p class="text-[13px] text-muted-foreground">{formatBytes(Number(blob.size))}{#if blob.contentType}, {blob.contentType}{/if}</p>
		</div>
	{:else if isMarkdown && mode === 'preview'}
		<div class="px-6 py-5">
			<Markdown source={text} {baseHref} {rawBase} />
		</div>
	{:else if lines.length === 0}
		<div class="px-4 py-8 text-center text-[13px] text-muted-foreground">Empty file</div>
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

<FormPanel
	open={deleteOpen}
	onOpenChange={(v) => (deleteOpen = v)}
	title="Delete file"
	description="Commits the removal of {blob.path} on {ref}."
	icon={Trash2}
>
	<div class="space-y-3">
		<FormField label="Commit message" id="delete-message" required>
			<Textarea id="delete-message" bind:value={deleteMessage} rows={3} />
		</FormField>
		<FormField label="Commit to a new branch" id="delete-branch" help="Leave empty to commit directly to {ref}">
			<Input id="delete-branch" bind:value={deleteBranch} placeholder="feature/remove-file" class="font-mono text-sm" />
		</FormField>
	</div>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (deleteOpen = false)}>Cancel</Button>
		<Button variant="destructive" onclick={confirmDelete} disabled={deleting || !deleteMessage.trim()}>
			{deleting ? 'Deleting...' : 'Commit Deletion'}
		</Button>
	{/snippet}
</FormPanel>

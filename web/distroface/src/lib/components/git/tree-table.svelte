<script lang="ts">
	import { resolve } from '$app/paths';
	import { Folder, File, FileCode, FileText, FileImage, GitBranch, Link2, CornerLeftUp } from '@lucide/svelte';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { relativeTime } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import type { GitTreeEntry } from '$lib/proto/distroface/v1/git_pb';
	import type { Component } from 'svelte';

	let {
		entries,
		namespace,
		name,
		ref,
		path = '',
		loading = false
	}: {
		entries: GitTreeEntry[];
		namespace: string;
		name: string;
		ref: string;
		path?: string;
		loading?: boolean;
	} = $props();

	const encodedRef = $derived(encodeURIComponent(ref));
	const parentPath = $derived(path.includes('/') ? path.slice(0, path.lastIndexOf('/')) : '');

	const sorted = $derived(
		[...entries].sort((a, b) => {
			const ad = a.type === 'tree' ? 0 : 1;
			const bd = b.type === 'tree' ? 0 : 1;
			if (ad !== bd) return ad - bd;
			return a.name.localeCompare(b.name);
		})
	);

	function pathOf(entry: GitTreeEntry): string {
		return entry.path || (path ? `${path}/${entry.name}` : entry.name);
	}

	function iconFor(entry: GitTreeEntry): Component<{ class?: string }> {
		if (entry.isSubmodule || entry.type === 'commit') return GitBranch;
		if (entry.type === 'tree') return Folder;
		if (entry.mode === '120000') return Link2;
		const lower = entry.name.toLowerCase();
		if (/\.(png|jpe?g|gif|svg|webp|ico|bmp)$/.test(lower)) return FileImage;
		if (/\.(md|markdown|txt|rst|adoc)$/.test(lower) || lower === 'license' || lower === 'readme') return FileText;
		if (/\.[a-z0-9]+$/.test(lower)) return FileCode;
		return File;
	}

	function title(entry: GitTreeEntry): string {
		return entry.lastCommit?.message.split('\n')[0] ?? '';
	}
</script>

<div class="rounded-xl border border-border/60 overflow-hidden min-w-0">
	<table class="w-full text-[13px] table-fixed">
		<tbody>
			{#if loading}
				{#each { length: 6 }, i (i)}
					<tr class="border-b border-border/40 last:border-0">
						<td class="px-3 py-2.5 w-8"><Skeleton class="h-4 w-4" /></td>
						<td class="px-2 py-2.5"><Skeleton class="h-4 w-40" /></td>
						<td class="px-2 py-2.5 hidden md:table-cell"><Skeleton class="h-4 w-64" /></td>
						<td class="px-3 py-2.5 w-24 hidden sm:table-cell"><Skeleton class="h-4 w-16 ml-auto" /></td>
					</tr>
				{/each}
			{:else}
				{#if path}
					<tr class="border-b border-border/40 hover:bg-muted/30 transition-colors">
						<td class="px-3 py-2 w-8">
							<CornerLeftUp class="h-4 w-4 text-muted-foreground/60" />
						</td>
						<td class="px-2 py-2" colspan="3">
							{#if parentPath}
							<a href={resolve('/[namespace]/[name]/tree/[ref]/[...path]', { namespace, name, ref: encodedRef, path: parentPath })} class="text-muted-foreground hover:text-primary font-mono">..</a>
						{:else}
							<a href={resolve('/[namespace]/[name]', { namespace, name })} class="text-muted-foreground hover:text-primary font-mono">..</a>
						{/if}
						</td>
					</tr>
				{/if}
				{#each sorted as entry (entry.sha + entry.name)}
					{@const Icon = iconFor(entry)}
					{@const commit = entry.lastCommit}
					<tr class="border-b border-border/40 last:border-0 hover:bg-muted/30 transition-colors group">
						<td class="px-3 py-2 w-8">
							<Icon class="h-4 w-4 {entry.type === 'tree' ? 'text-primary/70 fill-primary/15' : 'text-muted-foreground/60'}" />
						</td>
						<td class="px-2 py-2 min-w-0 md:max-w-[16rem]">
							{#if entry.isSubmodule || entry.type === 'commit'}
								<span class="truncate block text-foreground/90" title="Submodule {entry.sha.slice(0, 7)}">
									{entry.name} <span class="text-muted-foreground font-mono text-[11px]">@ {entry.sha.slice(0, 7)}</span>
								</span>
							{:else}
								{#if entry.type === 'tree'}
									<a href={resolve('/[namespace]/[name]/tree/[ref]/[...path]', { namespace, name, ref: encodedRef, path: pathOf(entry) })} class="truncate block hover:text-primary hover:underline underline-offset-2 transition-colors">
										{entry.name}
									</a>
								{:else}
									<a href={resolve('/[namespace]/[name]/blob/[ref]/[...path]', { namespace, name, ref: encodedRef, path: pathOf(entry) })} class="truncate block hover:text-primary hover:underline underline-offset-2 transition-colors">
										{entry.name}
									</a>
								{/if}
							{/if}
						</td>
						<td class="px-2 py-2 hidden md:table-cell min-w-0">
							{#if commit}
								<a href={resolve('/[namespace]/[name]/commit/[sha]', { namespace, name, sha: commit.sha })} class="text-muted-foreground hover:text-primary truncate block max-w-[36rem]" title={title(entry)}>
									{title(entry)}
								</a>
							{/if}
						</td>
						<td class="px-3 py-2 w-28 text-right text-muted-foreground/70 whitespace-nowrap hidden sm:table-cell tabular-nums">
							{#if commit?.author?.at}
								<span title={timestampDate(commit.author.at).toLocaleString()}>{relativeTime(timestampDate(commit.author.at))}</span>
							{:else if entry.type !== 'tree' && entry.size > 0n}
								<span class="text-[12px]">{Number(entry.size).toLocaleString()} B</span>
							{/if}
						</td>
					</tr>
				{/each}
				{#if sorted.length === 0 && !path}
					<tr>
						<td colspan="4" class="px-4 py-8 text-center text-muted-foreground">This directory is empty</td>
					</tr>
				{/if}
			{/if}
		</tbody>
	</table>
</div>

<script lang="ts">
	import { resolve } from '$app/paths';
	import {
		Folder,
		File,
		FileCode,
		FileText,
		FileImage,
		GitBranch,
		Link,
		CornerLeftUp
	} from '$lib/icons';
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
		if (entry.mode === '120000') return Link;
		const lower = entry.name.toLowerCase();
		if (/\.(png|jpe?g|gif|svg|webp|ico|bmp)$/.test(lower)) return FileImage;
		if (/\.(md|markdown|txt|rst|adoc)$/.test(lower) || lower === 'license' || lower === 'readme')
			return FileText;
		if (/\.[a-z0-9]+$/.test(lower)) return FileCode;
		return File;
	}

	function title(entry: GitTreeEntry): string {
		return entry.lastCommit?.message.split('\n')[0] ?? '';
	}
</script>

<div class="min-w-0">
	<table class="w-full table-fixed text-sm">
		<tbody class="divide-y">
			{#if loading}
				{#each { length: 6 }, i (i)}
					<tr>
						<td class="w-8 px-3 py-2.5"><Skeleton class="size-4" /></td>
						<td class="px-2 py-2.5"><Skeleton class="h-4 w-40" /></td>
						<td class="hidden px-2 py-2.5 md:table-cell"><Skeleton class="h-4 w-64" /></td>
						<td class="hidden w-24 px-3 py-2.5 sm:table-cell"
							><Skeleton class="ml-auto h-4 w-16" /></td
						>
					</tr>
				{/each}
			{:else}
				{#if path}
					<tr class="transition-colors hover:bg-muted/30">
						<td class="w-8 px-3 py-2">
							<CornerLeftUp class="size-4 text-muted-foreground" />
						</td>
						<td class="px-2 py-2" colspan="3">
							{#if parentPath}
								<a
									href={resolve('/[namespace]/[name]/tree/[ref]/[...path]', {
										namespace,
										name,
										ref: encodedRef,
										path: parentPath
									})}
									class="text-muted-foreground hover:text-primary">..</a
								>
							{:else}
								<a
									href={resolve('/[namespace]/[name]', { namespace, name })}
									class="text-muted-foreground hover:text-primary">..</a
								>
							{/if}
						</td>
					</tr>
				{/if}
				{#each sorted as entry (entry.sha + entry.name)}
					{@const Icon = iconFor(entry)}
					{@const commit = entry.lastCommit}
					<tr class="group transition-colors hover:bg-muted/30">
						<td class="w-8 px-3 py-2">
							<Icon class="size-4 text-muted-foreground" />
						</td>
						<td class="min-w-0 px-2 py-2 md:max-w-64">
							{#if entry.isSubmodule || entry.type === 'commit'}
								<span
									class="block truncate text-foreground"
									title="Submodule {entry.sha.slice(0, 7)}"
								>
									{entry.name}
									<span class="font-mono text-2xs text-muted-foreground"
										>@ {entry.sha.slice(0, 7)}</span
									>
								</span>
							{:else if entry.type === 'tree'}
								<a
									href={resolve('/[namespace]/[name]/tree/[ref]/[...path]', {
										namespace,
										name,
										ref: encodedRef,
										path: pathOf(entry)
									})}
									class="block truncate underline-offset-2 transition-colors hover:text-primary hover:underline"
								>
									{entry.name}
								</a>
							{:else}
								<a
									href={resolve('/[namespace]/[name]/blob/[ref]/[...path]', {
										namespace,
										name,
										ref: encodedRef,
										path: pathOf(entry)
									})}
									class="block truncate underline-offset-2 transition-colors hover:text-primary hover:underline"
								>
									{entry.name}
								</a>
							{/if}
						</td>
						<td class="hidden min-w-0 px-2 py-2 md:table-cell">
							{#if commit}
								<a
									href={resolve('/[namespace]/[name]/commit/[sha]', {
										namespace,
										name,
										sha: commit.sha
									})}
									class="block max-w-xl truncate text-muted-foreground hover:text-primary"
									title={title(entry)}
								>
									{title(entry)}
								</a>
							{/if}
						</td>
						<td
							class="hidden w-28 px-3 py-2 text-right text-xs whitespace-nowrap text-muted-foreground tabular-nums sm:table-cell"
						>
							{#if commit?.author?.at}
								<span title={timestampDate(commit.author.at).toLocaleString()}
									>{relativeTime(timestampDate(commit.author.at))}</span
								>
							{:else if entry.type !== 'tree' && entry.size > 0n}
								<span>{Number(entry.size).toLocaleString()} B</span>
							{/if}
						</td>
					</tr>
				{/each}
				{#if sorted.length === 0 && !path}
					<tr>
						<td colspan="4" class="px-4 py-8 text-center text-muted-foreground"
							>This directory is empty</td
						>
					</tr>
				{/if}
			{/if}
		</tbody>
	</table>
</div>

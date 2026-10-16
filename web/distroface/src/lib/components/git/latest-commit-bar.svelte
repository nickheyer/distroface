<script lang="ts">
	import { resolve } from '$app/paths';
	import { History } from '@lucide/svelte';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import CommitBadge from './commit-badge.svelte';
	import VerificationBadge from './verification-badge.svelte';
	import type { GitCommit } from '$lib/proto/distroface/v1/git_pb';
	import { relativeTime } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';

	let {
		commit,
		namespace,
		name,
		ref,
		path = '',
		loading = false
	}: {
		commit?: GitCommit;
		namespace: string;
		name: string;
		ref: string;
		path?: string;
		loading?: boolean;
	} = $props();

	const title = $derived(commit?.message.split('\n')[0] ?? '');
	const date = $derived(commit?.author?.at ? timestampDate(commit.author.at) : null);
	const commitsHref = $derived(resolve('/[namespace]/[name]/commits/[ref]', { namespace, name, ref: encodeURIComponent(ref) }));
	const initials = $derived((commit?.author?.username || commit?.author?.name || '?').slice(0, 2).toUpperCase());
</script>

<div class="rounded-xl border border-border/60 bg-muted/20 px-4 py-2.5 flex items-center gap-3 text-[13px] min-w-0">
	{#if loading && !commit}
		<Skeleton class="h-6 w-6 rounded-full" />
		<Skeleton class="h-4 w-72" />
		<Skeleton class="h-4 w-20 ml-auto" />
	{:else if commit}
		<div class="h-6 w-6 rounded-full bg-primary/10 text-primary/80 flex items-center justify-center shrink-0 text-[10px] font-bold">{initials}</div>
		<div class="flex items-center gap-1.5 min-w-0 flex-1">
			{#if commit.author?.username}
				<a href={resolve('/[namespace]', { namespace: commit.author.username })} class="font-medium hover:text-primary shrink-0">{commit.author.username}</a>
			{:else}
				<span class="font-medium shrink-0">{commit.author?.name ?? 'Unknown'}</span>
			{/if}
			<a href={resolve('/[namespace]/[name]/commit/[sha]', { namespace, name, sha: commit.sha })} class="text-muted-foreground hover:text-primary truncate" title={title}>{title}</a>
			<VerificationBadge verification={commit.verification} compact />
		</div>
		<div class="flex items-center gap-3 shrink-0 text-muted-foreground">
			<CommitBadge sha={commit.sha} {namespace} {name} />
			{#if date}
				<span class="hidden sm:inline tabular-nums" title={date.toLocaleString()}>{relativeTime(date)}</span>
			{/if}
			<a href={resolve(`${commitsHref}${path ? `?path=${encodeURIComponent(path)}` : ''}` as `/${string}`)} class="inline-flex items-center gap-1 hover:text-primary">
				<History class="h-3.5 w-3.5" />
				<span class="hidden sm:inline">History</span>
			</a>
		</div>
	{:else}
		<span class="text-muted-foreground">No commits</span>
	{/if}
</div>

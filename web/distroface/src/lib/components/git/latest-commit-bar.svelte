<script lang="ts">
	import { resolve } from '$app/paths';
	import { History } from '@lucide/svelte';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Avatar, AvatarFallback } from '$lib/components/ui/avatar';
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
	const commitsHref = $derived(
		resolve('/[namespace]/[name]/commits/[ref]', { namespace, name, ref: encodeURIComponent(ref) })
	);
	const initials = $derived(
		(commit?.author?.username || commit?.author?.name || '?').slice(0, 2).toUpperCase()
	);
</script>

<div
	class="flex min-w-0 items-center gap-3 border-b bg-muted/40 px-4 py-2.5 text-sm last:border-b-0"
>
	{#if loading && !commit}
		<Skeleton class="size-5 rounded-full" />
		<Skeleton class="h-4 w-72" />
		<Skeleton class="ml-auto h-4 w-20" />
	{:else if commit}
		<Avatar class="size-5">
			<AvatarFallback class="text-2xs">{initials}</AvatarFallback>
		</Avatar>
		<div class="flex min-w-0 flex-1 items-center gap-1.5">
			{#if commit.author?.username}
				<a
					href={resolve('/[namespace]', { namespace: commit.author.username })}
					class="shrink-0 font-medium hover:text-primary">{commit.author.username}</a
				>
			{:else}
				<span class="shrink-0 font-medium">{commit.author?.name ?? 'Unknown'}</span>
			{/if}
			<a
				href={resolve('/[namespace]/[name]/commit/[sha]', { namespace, name, sha: commit.sha })}
				class="truncate text-muted-foreground hover:text-primary"
				{title}>{title}</a
			>
			<VerificationBadge verification={commit.verification} compact />
		</div>
		<div class="flex shrink-0 items-center gap-3 text-xs text-muted-foreground">
			<CommitBadge sha={commit.sha} {namespace} {name} />
			{#if date}
				<span class="hidden tabular-nums sm:inline" title={date.toLocaleString()}
					>{relativeTime(date)}</span
				>
			{/if}
			<a
				href={resolve(
					`${commitsHref}${path ? `?path=${encodeURIComponent(path)}` : ''}` as `/${string}`
				)}
				class="inline-flex items-center gap-1 hover:text-primary"
			>
				<History class="size-3.5" />
				<span class="hidden sm:inline">History</span>
			</a>
		</div>
	{:else}
		<span class="text-muted-foreground">No commits</span>
	{/if}
</div>

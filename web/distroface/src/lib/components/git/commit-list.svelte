<script lang="ts">
	import { resolve } from '$app/paths';
	import { GitCommit as GitCommitIcon, ChevronDown, ChevronUp } from '$lib/icons';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Avatar, AvatarFallback } from '$lib/components/ui/avatar';
	import EmptyState from '$lib/components/empty-state.svelte';
	import CommitBadge from './commit-badge.svelte';
	import VerificationBadge from './verification-badge.svelte';
	import { CommitStatusState } from '$lib/proto/distroface/v1/git_pb';
	import type { GitCommit } from '$lib/proto/distroface/v1/git_pb';
	import { relativeTime } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { SvelteSet } from 'svelte/reactivity';

	let {
		commits,
		namespace,
		name,
		loading = false,
		emptyMessage = 'No commits yet',
		groupByDay = true
	}: {
		commits: GitCommit[];
		namespace: string;
		name: string;
		loading?: boolean;
		emptyMessage?: string;
		groupByDay?: boolean;
	} = $props();

	const expanded = new SvelteSet<string>();

	function when(c: GitCommit): Date | null {
		const ts = c.committer?.at ?? c.author?.at;
		return ts ? timestampDate(ts) : null;
	}

	function dayKey(c: GitCommit): string {
		const d = when(c);
		return d ? d.toDateString() : 'Unknown date';
	}

	function dayLabel(key: string): string {
		if (key === 'Unknown date') return key;
		const d = new Date(key);
		return d.toLocaleDateString(undefined, {
			weekday: 'short',
			month: 'short',
			day: 'numeric',
			year: 'numeric'
		});
	}

	const groups = $derived.by(() => {
		if (!groupByDay) return [{ key: '', commits }];
		const out: { key: string; commits: GitCommit[] }[] = [];
		for (const c of commits) {
			const key = dayKey(c);
			const last = out[out.length - 1];
			if (last && last.key === key) last.commits.push(c);
			else out.push({ key, commits: [c] });
		}
		return out;
	});

	function title(c: GitCommit): string {
		return c.message.split('\n')[0] ?? '';
	}

	function body(c: GitCommit): string {
		const idx = c.message.indexOf('\n');
		return idx < 0 ? '' : c.message.slice(idx + 1).trim();
	}

	function initials(c: GitCommit): string {
		const n = c.author?.username || c.author?.name || '?';
		return n
			.split(/[\s-]+/)
			.map((w) => w[0])
			.join('')
			.toUpperCase()
			.slice(0, 2);
	}

	function statusClass(state: CommitStatusState): string {
		switch (state) {
			case CommitStatusState.SUCCESS:
				return 'bg-success';
			case CommitStatusState.PENDING:
				return 'bg-warning';
			case CommitStatusState.FAILURE:
			case CommitStatusState.ERROR:
				return 'bg-destructive';
			default:
				return '';
		}
	}

	function statusTitle(state: CommitStatusState): string {
		switch (state) {
			case CommitStatusState.SUCCESS:
				return 'All checks passed';
			case CommitStatusState.PENDING:
				return 'Checks pending';
			case CommitStatusState.FAILURE:
				return 'Some checks failed';
			case CommitStatusState.ERROR:
				return 'Some checks errored';
			default:
				return '';
		}
	}

	function toggle(sha: string) {
		if (expanded.has(sha)) expanded.delete(sha);
		else expanded.add(sha);
	}
</script>

{#if loading && commits.length === 0}
	<div class="box divide-y">
		{#each { length: 5 }, i (i)}
			<div class="flex items-center gap-3 px-4 py-3">
				<Skeleton class="size-7 rounded-full" />
				<div class="flex-1 space-y-2">
					<Skeleton class="h-4 w-80" />
					<Skeleton class="h-3 w-40" />
				</div>
				<Skeleton class="h-5 w-16" />
			</div>
		{/each}
	</div>
{:else if commits.length === 0}
	<EmptyState icon={GitCommitIcon} message={emptyMessage} />
{:else}
	<div class="space-y-6 transition-opacity {loading ? 'opacity-60' : ''}">
		{#each groups as group (group.key)}
			<div class="space-y-2">
				{#if group.key}
					<div class="flex items-center gap-2 text-xs font-medium text-muted-foreground">
						<GitCommitIcon class="size-3.5" />
						<span>Commits on {dayLabel(group.key)}</span>
					</div>
				{/if}
				<div class="box divide-y">
					{#each group.commits as c (c.sha)}
						{@const open = expanded.has(c.sha)}
						{@const extra = body(c)}
						{@const date = when(c)}
						<div class="flex items-start gap-3 px-4 py-3">
							<Avatar class="mt-0.5 size-7">
								<AvatarFallback>{initials(c)}</AvatarFallback>
							</Avatar>
							<div class="min-w-0 flex-1">
								<div class="flex min-w-0 items-center gap-1.5">
									<a
										href={resolve('/[namespace]/[name]/commit/[sha]', {
											namespace,
											name,
											sha: c.sha
										})}
										class="truncate text-sm font-semibold transition-colors hover:text-primary"
										title={title(c)}
									>
										{title(c)}
									</a>
									{#if extra}
										<button
											type="button"
											class="inline-flex h-4 shrink-0 items-center rounded border px-1 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
											onclick={() => toggle(c.sha)}
											aria-label="Toggle commit body"
											aria-expanded={open}
										>
											{#if open}
												<ChevronUp class="size-3" />
											{:else}
												<ChevronDown class="size-3" />
											{/if}
										</button>
									{/if}
								</div>
								{#if open && extra}
									<pre
										class="mt-2 mb-1 rounded-md border bg-muted px-3 py-2 font-mono text-xs whitespace-pre-wrap text-muted-foreground">{extra}</pre>
								{/if}
								<div
									class="mt-0.5 flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground"
								>
									{#if c.author?.username}
										<a
											href={resolve('/[namespace]', { namespace: c.author.username })}
											class="font-medium text-foreground hover:text-primary">{c.author.username}</a
										>
									{:else}
										<span class="font-medium text-foreground">{c.author?.name ?? 'Unknown'}</span>
									{/if}
									{#if date}
										<span title={date.toLocaleString()}>committed {relativeTime(date)}</span>
									{/if}
									{#if c.committer && c.author && c.committer.email !== c.author.email}
										<span>via {c.committer.username || c.committer.name}</span>
									{/if}
									<VerificationBadge verification={c.verification} />
								</div>
							</div>
							<div class="flex shrink-0 items-center gap-2 self-start">
								{#if c.status !== CommitStatusState.UNSPECIFIED}
									<span class="status-dot {statusClass(c.status)}" title={statusTitle(c.status)}
									></span>
								{/if}
								<CommitBadge sha={c.sha} {namespace} {name} />
							</div>
						</div>
					{/each}
				</div>
			</div>
		{/each}
	</div>
{/if}

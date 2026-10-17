<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { FileX, FileCode } from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import EmptyState from '$lib/components/empty-state.svelte';
	import RefPicker from '$lib/components/git/ref-picker.svelte';
	import PathBreadcrumb from '$lib/components/git/path-breadcrumb.svelte';
	import CommitBadge from '$lib/components/git/commit-badge.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { highlightLines, languageFromPath, escapeHtml } from '$lib/highlight';
	import { relativeTime } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import type { GitBlameRange } from '$lib/proto/distroface/v1/git_pb';

	const repo = $derived(gitRepoStore.repo!);
	const namespace = $derived(repo.namespace);
	const name = $derived(repo.name);
	const ref = $derived(page.params.ref ?? '');
	const path = $derived(page.params.path ?? '');
	const encodedRef = $derived(encodeURIComponent(ref));

	let ranges = $state<GitBlameRange[]>([]);
	let loading = $state(true);
	let missing = $state(false);
	let html = $state<string[] | null>(null);
	let seq = 0;

	async function load() {
		const mine = ++seq;
		loading = true;
		missing = false;
		html = null;
		try {
			const resp = await rpcClient.gitFiles.blame({ namespace, name, ref, path }, silentCallOptions);
			if (mine !== seq) return;
			ranges = resp.ranges;
			const text = resp.ranges.flatMap((r) => r.lines).join('\n');
			highlightLines(text, languageFromPath(path)).then((out) => {
				if (mine === seq) html = out;
			});
		} catch {
			if (mine !== seq) return;
			ranges = [];
			missing = true;
		} finally {
			if (mine === seq) loading = false;
		}
	}

	$effect(() => {
		void ref;
		void path;
		void repo.id;
		load();
	});

	function onRef(next: string) {
		goto(resolve('/[namespace]/[name]/blame/[ref]/[...path]', { namespace, name, ref: encodeURIComponent(next), path }));
	}

	// Oldest commits fade, newest stand out
	const ages = $derived.by(() => {
		const times = ranges.map((r) => (r.commit?.author?.at ? timestampDate(r.commit.author.at).getTime() : 0)).filter((t) => t > 0);
		const min = Math.min(...times);
		const max = Math.max(...times);
		return { min, max };
	});

	function heat(r: GitBlameRange): number {
		const t = r.commit?.author?.at ? timestampDate(r.commit.author.at).getTime() : 0;
		if (!t || ages.max === ages.min) return 0.15;
		return 0.08 + 0.5 * ((t - ages.min) / (ages.max - ages.min));
	}

	const range = $derived.by(() => {
		const m = /^#L(\d+)(?:-L?(\d+))?$/.exec(page.url.hash);
		if (!m) return null;
		const a = Number(m[1]);
		const b = m[2] ? Number(m[2]) : a;
		return { start: Math.min(a, b), end: Math.max(a, b) };
	});

	const totalLines = $derived(ranges.reduce((n, r) => n + r.lines.length, 0));
</script>

<div class="space-y-4">
	<div class="flex items-center gap-3 flex-wrap">
		<RefPicker {namespace} {name} current={ref} defaultBranch={repo.defaultBranch} onSelect={onRef} />
		<PathBreadcrumb {namespace} {name} {ref} {path} leafIsFile />
		<div class="ml-auto">
			<Button variant="outline" size="sm" class="h-8" href={resolve('/[namespace]/[name]/blob/[ref]/[...path]', { namespace, name, ref: encodedRef, path })}>
				<FileCode class="h-3.5 w-3.5 mr-1.5" />View file
			</Button>
		</div>
	</div>

	{#if loading}
		<Skeleton class="h-96 w-full rounded-xl" />
	{:else if missing}
		<EmptyState icon={FileX} message="Cannot blame this file" description="{path} does not exist at {ref} or is binary." />
	{:else}
		<div class="rounded-xl border border-border/60 overflow-hidden bg-card">
			<div class="px-3 py-2 border-b border-border/50 bg-muted/20 text-[12.5px] text-muted-foreground tabular-nums">
				{totalLines} line{totalLines !== 1 ? 's' : ''} across {ranges.length} commit range{ranges.length !== 1 ? 's' : ''}
			</div>
			<div class="overflow-x-auto">
				<table class="code-table">
					<tbody>
						{#each ranges as r, ri (ri)}
							{@const c = r.commit}
							{@const offset = ranges.slice(0, ri).reduce((n, x) => n + x.lines.length, 0)}
							{@const title = c?.message.split('\n')[0] ?? ''}
							{#each r.lines as line, li (li)}
								{@const n = r.startLine + li}
								{@const idx = offset + li}
								<tr id="L{n}" class={range && n >= range.start && n <= range.end ? 'highlighted' : ''}>
									{#if li === 0}
										<td
											rowspan={r.lines.length}
											class="align-top border-t border-border/40 border-r px-3 py-1 text-[12px] whitespace-nowrap w-72 max-w-72 font-sans"
											style="background-color: color-mix(in oklch, var(--primary) {Math.round(heat(r) * 100)}%, transparent)"
										>
											{#if c}
												<div class="flex items-center gap-2 min-w-0">
													<CommitBadge sha={c.sha} {namespace} {name} copy={false} />
													<a href={resolve('/[namespace]/[name]/commit/[sha]', { namespace, name, sha: c.sha })} class="truncate hover:text-primary" title={title}>{title}</a>
												</div>
												<div class="text-muted-foreground mt-0.5 truncate">
													{c.author?.username || c.author?.name}
													{#if c.author?.at}
														<span title={timestampDate(c.author.at).toLocaleString()}>, {relativeTime(timestampDate(c.author.at))}</span>
													{/if}
												</div>
											{/if}
										</td>
									{/if}
									<td class="line-no {li === 0 ? 'border-t border-border/40' : ''}"><a href="#L{n}">{n}</a></td>
									<td class="code-line {li === 0 ? 'border-t border-border/40' : ''}">
										{#if html && html[idx] !== undefined}
											<!-- eslint-disable-next-line svelte/no-at-html-tags -->
											{@html html[idx]}
										{:else}
											<!-- eslint-disable-next-line svelte/no-at-html-tags -->
											{@html escapeHtml(line)}
										{/if}
									</td>
								</tr>
							{/each}
						{/each}
					</tbody>
				</table>
			</div>
		</div>
	{/if}
</div>

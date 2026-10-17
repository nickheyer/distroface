<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { GitCommit as GitCommitIcon, GitBranch, Tag, Undo2, Cherry, ExternalLink, CheckCircle2, XCircle, Clock, AlertCircle } from '@lucide/svelte';
	import { safeHref } from '$lib/utils';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import EmptyState from '$lib/components/empty-state.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import FormPanel from '$lib/components/form-panel.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import CommitBadge from '$lib/components/git/commit-badge.svelte';
	import VerificationBadge from '$lib/components/git/verification-badge.svelte';
	import DiffViewer from '$lib/components/git/diff-viewer.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { CommitStatusState, GitUnit } from '$lib/proto/distroface/v1/git_pb';
	import type { GitCommit, GitCommitStatus } from '$lib/proto/distroface/v1/git_pb';
	import { relativeTime } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { toast } from 'svelte-sonner';

	const repo = $derived(gitRepoStore.repo!);
	const namespace = $derived(repo.namespace);
	const name = $derived(repo.name);
	const sha = $derived(page.params.sha ?? '');
	const canWrite = $derived(gitRepoStore.canWrite(GitUnit.CODE) && !repo.isArchived);

	let commit = $state<GitCommit | undefined>();
	let statuses = $state<GitCommitStatus[]>([]);
	let branches = $state<string[]>([]);
	let tags = $state<string[]>([]);
	let loading = $state(true);
	let missing = $state(false);
	let seq = 0;

	async function load() {
		const mine = ++seq;
		loading = true;
		missing = false;
		try {
			const resp = await rpcClient.gitFiles.getCommit({ namespace, name, sha }, silentCallOptions);
			if (mine !== seq) return;
			commit = resp.commit;
			statuses = resp.statuses;
			branches = resp.branches;
			tags = resp.tags;
			missing = !resp.commit;
		} catch {
			if (mine !== seq) return;
			commit = undefined;
			missing = true;
		} finally {
			if (mine === seq) loading = false;
		}
	}

	const parentSha = $derived(commit?.parents[0] ?? '');

	$effect(() => {
		void sha;
		void repo.id;
		load();
	});

	const title = $derived(commit?.message.split('\n')[0] ?? '');
	const body = $derived.by(() => {
		if (!commit) return '';
		const idx = commit.message.indexOf('\n');
		return idx < 0 ? '' : commit.message.slice(idx + 1).trim();
	});
	const authorDate = $derived(commit?.author?.at ? timestampDate(commit.author.at) : null);
	const committerDate = $derived(commit?.committer?.at ? timestampDate(commit.committer.at) : null);
	const differentCommitter = $derived(!!commit?.committer && !!commit?.author && commit.committer.email !== commit.author.email);

	function statusIcon(s: CommitStatusState) {
		switch (s) {
			case CommitStatusState.SUCCESS:
				return CheckCircle2;
			case CommitStatusState.FAILURE:
				return XCircle;
			case CommitStatusState.ERROR:
				return AlertCircle;
			default:
				return Clock;
		}
	}

	function statusColor(s: CommitStatusState): string {
		switch (s) {
			case CommitStatusState.SUCCESS:
				return 'text-success';
			case CommitStatusState.FAILURE:
			case CommitStatusState.ERROR:
				return 'text-destructive';
			default:
				return 'text-amber-500';
		}
	}

	// Revert and cherry pick share one dialog
	let opOpen = $state(false);
	let op = $state<'revert' | 'cherry'>('revert');
	let opBranch = $state('');
	let opNewBranch = $state('');
	let opBusy = $state(false);

	function openOp(kind: 'revert' | 'cherry') {
		op = kind;
		opBranch = repo.defaultBranch;
		opNewBranch = '';
		opOpen = true;
	}

	async function runOp() {
		if (!opBranch.trim() || opBusy) return;
		opBusy = true;
		try {
			const req = { namespace, name, sha, branch: opBranch.trim(), newBranch: opNewBranch.trim() };
			const resp = op === 'revert' ? await rpcClient.gitFiles.revertCommit(req) : await rpcClient.gitFiles.cherryPickCommit(req);
			toast.success(op === 'revert' ? 'Commit reverted' : 'Commit cherry picked');
			opOpen = false;
			gitRepoStore.reload();
			if (resp.commit) await goto(resolve('/[namespace]/[name]/commit/[sha]', { namespace, name, sha: resp.commit.sha }));
		} catch {
			// Error interceptor already toasted
		} finally {
			opBusy = false;
		}
	}
</script>

<div class="space-y-5">
	{#if loading && !commit}
		<Skeleton class="h-32 w-full rounded-xl" />
		<Skeleton class="h-64 w-full rounded-xl" />
	{:else if missing || !commit}
		<EmptyState icon={GitCommitIcon} message="Commit not found" description="{sha} does not exist in this repository." />
	{:else}
		<div class="rounded-xl border border-border/60 bg-card overflow-hidden">
			<div class="px-5 py-4 space-y-3">
				<div class="flex items-start gap-4 flex-wrap">
					<div class="flex-1 min-w-0 space-y-2">
						<h2 class="text-lg font-semibold leading-snug wrap-break-word">{title}</h2>
						{#if body}
							<pre class="text-[13px] whitespace-pre-wrap font-mono text-muted-foreground bg-muted/30 rounded-lg p-3 border border-border/40">{body}</pre>
						{/if}
						<div class="flex items-center gap-2 flex-wrap text-[12.5px]">
							{#if branches.length > 0}
								{#each branches as b (b)}
									<a href={resolve('/[namespace]/[name]/tree/[ref]/[...path]', { namespace, name, ref: encodeURIComponent(b), path: '' })} class="inline-flex items-center gap-1 rounded-md border border-border/60 px-1.5 py-0.5 text-muted-foreground hover:text-primary font-mono text-[11.5px]">
										<GitBranch class="h-3 w-3" />{b}
									</a>
								{/each}
							{/if}
							{#each tags as t (t)}
								<a href={resolve('/[namespace]/[name]/tree/[ref]/[...path]', { namespace, name, ref: encodeURIComponent(t), path: '' })} class="inline-flex items-center gap-1 rounded-md border border-border/60 px-1.5 py-0.5 text-muted-foreground hover:text-primary font-mono text-[11.5px]">
									<Tag class="h-3 w-3" />{t}
								</a>
							{/each}
						</div>
					</div>
					<PermissionGate allowed={canWrite}>
						<div class="flex items-center gap-2 shrink-0">
							<Button variant="outline" size="sm" class="h-8" onclick={() => openOp('revert')}>
								<Undo2 class="h-3.5 w-3.5 mr-1.5" />Revert
							</Button>
							<Button variant="outline" size="sm" class="h-8" onclick={() => openOp('cherry')}>
								<Cherry class="h-3.5 w-3.5 mr-1.5" />Cherry pick
							</Button>
						</div>
					</PermissionGate>
				</div>
			</div>
			<div class="border-t border-border/50 bg-muted/20 px-5 py-3 flex items-center gap-x-5 gap-y-2 flex-wrap text-[12.5px] text-muted-foreground">
				<span class="flex items-center gap-1.5">
					<span class="h-5 w-5 rounded-full bg-primary/10 text-primary/80 flex items-center justify-center text-[9px] font-bold">
						{(commit.author?.username || commit.author?.name || '?').slice(0, 2).toUpperCase()}
					</span>
					{#if commit.author?.username}
						<a href={resolve('/[namespace]', { namespace: commit.author.username })} class="font-medium text-foreground hover:text-primary">{commit.author.username}</a>
					{:else}
						<span class="font-medium text-foreground">{commit.author?.name}</span>
					{/if}
					{#if authorDate}
						<span title={authorDate.toLocaleString()}>authored {relativeTime(authorDate)}</span>
					{/if}
				</span>
				{#if differentCommitter}
					<span>
						<span class="font-medium text-foreground">{commit.committer?.username || commit.committer?.name}</span>
						{#if committerDate}
							<span title={committerDate.toLocaleString()}>committed {relativeTime(committerDate)}</span>
						{/if}
					</span>
				{/if}
				<VerificationBadge verification={commit.verification} />
				<span class="sm:ml-auto flex items-center gap-3 flex-wrap">
					<span>commit <CommitBadge sha={commit.sha} {namespace} {name} length={10} /></span>
					{#if commit.parents.length > 0}
						<span class="flex items-center gap-1">
							{commit.parents.length > 1 ? 'parents' : 'parent'}
							{#each commit.parents as p (p)}
								<CommitBadge sha={p} {namespace} {name} copy={false} />
							{/each}
						</span>
					{/if}
				</span>
			</div>
			{#if statuses.length > 0}
				<div class="border-t border-border/50 px-5 py-3 space-y-1.5">
					{#each statuses as s (s.id)}
						{@const Icon = statusIcon(s.state)}
						<div class="flex items-center gap-2 text-[12.5px]">
							<Icon class="h-3.5 w-3.5 shrink-0 {statusColor(s.state)}" />
							<span class="font-medium">{s.context}</span>
							{#if s.description}
								<span class="text-muted-foreground truncate">{s.description}</span>
							{/if}
							{#if safeHref(s.targetUrl)}
								<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
								<a href={safeHref(s.targetUrl)} target="_blank" rel="noopener noreferrer" class="ml-auto text-primary hover:underline inline-flex items-center gap-1 shrink-0">
									Details<ExternalLink class="h-3 w-3" />
								</a>
							{/if}
						</div>
					{/each}
				</div>
			{/if}
		</div>

		<DiffViewer
			{namespace}
			{name}
			head={commit.sha}
			base={parentSha}
			resetKey={commit.sha}
			listFiles={async (p) => {
				const resp = await rpcClient.gitFiles.getCommitDiff({ namespace, name, sha: commit?.sha ?? sha, parent: 0, page: p }, silentCallOptions);
				return { files: resp.diff?.files ?? [], page: resp.page, totalFiles: resp.diff?.totalFiles ?? 0, additions: resp.diff?.additions ?? 0, deletions: resp.diff?.deletions ?? 0, truncated: resp.diff?.truncated ?? false };
			}}
			loadFile={async (path) => (await rpcClient.gitFiles.getDiffFile({ namespace, name, base: parentSha, head: commit?.sha ?? sha, path }, silentCallOptions)).file}
		/>
	{/if}
</div>

<FormPanel
	open={opOpen}
	onOpenChange={(v) => (opOpen = v)}
	title={op === 'revert' ? 'Revert commit' : 'Cherry pick commit'}
	description={op === 'revert' ? `Commits the inverse of ${sha.slice(0, 7)} onto a branch.` : `Applies ${sha.slice(0, 7)} onto a branch.`}
	icon={op === 'revert' ? Undo2 : Cherry}
>
	<div class="space-y-3">
		<FormField label="Target branch" id="op-branch" required>
			<Input id="op-branch" bind:value={opBranch} class="font-mono text-sm" />
		</FormField>
		<FormField label="Commit to a new branch" id="op-new-branch" help="Created from the target branch when set">
			<Input id="op-new-branch" bind:value={opNewBranch} placeholder={op === 'revert' ? `revert-${sha.slice(0, 7)}` : `cherry-pick-${sha.slice(0, 7)}`} class="font-mono text-sm" />
		</FormField>
	</div>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (opOpen = false)}>Cancel</Button>
		<Button onclick={runOp} disabled={opBusy || !opBranch.trim()}>
			{opBusy ? 'Working...' : op === 'revert' ? 'Revert Commit' : 'Cherry Pick Commit'}
		</Button>
	{/snippet}
</FormPanel>

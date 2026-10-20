<script lang="ts">
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import {
		GitPullRequest,
		GitCompare,
		ArrowLeft,
		Loader2,
		GitFork,
		GitCommit as GitCommitIcon,
		FileDiff,
		AlertTriangle
	} from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Switch } from '$lib/components/ui/switch';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import EmptyState from '$lib/components/empty-state.svelte';
	import FormCard from '$lib/components/form-card.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import RefPicker from '$lib/components/git/ref-picker.svelte';
	import BaseRepoPicker from '$lib/components/git/base-repo-picker.svelte';
	import PagedCommitList from '$lib/components/git/paged-commit-list.svelte';
	import DiffViewer from '$lib/components/git/diff-viewer.svelte';
	import CommentBox from '$lib/components/git/comment-box.svelte';
	import LabelPicker from '$lib/components/git/label-picker.svelte';
	import AssigneePicker from '$lib/components/git/assignee-picker.svelte';
	import MilestonePicker from '$lib/components/git/milestone-picker.svelte';
	import PrincipalPicker from '$lib/components/git/principal-picker.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { authStore } from '$lib/stores/auth.svelte';
	import { GitUnit } from '$lib/proto/distroface/v1/git_pb';
	import type { CompareRefsResponse, GitRepository } from '$lib/proto/distroface/v1/git_pb';
	import { IssueState } from '$lib/proto/distroface/v1/issue_pb';
	import type { PullRequest } from '$lib/proto/distroface/v1/pull_pb';
	import { toast } from 'svelte-sonner';
	import { SvelteURLSearchParams } from 'svelte/reactivity';

	const repo = $derived(gitRepoStore.repo!);
	const namespace = $derived(repo.namespace);
	const name = $derived(repo.name);
	const repoFullName = $derived(`${namespace}/${name}`);
	const orgId = $derived(repo.isOrgNamespace ? repo.ownerId : '');
	const canOpen = $derived(
		authStore.isAuthenticated && !repo.isArchived && gitRepoStore.unitEnabled(GitUnit.PULLS)
	);

	// Fork heads arrive prefixed with their repo
	function splitHead(raw: string): { repo: string; ref: string } {
		const idx = raw.indexOf(':');
		if (idx < 0) return { repo: '', ref: raw };
		return { repo: raw.slice(0, idx), ref: raw.slice(idx + 1) };
	}

	const initialHead = splitHead(page.url.searchParams.get('head') ?? '');
	let base = $state(page.url.searchParams.get('base') || '');
	let headRepo = $state(initialHead.repo);
	let head = $state(initialHead.ref);

	$effect(() => {
		if (!base) base = repo.defaultBranch;
	});

	let forks = $state<GitRepository[]>([]);
	const headRepoFull = $derived(headRepo && headRepo !== repoFullName ? headRepo : '');
	const headNs = $derived(headRepoFull ? headRepoFull.split('/')[0] : namespace);
	const headName = $derived(headRepoFull ? headRepoFull.split('/').slice(1).join('/') : name);
	const compareHead = $derived(headRepoFull ? `${headRepoFull}:${head}` : head);

	async function loadForks() {
		try {
			const resp = await rpcClient.git.listForks(
				{ namespace, name, page: { pageSize: 100, pageToken: '', orderBy: '' } },
				silentCallOptions
			);
			forks = resp.repositories;
		} catch {
			forks = [];
		}
	}

	let result = $state<CompareRefsResponse | undefined>();
	let comparing = $state(false);
	let compareFailed = $state(false);
	let existing = $state<PullRequest | undefined>();
	let seq = 0;
	let debounce: ReturnType<typeof setTimeout> | undefined;

	async function compare() {
		const mine = ++seq;
		if (!head || !base) {
			result = undefined;
			comparing = false;
			return;
		}
		comparing = true;
		compareFailed = false;
		try {
			const resp = await rpcClient.gitFiles.compareRefs(
				{ namespace, name, base, head: compareHead },
				silentCallOptions
			);
			if (mine !== seq) return;
			result = resp;
			if (resp.ahead === 1) void suggestTitle(mine);
		} catch {
			if (mine !== seq) return;
			result = undefined;
			compareFailed = true;
		} finally {
			if (mine === seq) comparing = false;
		}
		try {
			const resp = await rpcClient.pull.listPullRequests(
				{
					namespace,
					name,
					state: IssueState.OPEN,
					head: compareHead,
					base,
					labels: [],
					page: { pageSize: 1, pageToken: '', orderBy: '' }
				},
				silentCallOptions
			);
			if (mine !== seq) return;
			existing = resp.pullRequests[0];
		} catch {
			if (mine !== seq) return;
			existing = undefined;
		}
	}

	$effect(() => {
		void base;
		void compareHead;
		clearTimeout(debounce);
		debounce = setTimeout(compare, 150);
	});

	function syncUrl() {
		const params = new SvelteURLSearchParams();
		if (base) params.set('base', base);
		if (head) params.set('head', compareHead);
		goto(resolve(`/${namespace}/${name}/pulls/new?${params.toString()}`), {
			replaceState: true,
			keepFocus: true,
			noScroll: true
		});
	}

	function pickBase(r: string) {
		base = r;
		syncUrl();
	}

	function pickHead(r: string) {
		head = r;
		syncUrl();
	}

	function pickHeadRepo(v: string) {
		headRepo = v === repoFullName ? '' : v;
		head = '';
		syncUrl();
	}

	// Targeting the upstream opens the pull request there with this repository as the head
	function pickBaseRepo(full: string) {
		const [ns, ...rest] = full.split('/');
		const params = new SvelteURLSearchParams();
		if (head) params.set('head', headRepoFull ? compareHead : `${repoFullName}:${head}`);
		goto(resolve(`/${ns}/${rest.join('/')}/pulls/new?${params.toString()}`));
	}

	let title = $state('');
	let titleTouched = $state(false);
	let body = $state('');
	let draft = $state(false);
	let labelIds = $state<string[]>([]);
	let assigneeIds = $state<string[]>([]);
	let milestoneId = $state('');
	let reviewerIds = $state<string[]>([]);
	let allowMaintainerEdits = $state(true);
	let submitting = $state(false);

	// Branch name becomes the title until the user types, a lone commit lends its subject
	$effect(() => {
		if (titleTouched || !head) return;
		title = head.replace(/[-_/]+/g, ' ').replace(/^\w/, (c) => c.toUpperCase());
	});

	async function suggestTitle(mine: number) {
		try {
			const resp = await rpcClient.gitFiles.listCompareCommits(
				{
					namespace,
					name,
					base,
					head: compareHead,
					page: { pageSize: 1, pageToken: '', orderBy: '' }
				},
				silentCallOptions
			);
			const subject = resp.commits[0]?.message.split('\n')[0] ?? '';
			if (mine === seq && !titleTouched && subject) title = subject;
		} catch {
			// The branch name stays as the title
		}
	}

	async function loadTemplate() {
		try {
			const resp = await rpcClient.gitFiles.getTemplates({ namespace, name }, silentCallOptions);
			if (!body && resp.pullRequestTemplate) body = resp.pullRequestTemplate;
		} catch {
			// No template, the body stays empty
		}
	}

	onMount(() => {
		loadForks();
		loadTemplate();
	});

	async function teamIdSet(): Promise<Set<string>> {
		if (!orgId) return new Set();
		try {
			const resp = await rpcClient.organization.listTeams(
				{ orgId, page: { pageSize: 200, pageToken: '', orderBy: '' } },
				silentCallOptions
			);
			return new Set(resp.teams.map((t) => t.id));
		} catch {
			return new Set();
		}
	}

	const canSubmit = $derived(
		!!title.trim() && !!head && !!base && !!result && !submitting && !comparing
	);

	async function submit() {
		if (!canSubmit) return;
		submitting = true;
		try {
			const teams = await teamIdSet();
			const resp = await rpcClient.pull.createPullRequest({
				namespace,
				name,
				base,
				head: compareHead,
				title: title.trim(),
				body,
				draft,
				labelIds,
				milestoneId,
				assigneeIds,
				reviewerIds: reviewerIds.filter((id) => !teams.has(id)),
				teamReviewerIds: reviewerIds.filter((id) => teams.has(id)),
				allowMaintainerEdits: headRepoFull ? allowMaintainerEdits : false
			});
			const n = Number(resp.pullRequest?.issue?.number ?? 0n);
			toast.success('Pull request opened');
			gitRepoStore.reload();
			await goto(
				resolve('/[namespace]/[name]/pulls/[number]', { namespace, name, number: String(n) })
			);
		} catch {
			// Error interceptor already toasted
		} finally {
			submitting = false;
		}
	}

	const identical = $derived(!!result && result.ahead === 0 && result.changedFiles === 0);
	const diffBase = $derived(result ? result.mergeBaseSha || result.baseSha : '');
	const resetKey = $derived(result ? `${diffBase}:${result.headSha}` : '');
</script>

<svelte:head>
	<title>New pull request - {repoFullName}</title>
</svelte:head>

{#if !canOpen}
	<EmptyState
		icon={GitPullRequest}
		message="Cannot open a pull request"
		description={authStore.isAuthenticated
			? 'Pull requests are disabled or the repository is archived.'
			: 'Sign in to open a pull request.'}
	>
		{#snippet actions()}
			<Button variant="outline" href="/{namespace}/{name}/pulls">Back to pull requests</Button>
		{/snippet}
	</EmptyState>
{:else}
	<div class="space-y-4">
		<div class="flex flex-wrap items-center gap-2">
			<Button variant="ghost" href="/{namespace}/{name}/pulls"><ArrowLeft />Pull requests</Button>
			<h2 class="section-title">New pull request</h2>
		</div>

		<div class="flex flex-wrap items-center gap-2 rounded-lg border bg-card px-4 py-3">
			<GitCompare class="size-4 text-muted-foreground" />
			<BaseRepoPicker {repo} onSelect={pickBaseRepo} />
			<RefPicker
				{namespace}
				{name}
				current={base}
				defaultBranch={repo.defaultBranch}
				label="base"
				kinds={['branch']}
				onSelect={pickBase}
			/>
			<span class="text-xs text-muted-foreground">&lt;-</span>
			{#if forks.length > 0 || headRepoFull}
				<Select
					type="single"
					value={headRepoFull || repoFullName}
					onValueChange={(v) => {
						if (v) pickHeadRepo(v);
					}}
				>
					<SelectTrigger class="max-w-full font-mono sm:max-w-64" aria-label="Head repository">
						<GitFork class="size-3.5 text-muted-foreground" />
						{headRepoFull || repoFullName}
					</SelectTrigger>
					<SelectContent>
						<SelectItem value={repoFullName}>{repoFullName}</SelectItem>
						{#each forks as f (f.id)}
							<SelectItem value={f.fullName}>{f.fullName}</SelectItem>
						{/each}
						{#if headRepoFull && !forks.some((f) => f.fullName === headRepoFull)}
							<SelectItem value={headRepoFull}>{headRepoFull}</SelectItem>
						{/if}
					</SelectContent>
				</Select>
			{/if}
			<RefPicker
				namespace={headNs}
				name={headName}
				current={head || 'Select branch'}
				defaultBranch=""
				label="compare"
				kinds={['branch']}
				onSelect={pickHead}
			/>
			{#if result}
				<span class="ml-1 text-xs text-muted-foreground tabular-nums">
					<span class="font-medium text-foreground">{result.ahead}</span> ahead,
					<span class="font-medium text-foreground">{result.behind}</span> behind
				</span>
			{/if}
			{#if comparing}
				<Loader2 class="size-3.5 animate-spin text-muted-foreground" />
			{/if}
		</div>

		{#if existing}
			<Alert variant="warning">
				<AlertTriangle />
				<AlertDescription>
					<span>
						An open pull request already exists for this comparison:
						<a
							href={resolve('/[namespace]/[name]/pulls/[number]', {
								namespace,
								name,
								number: String(existing.issue?.number ?? 0n)
							})}
							class="link font-medium"
							>#{Number(existing.issue?.number ?? 0n)} {existing.issue?.title}</a
						>
					</span>
				</AlertDescription>
			</Alert>
		{/if}

		{#if !head}
			<EmptyState
				icon={GitCompare}
				message="Choose a branch to compare"
				description="Pick the branch with your changes to preview commits and a diff against {base}."
			/>
		{:else if compareFailed}
			<EmptyState
				icon={GitCompare}
				message="Nothing to compare"
				description="{base} and {compareHead} could not be compared. Check that both branches exist."
			/>
		{:else if identical}
			<EmptyState
				icon={GitCompare}
				message="These branches are identical"
				description="There is nothing to open a pull request for between {base} and {compareHead}."
			/>
		{:else}
			<div class="grid gap-6 lg:grid-cols-[minmax(0,1fr)_18rem]">
				<div class="min-w-0">
					<FormCard
						title="Describe the change"
						description="Reviewers read this first, link issues it closes with #number"
						icon={GitPullRequest}
					>
						<div class="space-y-4">
							<FormField label="Title" id="pr-title" required>
								<Input
									id="pr-title"
									bind:value={title}
									oninput={() => (titleTouched = true)}
									placeholder="Short summary of the change"
								/>
							</FormField>
							<FormField label="Description" id="pr-body">
								<CommentBox
									bind:value={body}
									placeholder="Describe the change"
									submitLabel="Create pull request"
									{submitting}
									disabled={submitting}
									onSubmit={submit}
									hideSubmit
								/>
							</FormField>
							<FormField
								label="Draft"
								layout="toggle"
								help="Marks the pull request as a work in progress that cannot be merged yet"
							>
								<Switch bind:checked={draft} />
							</FormField>
							{#if headRepoFull}
								<FormField
									label="Allow maintainer edits"
									layout="toggle"
									help="Maintainers of the base repository may push to your branch"
								>
									<Switch bind:checked={allowMaintainerEdits} />
								</FormField>
							{/if}
						</div>
						{#snippet footer()}
							<Button variant="outline" href="/{namespace}/{name}/pulls">Cancel</Button>
							<Button onclick={submit} disabled={!canSubmit}>
								{#if submitting}<Loader2 class="animate-spin" />{/if}
								{submitting
									? 'Creating'
									: draft
										? 'Create draft pull request'
										: 'Create pull request'}
							</Button>
						{/snippet}
					</FormCard>
				</div>

				<aside class="grid gap-5 text-sm sm:grid-cols-2 lg:block lg:space-y-5">
					<section class="space-y-2">
						<span class="eyebrow">Reviewers</span>
						<PrincipalPicker bind:selected={reviewerIds} {orgId} placeholder="No reviewers" />
					</section>
					<section class="space-y-2 lg:border-t lg:pt-5">
						<AssigneePicker {namespace} {name} bind:selected={assigneeIds} canEdit={true} />
					</section>
					<section class="space-y-2 lg:border-t lg:pt-5">
						<LabelPicker {namespace} {name} bind:selected={labelIds} canEdit={true} />
					</section>
					<section class="space-y-2 lg:border-t lg:pt-5">
						<MilestonePicker {namespace} {name} bind:selected={milestoneId} canEdit={true} />
					</section>
				</aside>
			</div>

			{#if comparing && !result}
				<Skeleton class="h-40 w-full rounded-lg" />
			{:else if result}
				<section class="space-y-3">
					<h2 class="inline-flex items-center gap-1.5 text-sm font-semibold">
						<GitCommitIcon class="size-4 text-muted-foreground" />
						{result.ahead} commit{result.ahead !== 1 ? 's' : ''}
					</h2>
					<PagedCommitList
						{namespace}
						{name}
						{resetKey}
						fetch={(p) =>
							rpcClient.gitFiles.listCompareCommits(
								{ namespace, name, base, head: compareHead, page: p },
								silentCallOptions
							)}
						emptyMessage="No commits between these branches"
					/>
				</section>
				<section class="space-y-3">
					<h2 class="inline-flex items-center gap-1.5 text-sm font-semibold">
						<FileDiff class="size-4 text-muted-foreground" />
						{result.changedFiles} file{result.changedFiles !== 1 ? 's' : ''} changed
					</h2>
					<DiffViewer
						{namespace}
						{name}
						head={result.headSha}
						base={diffBase}
						{resetKey}
						listFiles={async (p) => {
							const resp = await rpcClient.gitFiles.getCompareDiff(
								{ namespace, name, base, head: compareHead, page: p },
								silentCallOptions
							);
							return {
								files: resp.diff?.files ?? [],
								page: resp.page,
								totalFiles: resp.diff?.totalFiles ?? 0,
								additions: resp.diff?.additions ?? 0,
								deletions: resp.diff?.deletions ?? 0,
								truncated: resp.diff?.truncated ?? false
							};
						}}
						loadFile={async (path) =>
							(
								await rpcClient.gitFiles.getDiffFile(
									{ namespace, name, base: diffBase, head: result?.headSha ?? '', path },
									silentCallOptions
								)
							).file}
					/>
				</section>
			{/if}
		{/if}
	</div>
{/if}

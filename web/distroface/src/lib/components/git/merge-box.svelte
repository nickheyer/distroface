<script lang="ts">
	import { resolve } from '$app/paths';
	import {
		GitMerge,
		CheckCircle,
		XCircle,
		AlertCircle,
		Clock,
		Loader,
		GitBranch,
		ChevronDown,
		Undo,
		FileWarning,
		ShieldAlert,
		Zap,
		Trash,
		RotateCcw,
		Eye
	} from '$lib/icons';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Switch } from '$lib/components/ui/switch';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import FormField from '$lib/components/form-field.svelte';
	import {
		DropdownMenu,
		DropdownMenuContent,
		DropdownMenuItem,
		DropdownMenuTrigger
	} from '$lib/components/ui/dropdown-menu';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { GitMergeMethod, CommitStatusState } from '$lib/proto/distroface/v1/git_pb';
	import { MergeableState } from '$lib/proto/distroface/v1/pull_pb';
	import type { PullRequest } from '$lib/proto/distroface/v1/pull_pb';
	import {
		pullStatus,
		mergeMethodLabel,
		mergeMethodShort,
		blockerLabel,
		reviewSummary,
		statusLabel,
		headIsFork
	} from '$lib/pulls';
	import { relativeTime } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { toast } from 'svelte-sonner';

	let {
		namespace,
		name,
		pull,
		canMerge = false,
		canEdit = false,
		onChanged
	}: {
		namespace: string;
		name: string;
		pull: PullRequest;
		canMerge?: boolean;
		canEdit?: boolean;
		onChanged: () => void;
	} = $props();

	const number = $derived(pull.issue?.number ?? 0n);
	const status = $derived(pullStatus(pull));
	const repoFullName = $derived(`${namespace}/${name}`);
	const fork = $derived(headIsFork(pull, repoFullName));
	const reviews = $derived(reviewSummary(pull));
	const allowed = $derived(
		pull.allowedMethods.length > 0 ? pull.allowedMethods : [GitMergeMethod.MERGE]
	);
	const autoArmed = $derived(pull.autoMergeMethod !== GitMergeMethod.UNSPECIFIED);
	const checking = $derived(
		pull.mergeableState === MergeableState.UNKNOWN ||
			pull.mergeableState === MergeableState.CHECKING
	);
	const conflicts = $derived(pull.mergeableState === MergeableState.CONFLICT);
	const behind = $derived(pull.blockers.some((b) => b.rule === 'up_to_date'));
	const mergeNow = $derived(canMerge && pull.canMerge && !checking && !conflicts);

	let method = $state<GitMergeMethod>(GitMergeMethod.MERGE);
	let methodPull = '';
	let confirming = $state(false);
	let title = $state('');
	let message = $state('');
	let deleteBranch = $state(false);
	let busy = $state('');

	// Defaults reset when a different pull request arrives
	$effect(() => {
		const key = `${pull.issue?.id ?? ''}:${allowed.join(',')}`;
		if (methodPull === key) return;
		methodPull = key;
		method = allowed.includes(pull.defaultMethod) ? pull.defaultMethod : allowed[0];
		deleteBranch = !!gitRepoStore.repo?.merge?.deleteBranchOnMerge && !fork;
	});

	const needsMessage = $derived(
		method === GitMergeMethod.MERGE ||
			method === GitMergeMethod.SQUASH ||
			method === GitMergeMethod.REBASE_MERGE
	);

	function defaultTitle(m: GitMergeMethod): string {
		const n = Number(number);
		if (m === GitMergeMethod.SQUASH) return `${pull.issue?.title ?? ''} (!${n})`;
		const ns = pull.headRepoFullName.split('/')[0] || namespace;
		return `Merge pull request !${n} from ${ns}/${pull.headBranch}`;
	}

	function defaultMessage(m: GitMergeMethod): string {
		if (m === GitMergeMethod.SQUASH) return '';
		return pull.issue?.title ?? '';
	}

	function startConfirm() {
		title = defaultTitle(method);
		message = defaultMessage(method);
		confirming = true;
	}

	function changeMethod(v: string) {
		const m = Number(v) as GitMergeMethod;
		if (!allowed.includes(m)) return;
		method = m;
		if (confirming) {
			title = defaultTitle(m);
			message = defaultMessage(m);
		}
	}

	async function run(key: string, fn: () => Promise<unknown>, success?: string) {
		if (busy) return;
		busy = key;
		try {
			await fn();
			if (success) toast.success(success);
			onChanged();
		} catch {
			// Error interceptor already toasted
		} finally {
			busy = '';
		}
	}

	function merge() {
		run(
			'merge',
			() =>
				rpcClient.pull.mergePullRequest({
					namespace,
					name,
					number,
					method,
					title: needsMessage ? title : '',
					message: needsMessage ? message : '',
					deleteBranch,
					expectedHeadSha: pull.headSha
				}),
			'Pull request merged'
		).then(() => (confirming = false));
	}

	function scheduleAuto() {
		run(
			'auto',
			() =>
				rpcClient.pull.scheduleAutoMerge({
					namespace,
					name,
					number,
					method,
					title: needsMessage ? title : '',
					message: needsMessage ? message : '',
					deleteBranch
				}),
			'Auto-merge enabled'
		).then(() => (confirming = false));
	}

	function cancelAuto() {
		run(
			'cancel-auto',
			() => rpcClient.pull.cancelAutoMerge({ namespace, name, number }),
			'Auto-merge cancelled'
		);
	}

	function updateBranch(rebase: boolean) {
		run(
			'update',
			() => rpcClient.pull.updateBranch({ namespace, name, number, rebase }),
			rebase ? 'Branch rebased onto base' : 'Base merged into branch'
		);
	}

	function close(withDelete: boolean) {
		run(
			'close',
			() => rpcClient.pull.closePullRequest({ namespace, name, number, deleteBranch: withDelete }),
			'Pull request closed'
		);
	}

	function reopen() {
		run(
			'reopen',
			() => rpcClient.pull.reopenPullRequest({ namespace, name, number }),
			'Pull request reopened'
		);
	}

	function setDraft(draft: boolean) {
		run(
			'draft',
			() => rpcClient.pull.updatePullRequest({ namespace, name, number, draft }),
			draft ? 'Converted to draft' : 'Ready for review'
		);
	}

	function restoreBranch() {
		run(
			'restore',
			() => rpcClient.pull.restoreHeadBranch({ namespace, name, number }),
			'Branch restored'
		);
	}

	function deleteHeadBranch() {
		run(
			'delete-branch',
			() => rpcClient.gitRefs.deleteBranch({ namespace, name, branch: pull.headBranch }),
			'Branch deleted'
		);
	}

	// Unknown states recompute on view, poll until settled
	let attempts = 0;
	let timer: ReturnType<typeof setTimeout> | undefined;
	$effect(() => {
		void pull;
		clearTimeout(timer);
		if (!checking || status !== 'open') {
			attempts = 0;
			return;
		}
		if (attempts >= 6) return;
		timer = setTimeout(async () => {
			attempts++;
			try {
				await rpcClient.pull.getMergeability({ namespace, name, number }, silentCallOptions);
			} catch {
				// Pull reload below shows whatever the server has
			}
			onChanged();
		}, 2000);
		return () => clearTimeout(timer);
	});

	function checksIcon(s: CommitStatusState) {
		switch (s) {
			case CommitStatusState.SUCCESS:
				return CheckCircle;
			case CommitStatusState.FAILURE:
			case CommitStatusState.ERROR:
				return XCircle;
			case CommitStatusState.PENDING:
				return Clock;
			default:
				return AlertCircle;
		}
	}

	function checksColor(s: CommitStatusState): string {
		switch (s) {
			case CommitStatusState.SUCCESS:
				return 'text-success';
			case CommitStatusState.FAILURE:
			case CommitStatusState.ERROR:
				return 'text-destructive';
			case CommitStatusState.PENDING:
				return 'text-warning';
			default:
				return 'text-muted-foreground';
		}
	}

	const mergedWhen = $derived(pull.mergedAt ? relativeTime(timestampDate(pull.mergedAt)) : '');
	const canDeleteHead = $derived(
		canEdit && !fork && !pull.headDeleted && pull.headBranch !== gitRepoStore.repo?.defaultBranch
	);
</script>

{#snippet row(Icon: typeof CheckCircle, color: string, heading: string, detail: string)}
	<div class="flex items-start gap-3 px-4 py-3">
		<Icon class="mt-0.5 size-5 shrink-0 {color}" />
		<div class="min-w-0 flex-1">
			<p class="text-sm font-medium">{heading}</p>
			{#if detail}
				<p class="mt-0.5 text-xs text-muted-foreground">{detail}</p>
			{/if}
		</div>
	</div>
{/snippet}

<div class="box">
	{#if status === 'merged'}
		<div class="flex items-start gap-3 bg-done/10 px-4 py-3">
			<GitMerge class="mt-0.5 size-5 shrink-0 text-done" />
			<div class="min-w-0 flex-1">
				<p class="text-sm font-medium">Pull request successfully merged and closed</p>
				<p class="mt-0.5 text-xs text-muted-foreground">
					{#if pull.mergedBy}
						<a
							href={resolve('/[namespace]', { namespace: pull.mergedBy.username })}
							class="hover:underline">{pull.mergedBy.username}</a
						> merged
					{:else}
						Merged
					{/if}
					{pull.commitCount} commit{pull.commitCount !== 1 ? 's' : ''} into
					<span class="font-mono">{pull.baseBranch}</span>
					{#if pull.mergeMethod !== GitMergeMethod.UNSPECIFIED}
						by {mergeMethodShort(pull.mergeMethod)}
					{/if}
					{#if mergedWhen}{mergedWhen}{/if}
					{#if pull.mergeCommitSha}
						<a
							href={resolve('/[namespace]/[name]/commit/[sha]', {
								namespace,
								name,
								sha: pull.mergeCommitSha
							})}
							class="link ml-1 font-mono">{pull.mergeCommitSha.slice(0, 7)}</a
						>
					{/if}
				</p>
			</div>
		</div>
	{:else if status === 'closed'}
		<div class="flex items-start gap-3 bg-destructive/8 px-4 py-3">
			<XCircle class="mt-0.5 size-5 shrink-0 text-destructive" />
			<div class="min-w-0 flex-1">
				<p class="text-sm font-medium">Closed with unmerged commits</p>
				<p class="mt-0.5 text-xs text-muted-foreground">
					{#if pull.issue?.closedBy}
						<a
							href={resolve('/[namespace]', { namespace: pull.issue.closedBy.username })}
							class="hover:underline">{pull.issue.closedBy.username}</a
						> closed this
					{:else}
						Closed
					{/if}
					{#if pull.issue?.closedAt}{relativeTime(timestampDate(pull.issue.closedAt))}{/if}
					without merging
				</p>
			</div>
		</div>
	{:else if status === 'draft'}
		{@render row(
			FileWarning,
			'text-muted-foreground',
			'This pull request is still a work in progress',
			'Draft pull requests cannot be merged. Mark it ready for review when it is done.'
		)}
	{:else}
		<div class="divide-y">
			{#if reviews.approved > 0 && reviews.changesRequested === 0}
				{@render row(
					CheckCircle,
					'text-success',
					`${reviews.approved} approving review${reviews.approved !== 1 ? 's' : ''}`,
					reviews.awaiting > 0
						? `${reviews.awaiting} reviewer${reviews.awaiting !== 1 ? 's' : ''} still awaiting`
						: ''
				)}
			{:else if reviews.changesRequested > 0}
				{@render row(
					XCircle,
					'text-destructive',
					`${reviews.changesRequested} review${reviews.changesRequested !== 1 ? 's' : ''} requesting changes`,
					reviews.approved > 0
						? `${reviews.approved} approving`
						: 'Address the feedback and request a re-review'
				)}
			{:else if reviews.awaiting > 0}
				{@render row(
					Clock,
					'text-warning',
					`Awaiting review from ${reviews.awaiting} reviewer${reviews.awaiting !== 1 ? 's' : ''}`,
					''
				)}
			{:else}
				{@render row(
					AlertCircle,
					'text-muted-foreground',
					'No reviews yet',
					reviews.commented > 0
						? `${reviews.commented} comment review${reviews.commented !== 1 ? 's' : ''}`
						: ''
				)}
			{/if}

			{#if pull.status !== CommitStatusState.UNSPECIFIED}
				{@render row(
					checksIcon(pull.status),
					checksColor(pull.status),
					statusLabel(pull.status),
					''
				)}
			{/if}

			{#if checking}
				{@render row(
					Loader,
					'animate-spin text-muted-foreground',
					'Checking for ability to merge automatically',
					''
				)}
			{:else if conflicts}
				<div class="flex items-start gap-3 px-4 py-3">
					<AlertCircle class="mt-0.5 size-5 shrink-0 text-destructive" />
					<div class="min-w-0 flex-1">
						<p class="text-sm font-medium">This branch has conflicts that must be resolved</p>
						{#if pull.conflictPaths.length > 0}
							<ul class="mt-1.5 space-y-0.5">
								{#each pull.conflictPaths as p (p)}
									<li class="font-mono text-xs text-muted-foreground">{p}</li>
								{/each}
							</ul>
						{/if}
						<p class="mt-1.5 text-xs text-muted-foreground">
							Merge <span class="font-mono">{pull.baseBranch}</span> into your branch locally and push,
							or use Update branch when the base can merge cleanly
						</p>
					</div>
				</div>
			{:else if pull.mergeableState === MergeableState.BLOCKED}
				{@render row(
					ShieldAlert,
					'text-warning',
					'Merging is blocked',
					'Branch protection rules are not satisfied yet'
				)}
			{:else}
				{@render row(
					CheckCircle,
					'text-success',
					'No conflicts with the base branch',
					behind ? 'The head branch is behind the base' : 'Merging can be performed automatically'
				)}
			{/if}

			{#if pull.blockers.length > 0}
				<div class="px-4 py-3">
					<p class="eyebrow mb-1.5">Unmet rules</p>
					<ul class="space-y-1">
						{#each pull.blockers as b (b.rule + b.message)}
							<li class="flex items-start gap-2 text-xs">
								<XCircle class="mt-0.5 size-3.5 shrink-0 text-destructive" />
								<span
									><span class="font-medium">{blockerLabel(b.rule)}</span>{#if b.message}<span
											class="text-muted-foreground"
										>
											{b.message}</span
										>{/if}</span
								>
							</li>
						{/each}
					</ul>
				</div>
			{/if}

			{#if autoArmed}
				<div class="flex flex-wrap items-center gap-3 bg-primary/5 px-4 py-3">
					<Zap class="size-5 shrink-0 text-primary" />
					<div class="min-w-0 flex-1">
						<p class="text-sm font-medium">Auto-merge enabled</p>
						<p class="mt-0.5 text-xs text-muted-foreground">
							Merges by {mergeMethodShort(pull.autoMergeMethod)} once every rule passes{#if pull.autoMergeBy},
								scheduled by {pull.autoMergeBy}{/if}
						</p>
					</div>
					{#if canMerge}
						<Button variant="outline" class="shrink-0" onclick={cancelAuto} disabled={busy !== ''}>
							{#if busy === 'cancel-auto'}<Loader class="animate-spin" />{/if}
							Cancel auto-merge
						</Button>
					{/if}
				</div>
			{/if}
		</div>
	{/if}

	<div class="space-y-3 border-t bg-muted/30 px-4 py-3">
		{#if status === 'open' && canMerge}
			{#if confirming}
				<div class="space-y-4">
					{#if needsMessage}
						<FormField label="Commit title" id="merge-title">
							<Input id="merge-title" bind:value={title} placeholder={defaultTitle(method)} />
						</FormField>
						<FormField
							label="Commit message"
							id="merge-message"
							help="Optional extended description"
						>
							<Textarea id="merge-message" bind:value={message} rows={3} />
						</FormField>
					{/if}
					{#if !fork}
						<FormField
							label="Delete branch after merging"
							layout="toggle"
							help="Removes {pull.headBranch} once the merge lands"
						>
							<Switch bind:checked={deleteBranch} />
						</FormField>
					{/if}
					<div class="flex flex-wrap items-center gap-2">
						{#if mergeNow}
							<Button onclick={merge} disabled={busy !== ''}>
								{#if busy === 'merge'}<Loader class="animate-spin" />{:else}<GitMerge />{/if}
								Confirm {mergeMethodShort(method)}
							</Button>
						{:else}
							<Button onclick={scheduleAuto} disabled={busy !== ''}>
								{#if busy === 'auto'}<Loader class="animate-spin" />{:else}<Zap />{/if}
								Enable auto-merge
							</Button>
						{/if}
						<Button variant="ghost" onclick={() => (confirming = false)} disabled={busy !== ''}
							>Cancel</Button
						>
					</div>
				</div>
			{:else}
				<div class="flex flex-wrap items-center gap-2">
					<div class="inline-flex items-stretch">
						<Button
							class="rounded-r-none"
							onclick={startConfirm}
							disabled={busy !== '' || (!mergeNow && autoArmed)}
						>
							<GitMerge />
							{mergeNow
								? mergeMethodLabel(method)
								: autoArmed
									? 'Auto-merge enabled'
									: 'Enable auto-merge'}
						</Button>
						<Select type="single" value={String(method)} onValueChange={changeMethod}>
							<SelectTrigger
								class="w-9 justify-center rounded-l-none border-l-0 border-primary bg-primary px-0 text-primary-foreground hover:bg-primary/90 [&>svg:last-child]:hidden"
								aria-label="Merge method"
							>
								<ChevronDown class="size-3.5 text-primary-foreground" />
							</SelectTrigger>
							<SelectContent align="start">
								{#each allowed as m (m)}
									<SelectItem value={String(m)}>{mergeMethodLabel(m)}</SelectItem>
								{/each}
							</SelectContent>
						</Select>
					</div>
					{#if !mergeNow && !autoArmed}
						<span class="text-xs text-muted-foreground">Merges automatically once rules pass</span>
					{/if}
				</div>
			{/if}
		{:else if status === 'open' && !canMerge}
			<p class="text-xs text-muted-foreground">
				Only users with write access can merge this pull request
			</p>
		{/if}

		<div class="flex flex-wrap items-center gap-2">
			{#if status === 'open' && canEdit}
				<DropdownMenu>
					<DropdownMenuTrigger>
						{#snippet child({ props })}
							<Button {...props} variant="outline" disabled={busy !== ''}>
								{#if busy === 'update'}<Loader class="animate-spin" />{:else}<GitBranch />{/if}
								Update branch<ChevronDown class="opacity-60" />
							</Button>
						{/snippet}
					</DropdownMenuTrigger>
					<DropdownMenuContent align="start">
						<DropdownMenuItem onclick={() => updateBranch(false)}
							>Update with merge commit</DropdownMenuItem
						>
						<DropdownMenuItem onclick={() => updateBranch(true)}
							>Update with rebase</DropdownMenuItem
						>
					</DropdownMenuContent>
				</DropdownMenu>
				<Button variant="outline" onclick={() => setDraft(true)} disabled={busy !== ''}>
					{#if busy === 'draft'}<Loader class="animate-spin" />{:else}<FileWarning />{/if}
					Convert to draft
				</Button>
			{/if}
			{#if status === 'draft' && canEdit}
				<Button onclick={() => setDraft(false)} disabled={busy !== ''}>
					{#if busy === 'draft'}<Loader class="animate-spin" />{:else}<Eye />{/if}
					Ready for review
				</Button>
				<DropdownMenu>
					<DropdownMenuTrigger>
						{#snippet child({ props })}
							<Button {...props} variant="outline" disabled={busy !== ''}>
								{#if busy === 'update'}<Loader class="animate-spin" />{:else}<GitBranch />{/if}
								Update branch<ChevronDown class="opacity-60" />
							</Button>
						{/snippet}
					</DropdownMenuTrigger>
					<DropdownMenuContent align="start">
						<DropdownMenuItem onclick={() => updateBranch(false)}
							>Update with merge commit</DropdownMenuItem
						>
						<DropdownMenuItem onclick={() => updateBranch(true)}
							>Update with rebase</DropdownMenuItem
						>
					</DropdownMenuContent>
				</DropdownMenu>
			{/if}
			{#if (status === 'open' || status === 'draft') && canEdit}
				<div class="sm:ml-auto">
					{#if fork}
						<Button
							variant="outline-destructive"
							onclick={() => close(false)}
							disabled={busy !== ''}
						>
							{#if busy === 'close'}<Loader class="animate-spin" />{:else}<XCircle />{/if}
							Close pull request
						</Button>
					{:else}
						<DropdownMenu>
							<DropdownMenuTrigger>
								{#snippet child({ props })}
									<Button {...props} variant="outline-destructive" disabled={busy !== ''}>
										{#if busy === 'close'}<Loader class="animate-spin" />{:else}<XCircle />{/if}
										Close<ChevronDown class="opacity-60" />
									</Button>
								{/snippet}
							</DropdownMenuTrigger>
							<DropdownMenuContent align="end">
								<DropdownMenuItem onclick={() => close(false)}>Close pull request</DropdownMenuItem>
								<DropdownMenuItem onclick={() => close(true)}
									>Close and delete branch</DropdownMenuItem
								>
							</DropdownMenuContent>
						</DropdownMenu>
					{/if}
				</div>
			{/if}
			{#if status === 'closed' && canEdit}
				<Button variant="outline" onclick={reopen} disabled={busy !== ''}>
					{#if busy === 'reopen'}<Loader class="animate-spin" />{:else}<Undo />{/if}
					Reopen pull request
				</Button>
			{/if}
			{#if (status === 'merged' || status === 'closed') && canEdit && !fork}
				{#if pull.headDeleted}
					<Button variant="outline" onclick={restoreBranch} disabled={busy !== ''}>
						{#if busy === 'restore'}<Loader class="animate-spin" />{:else}<RotateCcw />{/if}
						Restore branch
					</Button>
				{:else if canDeleteHead}
					<Button variant="outline" onclick={deleteHeadBranch} disabled={busy !== ''}>
						{#if busy === 'delete-branch'}<Loader class="animate-spin" />{:else}<Trash />{/if}
						Delete branch
					</Button>
				{/if}
			{/if}
			{#if pull.headDeleted && status !== 'merged' && status !== 'closed'}
				<span class="text-xs text-muted-foreground">Head branch was deleted</span>
			{/if}
		</div>
	</div>
</div>

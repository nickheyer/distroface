<script lang="ts">
	import { resolve } from '$app/paths';
	import { Settings2, X, RotateCcw, Ban, Loader2, Users, Clock, CheckCircle2, XCircle, MessageSquare } from '@lucide/svelte';
	import { Popover, PopoverContent, PopoverTrigger } from '$lib/components/ui/popover';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Textarea } from '$lib/components/ui/textarea';
	import FormPanel from '$lib/components/form-panel.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import PrincipalPicker from './principal-picker.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { ReviewState } from '$lib/proto/distroface/v1/pull_pb';
	import type { PullRequest, Review } from '$lib/proto/distroface/v1/pull_pb';
	import { latestReviews, reviewStateLabel, reviewStateClass } from '$lib/pulls';
	import { toast } from 'svelte-sonner';

	let {
		namespace,
		name,
		pull,
		canRequest = false,
		canDismiss = false,
		orgId = '',
		onChanged
	}: {
		namespace: string;
		name: string;
		pull: PullRequest;
		canRequest?: boolean;
		canDismiss?: boolean;
		orgId?: string;
		onChanged: () => void;
	} = $props();

	const number = $derived(pull.issue?.number ?? 0n);
	const reviews = $derived(latestReviews(pull.reviews));
	const reviewedIds = $derived(new Set(reviews.map((r) => r.reviewer?.id ?? '')));
	// Requests without a submitted review still wait
	const requests = $derived(pull.reviewRequests.filter((r) => r.teamId || !r.fulfilled || !reviewedIds.has(r.user?.id ?? '')));

	let pickerOpen = $state(false);
	let selected = $state<string[]>([]);
	let saving = $state(false);
	let removing = $state('');
	let rerequesting = $state('');

	let dismissOpen = $state(false);
	let dismissTarget = $state<Review | undefined>();
	let dismissReason = $state('');
	let dismissing = $state(false);

	async function teamIdSet(): Promise<Set<string>> {
		if (!orgId) return new Set();
		try {
			const resp = await rpcClient.organization.listTeams({ orgId, page: { pageSize: 200, pageToken: '', orderBy: '' } }, silentCallOptions);
			return new Set(resp.teams.map((t) => t.id));
		} catch {
			return new Set();
		}
	}

	async function request() {
		if (selected.length === 0 || saving) return;
		saving = true;
		try {
			const teams = await teamIdSet();
			const teamIds = selected.filter((id) => teams.has(id));
			const userIds = selected.filter((id) => !teams.has(id));
			await rpcClient.pull.requestReviewers({ namespace, name, number, userIds, teamIds });
			toast.success('Review requested');
			selected = [];
			pickerOpen = false;
			onChanged();
		} catch {
			// Error interceptor already toasted
		} finally {
			saving = false;
		}
	}

	async function remove(userId: string, teamId: string) {
		const key = userId || teamId;
		if (removing) return;
		removing = key;
		try {
			await rpcClient.pull.removeReviewers({ namespace, name, number, userIds: userId ? [userId] : [], teamIds: teamId ? [teamId] : [] });
			onChanged();
		} catch {
			// Error interceptor already toasted
		} finally {
			removing = '';
		}
	}

	async function rerequest(userId: string) {
		if (rerequesting) return;
		rerequesting = userId;
		try {
			await rpcClient.pull.requestReviewers({ namespace, name, number, userIds: [userId], teamIds: [] });
			toast.success('Review re-requested');
			onChanged();
		} catch {
			// Error interceptor already toasted
		} finally {
			rerequesting = '';
		}
	}

	function openDismiss(r: Review) {
		dismissTarget = r;
		dismissReason = '';
		dismissOpen = true;
	}

	async function dismiss() {
		if (!dismissTarget || dismissing) return;
		dismissing = true;
		try {
			await rpcClient.pull.dismissReview({ namespace, name, number, reviewId: dismissTarget.id, message: dismissReason.trim() });
			toast.success('Review dismissed');
			dismissOpen = false;
			onChanged();
		} catch {
			// Error interceptor already toasted
		} finally {
			dismissing = false;
		}
	}

	function stateIcon(r: Review) {
		if (r.dismissed) return Ban;
		switch (r.state) {
			case ReviewState.APPROVED:
				return CheckCircle2;
			case ReviewState.CHANGES_REQUESTED:
				return XCircle;
			default:
				return MessageSquare;
		}
	}

	function stateColor(r: Review): string {
		if (r.dismissed) return 'text-muted-foreground';
		switch (r.state) {
			case ReviewState.APPROVED:
				return 'text-success';
			case ReviewState.CHANGES_REQUESTED:
				return 'text-destructive';
			default:
				return 'text-muted-foreground';
		}
	}
</script>

<div class="space-y-2">
	<div class="flex items-center justify-between">
		<span class="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">Reviewers</span>
		{#if canRequest}
			<Popover bind:open={pickerOpen}>
				<PopoverTrigger>
					{#snippet child({ props })}
						<Button {...props} variant="ghost" size="icon" class="h-6 w-6" title="Request reviewers">
							<Settings2 class="h-3.5 w-3.5 text-muted-foreground" />
						</Button>
					{/snippet}
				</PopoverTrigger>
				<PopoverContent class="w-[min(20rem,calc(100vw-2rem))] p-3 space-y-3" align="end">
					<FormField label="Request reviewers" bordered={false} help="Users or teams, requests notify them">
						<PrincipalPicker bind:selected {orgId} placeholder="Users or teams..." />
					</FormField>
					<div class="flex justify-end gap-2">
						<Button variant="outline" size="sm" class="h-8" onclick={() => (pickerOpen = false)}>Cancel</Button>
						<Button size="sm" class="h-8" onclick={request} disabled={saving || selected.length === 0}>
							{#if saving}<Loader2 class="h-3 w-3 animate-spin" />{/if}
							Request
						</Button>
					</div>
				</PopoverContent>
			</Popover>
		{/if}
	</div>

	{#if requests.length === 0 && reviews.length === 0}
		<p class="text-[12.5px] text-muted-foreground">No reviews yet</p>
	{:else}
		<ul class="space-y-1.5">
			{#each requests as rq (rq.teamId || rq.user?.id)}
				<li class="flex items-center gap-2 text-[13px] min-w-0">
					{#if rq.teamId}
						<Users class="h-3.5 w-3.5 text-muted-foreground shrink-0" />
						<span class="truncate">{rq.teamName || 'team'}</span>
					{:else}
						<Clock class="h-3.5 w-3.5 text-amber-500 shrink-0" />
						<a href={resolve('/[namespace]', { namespace: rq.user?.username ?? '' })} class="truncate hover:underline">{rq.user?.username ?? 'unknown'}</a>
					{/if}
					<Badge variant="outline" class="text-[10px] h-4.5 px-1.5 ml-auto border-amber-500/40 text-amber-600 dark:text-amber-400 shrink-0">
						{rq.fulfilled ? 'Reviewed' : 'Awaiting'}
					</Badge>
					{#if canRequest}
						<Button variant="ghost" size="icon" class="h-5 w-5 shrink-0" onclick={() => remove(rq.user?.id ?? '', rq.teamId)} disabled={removing !== ''} title="Remove request">
							<X class="h-3 w-3 text-muted-foreground" />
						</Button>
					{/if}
				</li>
			{/each}
			{#each reviews as r (r.id)}
				{@const Icon = stateIcon(r)}
				<li class="flex items-center gap-2 text-[13px] min-w-0">
					<Icon class="h-3.5 w-3.5 shrink-0 {stateColor(r)}" />
					<a href={resolve('/[namespace]', { namespace: r.reviewer?.username ?? '' })} class="truncate hover:underline">{r.reviewer?.username ?? 'unknown'}</a>
					<Badge variant="outline" class="text-[10px] h-4.5 px-1.5 ml-auto shrink-0 {reviewStateClass(r.state, r.dismissed)}">
						{r.dismissed ? 'Dismissed' : reviewStateLabel(r.state)}
					</Badge>
					{#if canRequest && r.reviewer}
						<Button variant="ghost" size="icon" class="h-5 w-5 shrink-0" onclick={() => rerequest(r.reviewer!.id)} disabled={rerequesting !== ''} title="Re-request review">
							<RotateCcw class="h-3 w-3 text-muted-foreground" />
						</Button>
					{/if}
					{#if canDismiss && !r.dismissed && r.state !== ReviewState.COMMENTED}
						<Button variant="ghost" size="icon" class="h-5 w-5 shrink-0" onclick={() => openDismiss(r)} title="Dismiss review">
							<Ban class="h-3 w-3 text-muted-foreground" />
						</Button>
					{/if}
				</li>
			{/each}
		</ul>
	{/if}
</div>

<FormPanel bind:open={dismissOpen} title="Dismiss review" description="The review keeps its comments but no longer counts toward merging." icon={Ban}>
	<FormField label="Reason" id="dismiss-reason" help="Shown on the timeline next to the dismissed review.">
		<Textarea id="dismiss-reason" bind:value={dismissReason} rows={3} placeholder="Why is this review no longer relevant?" />
	</FormField>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (dismissOpen = false)} disabled={dismissing}>Cancel</Button>
		<Button variant="destructive" onclick={dismiss} disabled={dismissing}>
			{#if dismissing}<Loader2 class="h-3.5 w-3.5 animate-spin" />{/if}
			Dismiss
		</Button>
	{/snippet}
</FormPanel>

<script lang="ts">
	import { resolve } from '$app/paths';
	import {
		Settings2,
		X,
		RotateCcw,
		Ban,
		Loader2,
		Users,
		Clock,
		CheckCircle2,
		XCircle,
		MessageSquare
	} from '@lucide/svelte';
	import { Popover, PopoverContent, PopoverTrigger } from '$lib/components/ui/popover';
	import { Button } from '$lib/components/ui/button';
	import { Badge, type BadgeVariant } from '$lib/components/ui/badge';
	import { Textarea } from '$lib/components/ui/textarea';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import PrincipalPicker from './principal-picker.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { ReviewState } from '$lib/proto/distroface/v1/pull_pb';
	import type { PullRequest, Review } from '$lib/proto/distroface/v1/pull_pb';
	import { latestReviews, reviewStateLabel } from '$lib/pulls';
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
	const requests = $derived(
		pull.reviewRequests.filter(
			(r) => r.teamId || !r.fulfilled || !reviewedIds.has(r.user?.id ?? '')
		)
	);

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
			const resp = await rpcClient.organization.listTeams(
				{ orgId, page: { pageSize: 200, pageToken: '', orderBy: '' } },
				silentCallOptions
			);
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
			await rpcClient.pull.removeReviewers({
				namespace,
				name,
				number,
				userIds: userId ? [userId] : [],
				teamIds: teamId ? [teamId] : []
			});
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
			await rpcClient.pull.requestReviewers({
				namespace,
				name,
				number,
				userIds: [userId],
				teamIds: []
			});
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
			await rpcClient.pull.dismissReview({
				namespace,
				name,
				number,
				reviewId: dismissTarget.id,
				message: dismissReason.trim()
			});
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

	function stateVariant(r: Review): BadgeVariant {
		if (r.dismissed) return 'outline';
		switch (r.state) {
			case ReviewState.APPROVED:
				return 'success';
			case ReviewState.CHANGES_REQUESTED:
				return 'destructive';
			case ReviewState.PENDING:
				return 'warning';
			default:
				return 'outline';
		}
	}
</script>

<div class="space-y-2">
	<div class="flex items-center justify-between">
		<span class="eyebrow">Reviewers</span>
		{#if canRequest}
			<Popover bind:open={pickerOpen}>
				<PopoverTrigger>
					{#snippet child({ props })}
						<Button
							{...props}
							variant="ghost-muted"
							size="icon-xs"
							title="Request reviewers"
							aria-label="Request reviewers"
						>
							<Settings2 />
						</Button>
					{/snippet}
				</PopoverTrigger>
				<PopoverContent class="w-[min(20rem,calc(100vw-2rem))] space-y-4" align="end">
					<FormField label="Request reviewers" help="Users or teams, requests notify them">
						<PrincipalPicker bind:selected {orgId} placeholder="Users or teams" />
					</FormField>
					<div class="flex justify-end gap-2">
						<Button variant="outline" onclick={() => (pickerOpen = false)}>Cancel</Button>
						<Button onclick={request} disabled={saving || selected.length === 0}>
							{#if saving}<Loader2 class="animate-spin" />{/if}
							Request
						</Button>
					</div>
				</PopoverContent>
			</Popover>
		{/if}
	</div>

	{#if requests.length === 0 && reviews.length === 0}
		<p class="text-sm text-muted-foreground">No reviews yet</p>
	{:else}
		<ul class="space-y-1.5">
			{#each requests as rq (rq.teamId || rq.user?.id)}
				<li class="flex min-w-0 items-center gap-2 text-sm">
					{#if rq.teamId}
						<Users class="size-3.5 shrink-0 text-muted-foreground" />
						<span class="truncate">{rq.teamName || 'team'}</span>
					{:else}
						<Clock class="size-3.5 shrink-0 text-warning" />
						<a
							href={resolve('/[namespace]', { namespace: rq.user?.username ?? '' })}
							class="truncate hover:underline">{rq.user?.username ?? 'unknown'}</a
						>
					{/if}
					<Badge variant={rq.fulfilled ? 'outline' : 'warning'} class="ml-auto shrink-0">
						{rq.fulfilled ? 'Reviewed' : 'Awaiting'}
					</Badge>
					{#if canRequest}
						<Button
							variant="ghost-muted"
							size="icon-xs"
							class="shrink-0"
							onclick={() => remove(rq.user?.id ?? '', rq.teamId)}
							disabled={removing !== ''}
							title="Remove request"
							aria-label="Remove request"
						>
							<X />
						</Button>
					{/if}
				</li>
			{/each}
			{#each reviews as r (r.id)}
				{@const Icon = stateIcon(r)}
				<li class="flex min-w-0 items-center gap-2 text-sm">
					<Icon class="size-3.5 shrink-0 {stateColor(r)}" />
					<a
						href={resolve('/[namespace]', { namespace: r.reviewer?.username ?? '' })}
						class="truncate hover:underline">{r.reviewer?.username ?? 'unknown'}</a
					>
					<Badge variant={stateVariant(r)} class="ml-auto shrink-0">
						{r.dismissed ? 'Dismissed' : reviewStateLabel(r.state)}
					</Badge>
					{#if canRequest && r.reviewer}
						<Button
							variant="ghost-muted"
							size="icon-xs"
							class="shrink-0"
							onclick={() => rerequest(r.reviewer!.id)}
							disabled={rerequesting !== ''}
							title="Re-request review"
							aria-label="Re-request review"
						>
							<RotateCcw />
						</Button>
					{/if}
					{#if canDismiss && !r.dismissed && r.state !== ReviewState.COMMENTED}
						<Button
							variant="ghost-muted"
							size="icon-xs"
							class="shrink-0"
							onclick={() => openDismiss(r)}
							title="Dismiss review"
							aria-label="Dismiss review"
						>
							<Ban />
						</Button>
					{/if}
				</li>
			{/each}
		</ul>
	{/if}
</div>

<ConfirmDialog
	bind:open={dismissOpen}
	title="Dismiss review"
	confirmLabel="Dismiss"
	onConfirm={dismiss}
	loading={dismissing}
	icon={Ban}
>
	{#snippet description()}
		The review keeps its comments but no longer counts toward merging.
	{/snippet}
	<FormField
		label="Reason"
		id="dismiss-reason"
		help="Shown on the timeline next to the dismissed review"
	>
		<Textarea
			id="dismiss-reason"
			bind:value={dismissReason}
			rows={3}
			placeholder="Why is this review no longer relevant?"
		/>
	</FormField>
</ConfirmDialog>

<script lang="ts">
	import {
		CheckCircle2, XCircle, Loader2, Clock, CircleDashed, Ban, CircleMinus, TimerOff, CircleDot, Circle, ShieldAlert
	} from '@lucide/svelte';
	import { ActionsConclusion, type ActionsStatus } from '$lib/proto/distroface/v1/actions_pb';
	import { runState, stateLabels } from './actions-state';
	import { cn } from '$lib/utils';

	let {
		status,
		conclusion = ActionsConclusion.UNSPECIFIED,
		needsApproval = false,
		class: className = 'h-4 w-4'
	}: {
		status: ActionsStatus;
		conclusion?: ActionsConclusion;
		needsApproval?: boolean;
		class?: string;
	} = $props();

	const state = $derived(runState(status, conclusion, needsApproval));
	const label = $derived(stateLabels[state]);
</script>

<span class="inline-flex shrink-0 items-center" title={label} aria-label={label} role="img" data-state={state}>
	{#if state === 'running'}
		<Loader2 class={cn('animate-spin text-amber-500', className)} />
	{:else if state === 'approval'}
		<ShieldAlert class={cn('text-amber-500', className)} />
	{:else if state === 'waiting'}
		<Clock class={cn('text-amber-500/80', className)} />
	{:else if state === 'queued'}
		<CircleDashed class={cn('text-muted-foreground', className)} />
	{:else if state === 'success'}
		<CheckCircle2 class={cn('text-success', className)} />
	{:else if state === 'failure'}
		<XCircle class={cn('text-destructive', className)} />
	{:else if state === 'cancelled'}
		<Ban class={cn('text-muted-foreground', className)} />
	{:else if state === 'skipped'}
		<CircleMinus class={cn('text-muted-foreground/70', className)} />
	{:else if state === 'timed_out'}
		<TimerOff class={cn('text-destructive', className)} />
	{:else if state === 'neutral'}
		<CircleDot class={cn('text-muted-foreground', className)} />
	{:else}
		<Circle class={cn('text-muted-foreground/50', className)} />
	{/if}
</span>

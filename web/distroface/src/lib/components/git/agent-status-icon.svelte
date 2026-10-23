<script lang="ts">
	import {
		CheckCircle2,
		XCircle,
		Loader2,
		CircleDashed,
		Ban,
		TimerOff,
		ShieldAlert,
		ShieldX,
		Gauge,
		Circle
	} from '@lucide/svelte';
	import {
		AgentSessionConclusion,
		type AgentSessionStatus
	} from '$lib/proto/distroface/v1/agents_pb';
	import { sessionState, sessionStateLabels } from './agents-state';
	import { cn } from '$lib/utils';

	let {
		status,
		conclusion = AgentSessionConclusion.UNSPECIFIED,
		class: className = 'size-4'
	}: {
		status: AgentSessionStatus;
		conclusion?: AgentSessionConclusion;
		class?: string;
	} = $props();

	const state = $derived(sessionState(status, conclusion));
	const label = $derived(sessionStateLabels[state]);
</script>

<span
	class="inline-flex shrink-0 items-center"
	title={label}
	aria-label={label}
	role="img"
	data-state={state}
>
	{#if state === 'running'}
		<Loader2 class={cn('animate-spin text-warning', className)} />
	{:else if state === 'approval'}
		<ShieldAlert class={cn('text-warning', className)} />
	{:else if state === 'queued'}
		<CircleDashed class={cn('text-muted-foreground', className)} />
	{:else if state === 'success'}
		<CheckCircle2 class={cn('text-success', className)} />
	{:else if state === 'failure'}
		<XCircle class={cn('text-destructive', className)} />
	{:else if state === 'cancelled'}
		<Ban class={cn('text-muted-foreground', className)} />
	{:else if state === 'timed_out'}
		<TimerOff class={cn('text-destructive', className)} />
	{:else if state === 'rejected'}
		<ShieldX class={cn('text-muted-foreground', className)} />
	{:else if state === 'budget'}
		<Gauge class={cn('text-warning', className)} />
	{:else}
		<Circle class={cn('text-muted-foreground', className)} />
	{/if}
</span>

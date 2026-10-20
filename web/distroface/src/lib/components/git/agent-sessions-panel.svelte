<script lang="ts">
	import { untrack } from 'svelte';
	import { resolve } from '$app/paths';
	import { Bot, Plus, Loader, ChevronDown } from '$lib/icons';
	import { Button } from '$lib/components/ui/button';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Popover, PopoverContent, PopoverTrigger } from '$lib/components/ui/popover';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import AgentStatusIcon from '$lib/components/git/agent-status-icon.svelte';
	import { rpcClient, silentCallOptions, backgroundCallOptions } from '$lib/api/rpc-client';
	import { configStore } from '$lib/stores/config.svelte';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import type {
		Agent,
		AgentSession,
		GetRepoAgentsResponse
	} from '$lib/proto/distroface/v1/agents_pb';
	import {
		isSessionActive,
		kindLabels,
		sessionState,
		sessionStateLabels,
		triggerLabel
	} from './agents-state';
	import { relativeTime } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { toast } from 'svelte-sonner';

	let {
		namespace,
		name,
		issueNumber,
		isPull = false,
		canStart = false,
		tick = 0
	}: {
		namespace: string;
		name: string;
		issueNumber: bigint;
		isPull?: boolean;
		canStart?: boolean;
		// Bumped by the page's background poll
		tick?: number;
	} = $props();

	let overview = $state<GetRepoAgentsResponse | undefined>();
	let sessions = $state<AgentSession[]>([]);
	let loaded = $state(false);
	let open = $state(false);
	let agentId = $state('');
	let prompt = $state('');
	let starting = $state(false);
	let seen = '';

	const unit = $derived(configStore.agentsEnabled && gitRepoStore.repo?.units?.agents === true);
	const enabled = $derived(unit && !!overview?.enabled);
	const agents = $derived((overview?.agents ?? []).filter((a) => a.enabled));
	const selected = $derived(agents.find((a) => a.id === agentId) ?? agents[0]);
	const anyActive = $derived(sessions.some((s) => isSessionActive(s.status)));

	async function load(silent = false) {
		if (!unit) {
			loaded = true;
			return;
		}
		try {
			const [o, s] = await Promise.all([
				overview
					? Promise.resolve(undefined)
					: rpcClient.agents.getRepoAgents({ namespace, name }, backgroundCallOptions),
				rpcClient.agents.listAgentSessions(
					{
						namespace,
						name,
						issueNumber,
						isPull,
						page: { pageSize: 10, pageToken: '', orderBy: '' }
					},
					silent ? backgroundCallOptions : silentCallOptions
				)
			]);
			if (o) overview = o;
			sessions = s.sessions;
			// Agent activity moves the counts in the repository header
			const sig = s.sessions
				.map((x) => `${x.id}:${x.status}:${x.eventCount}:${x.resultPullNumber}`)
				.join(',');
			if (silent && seen && sig !== seen) void gitRepoStore.refresh();
			seen = sig;
		} catch {
			// Unit may be off for this viewer
		} finally {
			loaded = true;
		}
	}

	// Only the target and the unit retrigger, loading must not track what it writes
	$effect(() => {
		void namespace;
		void name;
		void issueNumber;
		void unit;
		untrack(() => {
			overview = undefined;
			void load();
		});
	});

	$effect(() => {
		if (!anyActive) return;
		const t = setInterval(() => load(true), 5000);
		return () => clearInterval(t);
	});

	$effect(() => {
		if (tick > 0) untrack(() => void load(true));
	});

	async function start() {
		if (!selected || starting) return;
		starting = true;
		try {
			const resp = await rpcClient.agents.startAgentSession({
				namespace,
				name,
				agent: selected.id.startsWith('file:') ? selected.slug : selected.id,
				prompt: prompt.trim(),
				issueNumber,
				isPull
			});
			toast.success(`Session #${resp.session?.number} queued`);
			prompt = '';
			open = false;
			await load(true);
		} catch {
			// Error interceptor already toasted
		} finally {
			starting = false;
		}
	}

	function agentLabel(a: Agent): string {
		return `${a.name} (${kindLabels[a.kind] ?? 'Agent'})`;
	}
</script>

{#if loaded && enabled && (sessions.length > 0 || (canStart && agents.length > 0))}
	<section class="space-y-2 lg:border-t lg:pt-5">
		<div class="flex min-h-6 items-center justify-between">
			<span class="eyebrow inline-flex items-center gap-1.5"><Bot class="size-3.5" />Agents</span>
			{#if canStart && agents.length > 0 && overview?.canStart}
				<Popover bind:open>
					<PopoverTrigger>
						{#snippet child({ props })}
							<Button {...props} variant="ghost-muted" size="icon-xs" aria-label="Ask an agent">
								<Plus />
							</Button>
						{/snippet}
					</PopoverTrigger>
					<PopoverContent align="end" class="w-[min(22rem,calc(100vw-2rem))] space-y-3">
						<div class="space-y-1">
							<p class="text-sm font-medium">Ask an agent</p>
							<p class="text-xs text-muted-foreground">
								The agent reads this {isPull ? 'pull request' : 'issue'} and acts on your instruction.
							</p>
						</div>
						<Select type="single" value={selected?.id ?? ''} onValueChange={(v) => (agentId = v)}>
							<SelectTrigger class="w-full" aria-label="Agent">
								<span class="truncate">{selected ? agentLabel(selected) : 'Pick an agent'}</span>
								<ChevronDown class="ml-auto size-3.5 text-muted-foreground" />
							</SelectTrigger>
							<SelectContent>
								{#each agents as a (a.id)}
									<SelectItem value={a.id}>{agentLabel(a)}</SelectItem>
								{/each}
							</SelectContent>
						</Select>
						<Textarea
							bind:value={prompt}
							rows={3}
							placeholder={isPull
								? 'Review this pull request, or describe what to change'
								: 'Describe what the agent should do with this issue'}
						/>
						<div class="flex justify-end">
							<Button size="sm" onclick={start} disabled={starting || !selected}>
								{#if starting}<Loader class="animate-spin" />{:else}<Bot />{/if}
								Start session
							</Button>
						</div>
					</PopoverContent>
				</Popover>
			{/if}
		</div>
		{#if sessions.length === 0}
			<p class="text-xs text-muted-foreground">
				No agent sessions on this {isPull ? 'pull request' : 'issue'} yet.
			</p>
		{:else}
			<ul class="space-y-1.5">
				{#each sessions as s (s.id)}
					{@const state = sessionState(s.status, s.conclusion)}
					<li class="flex items-start gap-2 text-xs">
						<AgentStatusIcon status={s.status} conclusion={s.conclusion} class="mt-0.5 size-3.5" />
						<div class="min-w-0 flex-1">
							<a
								href={resolve('/[namespace]/[name]/agents/sessions/[id]', {
									namespace,
									name,
									id: s.id
								})}
								class="font-medium hover:text-primary">@{s.botUsername || s.agentSlug}</a
							>
							<span class="text-muted-foreground">
								{sessionStateLabels[state].toLowerCase()}, {triggerLabel(s.trigger).toLowerCase()}
								{#if s.createdAt}
									{relativeTime(timestampDate(s.createdAt))}
								{/if}
							</span>
							{#if s.resultPullNumber > 0n && !(isPull && s.resultPullNumber === issueNumber)}
								<a
									href={resolve('/[namespace]/[name]/pulls/[number]', {
										namespace,
										name,
										number: String(s.resultPullNumber)
									})}
									class="link">PR !{s.resultPullNumber}</a
								>
							{/if}
						</div>
					</li>
				{/each}
			</ul>
		{/if}
	</section>
{/if}

<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { Bot, GitBranch, Loader, FileCode } from '$lib/icons';
	import { toast } from 'svelte-sonner';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import { RadioGroup, RadioGroupItem } from '$lib/components/ui/radio-group';
	import EmptyState from '$lib/components/empty-state.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import { GitUnit } from '$lib/proto/distroface/v1/git_pb';
	import {
		AgentKind,
		AgentSource,
		type Agent,
		type GetRepoAgentsResponse
	} from '$lib/proto/distroface/v1/agents_pb';
	import {
		formatTokens,
		kindDescriptions,
		kindIcons,
		kindLabels
	} from '$lib/components/git/agents-state';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);
	const canWrite = $derived(!!repo && gitRepoStore.canWrite(GitUnit.AGENTS) && !repo.isArchived);
	const agentsHref = $derived(resolve('/[namespace]/[name]/agents', { namespace, name }));

	let overview = $state<GetRepoAgentsResponse | undefined>();
	let loading = $state(true);
	let branches = $state<string[]>([]);
	let agentKey = $state(page.url.searchParams.get('agent') ?? '');
	let prompt = $state(page.url.searchParams.get('prompt') ?? '');
	let ref = $state(page.url.searchParams.get('ref') ?? '');
	let issueNumber = $state(page.url.searchParams.get('issue') ?? '');
	let model = $state('');
	let submitting = $state(false);

	const agents = $derived((overview?.agents ?? []).filter((a) => a.enabled));
	const selected = $derived(
		agents.find((a) => a.slug === agentKey || a.id === agentKey) ?? agents[0]
	);
	const refOptions = $derived(ref && !branches.includes(ref) ? [ref, ...branches] : branches);
	const issueValid = $derived(issueNumber.trim() === '' || /^\d+$/.test(issueNumber.trim()));
	const needsPrompt = $derived(
		!issueNumber.trim() &&
			(selected?.kind === AgentKind.CODE ||
				selected?.kind === AgentKind.CHAT ||
				selected?.kind === AgentKind.CUSTOM)
	);
	const canSubmit = $derived(
		!!selected && issueValid && (!needsPrompt || prompt.trim() !== '') && !submitting
	);

	async function load() {
		loading = true;
		try {
			const [o, b] = await Promise.all([
				rpcClient.agents.getRepoAgents({ namespace, name }, silentCallOptions),
				rpcClient.gitRefs
					.listBranches(
						{ namespace, name, page: { pageSize: 200, pageToken: '', orderBy: '' } },
						silentCallOptions
					)
					.catch(() => undefined)
			]);
			overview = o;
			branches = b?.branches.map((x) => x.name) ?? [];
			if (!ref) ref = o.defaultBranch || repo?.defaultBranch || '';
		} catch {
			overview = undefined;
		} finally {
			loading = false;
		}
	}

	onMount(load);

	async function submit() {
		if (!canSubmit || !selected) return;
		submitting = true;
		try {
			const resp = await rpcClient.agents.startAgentSession({
				namespace,
				name,
				agent: selected.source === AgentSource.FILE ? selected.slug : selected.id,
				prompt: prompt.trim(),
				ref: ref.trim(),
				issueNumber: issueNumber.trim() ? BigInt(issueNumber.trim()) : 0n,
				model: model.trim()
			});
			toast.success(`Session #${resp.session?.number} queued`);
			await goto(
				resolve('/[namespace]/[name]/agents/sessions/[id]', {
					namespace,
					name,
					id: resp.session?.id ?? ''
				})
			);
		} catch {
			// Error interceptor already toasted
		} finally {
			submitting = false;
		}
	}

	// Effective caps, unset ones are left out
	const limitSummary = $derived.by(() => {
		if (!overview) return '';
		const parts: string[] = [];
		if (overview.maxTurns > 0) parts.push(`${overview.maxTurns} model turns`);
		if (overview.tokenBudget > 0n) parts.push(`${formatTokens(overview.tokenBudget)} tokens`);
		if (overview.maxSessionMinutes > 0) parts.push(`${overview.maxSessionMinutes} minutes`);
		return parts.length ? `Limits per session: ${parts.join(', ')}.` : 'No session limits.';
	});

	function placeholder(a?: Agent): string {
		switch (a?.kind) {
			case AgentKind.REVIEW:
				return 'Optional focus, for example: concentrate on the authentication changes';
			case AgentKind.CODE:
				return 'Describe the change to implement. Reference files, functions, and acceptance criteria.';
			case AgentKind.TRIAGE:
				return 'Optional guidance, for example: apply area labels and flag duplicates';
			default:
				return 'What should the agent do?';
		}
	}
</script>

{#if !repo || loading}
	<Skeleton class="h-96 w-full rounded-lg" />
{:else if !canWrite || !overview?.canStart || !configStore.agentsEnabled || !overview.enabled}
	<EmptyState
		icon={Bot}
		message="Cannot start a session"
		description={!configStore.agentsEnabled || !overview?.enabled
			? 'Agents are disabled by policy.'
			: 'Write access to the agents unit is required to start sessions.'}
	>
		{#snippet actions()}
			<Button variant="outline" href={agentsHref}>Back to agents</Button>
		{/snippet}
	</EmptyState>
{:else if agents.length === 0}
	<EmptyState
		icon={Bot}
		message="No agents available"
		description="Define an agent in the repository, organization, or instance settings first."
	>
		{#snippet actions()}
			<Button variant="outline" href={agentsHref}>Back to agents</Button>
			{#if gitRepoStore.canAdmin}
				<Button
					href={resolve('/[namespace]/[name]/settings/agents/definitions', { namespace, name })}
					>Define an agent</Button
				>
			{/if}
		{/snippet}
	</EmptyState>
{:else}
	<FormPage
		title="New agent task"
		description="Pick an agent and tell it what to do. The session runs on the server through the configured connector."
		backHref={agentsHref}
		backLabel="All sessions"
	>
		<FormSection
			title="Agent"
			description="Each agent has its own toolset, triggers, and approval policy."
		>
			<RadioGroup
				value={selected?.id ?? ''}
				onValueChange={(v) => (agentKey = v)}
				class="grid gap-2 sm:grid-cols-2"
			>
				{#each agents as a (a.id)}
					{@const Icon = kindIcons[a.kind]}
					<label
						class="flex cursor-pointer items-start gap-3 rounded-md border px-3 py-2.5 transition-colors hover:bg-muted/40 has-[[data-state=checked]]:border-primary has-[[data-state=checked]]:bg-primary/5"
					>
						<RadioGroupItem value={a.id} class="mt-1" />
						{#if Icon}<Icon class="mt-1 size-4 shrink-0 text-muted-foreground" />{/if}
						<span class="min-w-0 flex-1">
							<span class="flex flex-wrap items-center gap-2 text-sm font-medium">
								{a.name}
								<Badge variant="secondary">{kindLabels[a.kind]}</Badge>
								{#if a.source === AgentSource.FILE}<FileCode
										class="size-3.5 text-muted-foreground"
									/>{/if}
							</span>
							<span class="mt-0.5 block text-xs text-muted-foreground">
								{a.description || kindDescriptions[a.kind]}
							</span>
							<span class="mt-0.5 block font-mono text-2xs text-muted-foreground">
								@{a.botUsername || a.slug}{a.model ? `, ${a.model}` : ''}
							</span>
						</span>
					</label>
				{/each}
			</RadioGroup>
		</FormSection>

		<FormSection
			title="Task"
			description="Target an issue or pull request, or start from a branch."
		>
			<FormField
				label="Instruction"
				id="task-prompt"
				required={needsPrompt}
				help={selected?.kind === AgentKind.REVIEW && issueNumber.trim()
					? 'Blank reviews the pull request with the default instructions'
					: undefined}
			>
				<Textarea
					id="task-prompt"
					bind:value={prompt}
					rows={6}
					placeholder={placeholder(selected)}
				/>
			</FormField>
			<FormField
				label="Issue or pull request"
				id="task-issue"
				help="Number of the issue or pull request to work on, blank for a free standing task"
				error={!issueValid ? 'Enter a number' : undefined}
				span="half"
			>
				<Input
					id="task-issue"
					bind:value={issueNumber}
					placeholder="42"
					inputmode="numeric"
					class="font-mono"
					aria-invalid={!issueValid}
				/>
			</FormField>
			<FormField
				label="Branch"
				id="task-ref"
				help="Branch to read from and cut the working branch off, ignored for pull request targets"
				span="half"
			>
				<Select type="single" value={ref} onValueChange={(v) => (ref = v)}>
					<SelectTrigger id="task-ref" class="w-full">
						<span class="inline-flex items-center gap-2 truncate font-mono">
							<GitBranch class="size-3.5 shrink-0 text-muted-foreground" />
							{ref || 'Default branch'}
						</span>
					</SelectTrigger>
					<SelectContent>
						{#each refOptions as b (b)}
							<SelectItem value={b}>{b}</SelectItem>
						{/each}
					</SelectContent>
				</Select>
			</FormField>
			<FormField
				label="Model override"
				id="task-model"
				help="Blank uses the agent's model, must be allowed by the connector"
				span="half"
			>
				<Input
					id="task-model"
					bind:value={model}
					placeholder={selected?.model || 'agent default'}
					class="font-mono"
					autocomplete="off"
				/>
			</FormField>
		</FormSection>

		{#if overview}
			<p class="text-xs text-muted-foreground">
				{limitSummary}
				{#if overview.maxSessionsPerDay > 0}
					{overview.sessionsToday} of {overview.maxSessionsPerDay} sessions used today.
				{/if}
				{#if overview.allowCommands}Commands run on workflow runners.{:else}Commands are disabled.{/if}
			</p>
		{/if}

		{#snippet footer()}
			<Button onclick={submit} disabled={!canSubmit}>
				{#if submitting}<Loader class="animate-spin" />{:else}<Bot />{/if}
				{submitting ? 'Starting' : 'Start session'}
			</Button>
			<Button variant="outline" href={agentsHref}>Cancel</Button>
		{/snippet}
	</FormPage>
{/if}

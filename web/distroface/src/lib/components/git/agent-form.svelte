<script lang="ts">
	import { untrack } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Switch } from '$lib/components/ui/switch';
	import { Checkbox } from '$lib/components/ui/checkbox';
	import { Badge } from '$lib/components/ui/badge';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import * as RadioGroup from '$lib/components/ui/radio-group';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import FormActions from '$lib/components/form-actions.svelte';
	import LimitInput from '$lib/components/limit-input.svelte';
	import PrincipalPicker from '$lib/components/git/principal-picker.svelte';
	import { Loader, Undo } from '$lib/icons';
	import { rpcClient } from '$lib/api/rpc-client';
	import {
		AgentApprovalPolicy,
		AgentKind,
		type Agent,
		type AgentProvider,
		type AgentSpec
	} from '$lib/proto/distroface/v1/agents_pb';
	import {
		approvalDescriptions,
		approvalLabels,
		approvalOptions,
		defaultTools,
		kindDescriptions,
		kindIcons,
		kindLabels,
		kindOptions,
		kindTriggers,
		opensPullRequests,
		slugPattern,
		slugify,
		toolGroups,
		triggerFlags,
		type TriggerFlag
	} from './agents-state';
	import { toast } from 'svelte-sonner';

	let {
		scope,
		agent,
		providers = [],
		orgId = '',
		allowCommands = true,
		onSaved,
		cancelHref
	}: {
		scope: { kind: string; id: string };
		agent?: Agent;
		providers?: AgentProvider[];
		orgId?: string;
		allowCommands?: boolean;
		onSaved: (a: Agent) => void;
		cancelHref: string;
	} = $props();

	const initial = untrack(() => agent);
	const editing = !!initial;

	const positive = (v: number | bigint | undefined) => (v && Number(v) > 0 ? Number(v) : undefined);
	const whole = (v: number | null | undefined) => Math.max(0, Math.round(Number(v) || 0));

	function presetFlags(kind: AgentKind): Record<TriggerFlag, boolean> {
		const on = kindTriggers[kind] ?? [];
		return Object.fromEntries(triggerFlags.map((f) => [f, on.includes(f)])) as Record<
			TriggerFlag,
			boolean
		>;
	}

	let name = $state(initial?.name ?? '');
	let slug = $state(initial?.slug ?? '');
	let slugTouched = $state(!!initial);
	let description = $state(initial?.description ?? '');
	let kind = $state<AgentKind>(initial?.kind ?? AgentKind.REVIEW);
	let providerId = $state(initial?.providerId ?? '');
	let model = $state(initial?.model ?? '');
	let instructions = $state(initial?.instructions ?? '');
	let customTools = $state((initial?.tools.length ?? 0) > 0);
	let tools = $state<string[]>([...(initial?.tools ?? [])]);
	let approval = $state<AgentApprovalPolicy>(initial?.approval || AgentApprovalPolicy.COMMITS);
	let maxTurns = $state<number | undefined>(positive(initial?.maxTurns));
	let maxTokens = $state<number | undefined>(positive(initial?.maxTokens));
	let concurrency = $state<number | undefined>(positive(initial?.concurrency));
	let temperature = $state<number | undefined>(initial?.temperature);
	let enabled = $state(initial?.enabled ?? true);
	let allowedCallers = $state<string[]>([...(initial?.allowedCallerIds ?? [])]);

	const t = initial?.triggers;
	let flags = $state<Record<TriggerFlag, boolean>>(
		t
			? (Object.fromEntries(triggerFlags.map((f) => [f, t[f]])) as Record<TriggerFlag, boolean>)
			: presetFlags(untrack(() => kind))
	);
	let labels = $state((t?.issueLabels ?? []).join(', '));
	let baseBranches = $state((t?.baseBranches ?? []).join(', '));
	let paths = $state((t?.paths ?? []).join(', '));
	let pathsIgnore = $state((t?.pathsIgnore ?? []).join(', '));

	let saving = $state(false);

	$effect(() => {
		if (!slugTouched) slug = slugify(name);
	});

	const slugValid = $derived(slugPattern.test(slug));
	const nameValid = $derived(name.trim().length > 0);
	const handle = $derived(slug || 'handle');
	const kindLabel = $derived(kindLabels[kind] ?? 'Agent');
	const kindTools = $derived(defaultTools[kind] ?? []);
	const effectiveTools = $derived(customTools ? tools : kindTools);
	const selectedProvider = $derived(providers.find((p) => p.id === providerId));
	const providerModels = $derived(selectedProvider?.models ?? []);
	const ownPullRequests = $derived(opensPullRequests(kind));
	const presetApplied = $derived.by(() => {
		const preset = presetFlags(kind);
		return triggerFlags.every((f) => flags[f] === preset[f]);
	});

	function pickKind(v: string) {
		const next = Number(v) as AgentKind;
		const wasPreset = presetApplied;
		kind = next;
		if (wasPreset) flags = presetFlags(next);
		if (customTools && tools.length === 0) tools = [...kindTools];
	}

	function list(s: string): string[] {
		return s
			.split(',')
			.map((x) => x.trim())
			.filter(Boolean);
	}

	function toggleTool(name: string) {
		tools = tools.includes(name) ? tools.filter((x) => x !== name) : [...tools, name];
	}

	// Whether the chosen approval policy pauses a tool
	function gated(tool: { write?: boolean; commit?: boolean }): boolean {
		switch (approval) {
			case AgentApprovalPolicy.ALL_WRITES:
				return !!(tool.write || tool.commit);
			case AgentApprovalPolicy.COMMITS:
				return !!tool.commit;
			default:
				return false;
		}
	}
	const toolsHelp = $derived(
		approval === AgentApprovalPolicy.NONE
			? 'Every tool runs without approval under the never pause policy.'
			: 'Gated tools wait for approval before they run.'
	);

	function spec(): AgentSpec {
		return {
			$typeName: 'distroface.v1.AgentSpec',
			slug: slug.trim(),
			name: name.trim(),
			description: description.trim(),
			kind,
			providerId,
			model: model.trim(),
			instructions,
			tools: customTools ? tools : [],
			triggers: {
				$typeName: 'distroface.v1.AgentTriggers',
				...flags,
				issueLabels: list(labels),
				baseBranches: list(baseBranches),
				paths: list(paths),
				pathsIgnore: list(pathsIgnore)
			},
			maxTurns: whole(maxTurns),
			maxTokens: BigInt(whole(maxTokens)),
			temperature: temperature === undefined ? undefined : Number(temperature),
			approval,
			enabled,
			concurrency: whole(concurrency),
			allowedCallerIds: allowedCallers
		};
	}

	async function save() {
		if (!nameValid || !slugValid || saving) return;
		saving = true;
		try {
			const resp = editing
				? await rpcClient.agents.updateAgent({ scope, id: initial!.id, spec: spec() })
				: await rpcClient.agents.createAgent({ scope, spec: spec() });
			toast.success(editing ? 'Agent updated' : 'Agent created');
			if (resp.agent) onSaved(resp.agent);
		} catch {
			// Error interceptor already toasted
		} finally {
			saving = false;
		}
	}
</script>

{#snippet kindMark(k: AgentKind)}
	{@const Icon = kindIcons[k]}
	<Icon class="mt-0.5 size-4 shrink-0 text-muted-foreground" />
{/snippet}

<FormSection title="Identity" description="The handle is the bot username and the mention.">
	<FormField label="Name" id="agent-name" required span="half">
		<Input id="agent-name" bind:value={name} placeholder="Code reviewer" autocomplete="off" />
	</FormField>
	<FormField
		label="Handle"
		id="agent-slug"
		required
		span="half"
		error={slug && !slugValid ? '3 to 40 lower case letters, digits, or hyphens' : undefined}
	>
		<Input
			id="agent-slug"
			bind:value={slug}
			oninput={() => (slugTouched = true)}
			placeholder="reviewer"
			class="font-mono"
			autocomplete="off"
			aria-invalid={!!slug && !slugValid}
		/>
	</FormField>
	<FormField label="Description" id="agent-description">
		<Input
			id="agent-description"
			bind:value={description}
			placeholder="Reviews pull requests for correctness"
			autocomplete="off"
		/>
	</FormField>
</FormSection>

<FormSection
	title="Kind"
	description="Sets the prompt, the default tools, and the default triggers."
>
	<RadioGroup.Root
		value={String(kind)}
		onValueChange={pickKind}
		class="gap-0 divide-y rounded-lg border"
		aria-label="Agent kind"
	>
		{#each kindOptions as k (k)}
			<label class="flex cursor-pointer items-start gap-3 px-4 py-3" for="agent-kind-{k}">
				<RadioGroup.Item value={String(k)} id="agent-kind-{k}" class="mt-0.5" />
				{@render kindMark(k)}
				<span class="min-w-0 flex-1">
					<span class="block text-sm font-medium">{kindLabels[k]}</span>
					<span class="block text-xs text-muted-foreground">{kindDescriptions[k]}</span>
				</span>
				<span class="hidden text-xs text-muted-foreground tabular-nums sm:block"
					>{(defaultTools[k] ?? []).length} tools</span
				>
			</label>
		{/each}
	</RadioGroup.Root>
</FormSection>

<FormSection title="Model" description="Blank fields fall back to the scope default connector.">
	<FormField label="Connector" id="agent-provider" span="half">
		<Select
			type="single"
			value={providerId}
			onValueChange={(v) => (providerId = v === '__default' ? '' : v)}
		>
			<SelectTrigger id="agent-provider" class="w-full">
				{selectedProvider ? selectedProvider.name : 'Scope default'}
			</SelectTrigger>
			<SelectContent>
				<SelectItem value="__default">Scope default</SelectItem>
				{#each providers as p (p.id)}
					<SelectItem value={p.id} disabled={!p.enabled}>
						{p.name}{p.inherited ? ' (inherited)' : ''}{p.enabled ? '' : ' (disabled)'}
					</SelectItem>
				{/each}
			</SelectContent>
		</Select>
	</FormField>
	<FormField label="Model" id="agent-model" span="half">
		<Input
			id="agent-model"
			bind:value={model}
			placeholder={selectedProvider?.defaultModel || 'Connector default'}
			class="font-mono"
			autocomplete="off"
			list={providerModels.length ? 'agent-model-options' : undefined}
		/>
		{#if providerModels.length}
			<datalist id="agent-model-options">
				{#each providerModels as m (m)}<option value={m}></option>{/each}
			</datalist>
		{/if}
	</FormField>
	<FormField label="Temperature" id="agent-temperature" layout="inline">
		<LimitInput
			id="agent-temperature"
			label="temperature"
			bind:value={temperature}
			seed={0.2}
			allowZero
			min={0}
			max={2}
			step={0.1}
		/>
	</FormField>
	<FormField
		label="Instructions"
		id="agent-instructions"
		help="Added to the built in {kindLabel.toLowerCase()} prompt, AGENTS.md files are read automatically"
	>
		<Textarea
			id="agent-instructions"
			bind:value={instructions}
			rows={8}
			class="font-mono text-xs"
			placeholder="Focus on security and correctness. Follow CONTRIBUTING.md."
			spellcheck={false}
		/>
	</FormField>
</FormSection>

<FormSection title="Tools" description={toolsHelp}>
	<FormField
		label="Custom toolset"
		layout="toggle"
		help="Off uses the {kindLabel.toLowerCase()} defaults"
	>
		<Switch
			bind:checked={customTools}
			onCheckedChange={(v) => {
				if (v && tools.length === 0) tools = [...kindTools];
			}}
		/>
	</FormField>
	{#if customTools}
		<div class="form-subfields">
			<div class="grid gap-4 sm:grid-cols-3">
				{#each toolGroups as group (group.label)}
					<div class="space-y-2">
						<p class="eyebrow">{group.label}</p>
						{#each group.tools as tool (tool.name)}
							<label class="flex cursor-pointer items-start gap-2 text-sm">
								<Checkbox
									checked={tools.includes(tool.name)}
									onCheckedChange={() => toggleTool(tool.name)}
									class="mt-0.5"
								/>
								<span class="min-w-0">
									<span class="font-mono text-xs">{tool.name}</span>
									{#if gated(tool)}<Badge variant="warning" class="ml-1">gated</Badge>{/if}
									<span class="block text-xs text-muted-foreground">{tool.description}</span>
								</span>
							</label>
						{/each}
					</div>
				{/each}
			</div>
		</div>
	{:else}
		<div class="flex items-start gap-3">
			{@render kindMark(kind)}
			<div class="flex flex-wrap gap-1">
				{#each effectiveTools as name (name)}
					<Badge variant="outline" class="font-mono">{name}</Badge>
				{/each}
			</div>
		</div>
	{/if}
	{#if effectiveTools.includes('run_command') && !allowCommands}
		<p class="text-xs text-warning">Commands are disabled by policy, run_command is withheld.</p>
	{/if}
</FormSection>

<FormSection title="Triggers" description="Events that start a session.">
	<p class="eyebrow">Pull requests</p>
	<FormField label="Opened" layout="toggle">
		<Switch bind:checked={flags.pullRequestOpened} />
	</FormField>
	<FormField label="Updated" layout="toggle" help="New commits on the head branch">
		<Switch bind:checked={flags.pullRequestSynchronize} />
	</FormField>
	<FormField label="Ready for review" layout="toggle">
		<Switch bind:checked={flags.pullRequestReadyForReview} />
	</FormField>
	<FormField label="Review requested from @{handle}" layout="toggle">
		<Switch bind:checked={flags.reviewRequested} />
	</FormField>
	<FormField label="Include drafts" layout="toggle">
		<Switch bind:checked={flags.includeDrafts} />
	</FormField>

	<p class="eyebrow pt-2">Issues and comments</p>
	<FormField label="Assigned to @{handle}" layout="toggle">
		<Switch bind:checked={flags.issueAssigned} />
	</FormField>
	<FormField label="Mentioned in a comment" layout="toggle">
		<Switch bind:checked={flags.mention} />
	</FormField>
	<FormField label="Labels" id="agent-labels" span="half" help="Comma separated">
		<Input
			id="agent-labels"
			bind:value={labels}
			placeholder="agent, needs-triage"
			autocomplete="off"
		/>
	</FormField>

	{#if ownPullRequests}
		<p class="eyebrow pt-2">Own pull requests</p>
		<FormField label="Review feedback" layout="toggle">
			<Switch bind:checked={flags.reviewFeedback} />
		</FormField>
		<FormField
			label="Failed workflow"
			layout="toggle"
			span="half"
			help="Up to three repair attempts"
		>
			<Switch bind:checked={flags.workflowFailure} />
		</FormField>
	{/if}

	<p class="eyebrow pt-2">Filters</p>
	<FormField label="Base branches" id="agent-branches" span="half" help="Globs, blank matches all">
		<Input
			id="agent-branches"
			bind:value={baseBranches}
			placeholder="main, release/*"
			class="font-mono"
			autocomplete="off"
		/>
	</FormField>
	<FormField label="Paths" id="agent-paths" span="half" help="Pull requests touching these globs">
		<Input
			id="agent-paths"
			bind:value={paths}
			placeholder="src/**, api/**"
			class="font-mono"
			autocomplete="off"
		/>
	</FormField>
	<FormField label="Ignored paths" id="agent-paths-ignore" span="half">
		<Input
			id="agent-paths-ignore"
			bind:value={pathsIgnore}
			placeholder="docs/**, **/*.md"
			class="font-mono"
			autocomplete="off"
		/>
	</FormField>
	<FormField
		label="Respond to other agents"
		layout="toggle"
		newRow
		help="Off ignores events raised by agent accounts"
	>
		<Switch bind:checked={flags.respondToBots} />
	</FormField>
	{#snippet footer()}
		<Button
			variant="ghost-muted"
			size="sm"
			disabled={presetApplied}
			onclick={() => (flags = presetFlags(kind))}
		>
			<Undo />Reset to {kindLabel.toLowerCase()} defaults
		</Button>
	{/snippet}
</FormSection>

<FormSection title="Approval" description="Paused sessions wait for someone with write access.">
	<RadioGroup.Root
		value={String(approval)}
		onValueChange={(v) => (approval = Number(v) as AgentApprovalPolicy)}
		class="gap-0 divide-y rounded-lg border"
		aria-label="Approval policy"
	>
		{#each approvalOptions as a (a)}
			<label class="flex cursor-pointer items-start gap-3 px-4 py-3" for="agent-approval-{a}">
				<RadioGroup.Item value={String(a)} id="agent-approval-{a}" class="mt-0.5" />
				<span class="min-w-0">
					<span class="block text-sm font-medium">{approvalLabels[a]}</span>
					<span class="block text-xs text-muted-foreground">{approvalDescriptions[a]}</span>
				</span>
			</label>
		{/each}
	</RadioGroup.Root>
	{#if ownPullRequests}
		<p class="text-xs text-muted-foreground">
			Sessions commit to agents/{handle}/N and open a pull request into the target branch.
		</p>
	{/if}
</FormSection>

<FormSection title="Limits" description="Tighter than the policy caps, off inherits them.">
	<FormField label="Model turns" id="agent-turns" layout="inline">
		<LimitInput id="agent-turns" label="model turns" unit="turns" bind:value={maxTurns} seed={40} />
	</FormField>
	<FormField label="Token budget" id="agent-tokens" layout="inline" help="Per session">
		<LimitInput
			id="agent-tokens"
			label="token budget"
			unit="tokens"
			bind:value={maxTokens}
			seed={200000}
		/>
	</FormField>
	<FormField
		label="Concurrent sessions"
		id="agent-concurrency"
		layout="inline"
		help="Per repository"
	>
		<LimitInput
			id="agent-concurrency"
			label="concurrent sessions"
			unit="sessions"
			bind:value={concurrency}
			seed={1}
		/>
	</FormField>
</FormSection>

<FormSection title="Access" description="Who may start sessions and whether triggers fire.">
	<FormField label="Allowed callers" help="Blank allows anyone with write access">
		<PrincipalPicker
			bind:selected={allowedCallers}
			{orgId}
			placeholder="Everyone with write access"
		/>
	</FormField>
	<FormField label="Enabled" layout="toggle" help="Off ignores triggers and blocks manual starts">
		<Switch bind:checked={enabled} />
	</FormField>
</FormSection>

<FormActions>
	<Button onclick={save} disabled={saving || !nameValid || !slugValid}>
		{#if saving}<Loader class="animate-spin" />{/if}
		{saving ? 'Saving' : editing ? 'Save changes' : 'Create agent'}
	</Button>
	<Button variant="ghost-muted" href={cancelHref}>Cancel</Button>
</FormActions>

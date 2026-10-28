<script lang="ts">
	import { onMount } from 'svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { toast } from 'svelte-sonner';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Switch } from '$lib/components/ui/switch';
	import { Input } from '$lib/components/ui/input';
	import LimitInput from '$lib/components/limit-input.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import FormActions from '$lib/components/form-actions.svelte';
	import { Save, Undo2, Loader2 } from '@lucide/svelte';
	import { errText } from '$lib/act.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import {
		isLocked,
		patchSettings,
		tierOf,
		type ScopeInit,
		type SettingsPatch
	} from '$lib/settings-utils';
	import {
		SettingsScopeType,
		SettingsTier,
		type FieldProvenance,
		type Settings
	} from '$lib/proto/distroface/v1/settings_pb';

	let {
		scope,
		parentScope,
		canEdit = true
	}: {
		scope: ScopeInit;
		// One tier up enables custom tags and reset
		parentScope?: ScopeInit;
		canEdit?: boolean;
	} = $props();

	const P = {
		enabled: 'git.agents.enabled',
		allowCommands: 'git.agents.allow_commands',
		runsOn: 'git.agents.commands_runs_on',
		commandTimeout: 'git.agents.command_timeout_minutes',
		maxTurns: 'git.agents.max_turns',
		maxMinutes: 'git.agents.max_session_minutes',
		perDay: 'git.agents.max_sessions_per_day',
		concurrent: 'git.agents.max_concurrent_sessions',
		tokens: 'git.agents.token_budget_per_session',
		retention: 'git.agents.log_retention_days',
		privateNetworks: 'git.agents.allow_private_networks',
		debugToolCalls: 'git.agents.debug_tool_calls',
		repoProviders: 'git.agents.allow_repo_providers',
		repoAgents: 'git.agents.allow_repo_agents',
		fileAgents: 'git.agents.allow_file_agents'
	} as const;
	type Path = (typeof P)[keyof typeof P];

	const isSystem = $derived(scope.type === SettingsScopeType.SYSTEM);
	const isOrg = $derived(scope.type === SettingsScopeType.ORG);
	const isRepo = $derived(scope.type === SettingsScopeType.REPO);
	const ownTier = $derived(
		isOrg ? SettingsTier.ORG : isRepo ? SettingsTier.REPO : SettingsTier.SYSTEM
	);

	// Paths this tier may store
	const paths = $derived.by((): Path[] => {
		const limits: Path[] = [
			P.allowCommands,
			P.runsOn,
			P.commandTimeout,
			P.maxTurns,
			P.maxMinutes,
			P.perDay,
			P.concurrent,
			P.tokens,
			P.retention,
			P.fileAgents
		];
		if (isRepo) return limits;
		const org: Path[] = [P.enabled, P.repoProviders, P.repoAgents, ...limits];
		if (isOrg) return org;
		return [P.privateNetworks, P.debugToolCalls, ...org];
	});

	let loading = $state(true);
	let saving = $state(false);
	let resetting = $state(false);
	let prov = $state<FieldProvenance[]>([]);
	let inherited = $state<Settings | null>(null);

	let enabled = $state(false);
	let allowCommands = $state(false);
	let runsOn = $state('');
	let commandTimeout = $state<number | undefined>();
	let maxTurns = $state<number | undefined>();
	let maxMinutes = $state<number | undefined>();
	let perDay = $state<number | undefined>();
	let concurrent = $state<number | undefined>();
	let tokens = $state<number | undefined>();
	let retention = $state<number | undefined>();
	let privateNetworks = $state(true);
	let debugToolCalls = $state(false);
	let repoProviders = $state(true);
	let repoAgents = $state(true);
	let fileAgents = $state(true);

	const overridden = (path: string) => !!parentScope && tierOf(prov, path) === ownTier;
	const tag = (path: string) => (overridden(path) ? 'Custom' : undefined);
	const locked = (path: string) => isLocked(prov, path);
	const overrideCount = $derived(paths.filter(overridden).length);
	const whole = (v: number | null | undefined) => Math.max(0, Math.round(Number(v) || 0));
	const limitPaths: Path[] = [
		P.commandTimeout,
		P.maxTurns,
		P.maxMinutes,
		P.perDay,
		P.concurrent,
		P.tokens,
		P.retention
	];

	// Lower tiers only show a limit they set themselves
	function limit(path: Path, v: number | bigint | undefined): number | undefined {
		const n = Number(v ?? 0);
		if (n <= 0) return undefined;
		return parentScope && !overridden(path) ? undefined : n;
	}

	// Inherited limit while this scope sets none
	function fallback(path: Path): number | undefined {
		if (!parentScope) return undefined;
		const v = Number(valuesOf(inherited)[path] ?? 0);
		return v > 0 ? v : undefined;
	}

	function seed(s: Settings | null) {
		const a = s?.git?.agents;
		enabled = a?.enabled ?? false;
		allowCommands = a?.allowCommands ?? false;
		runsOn = a?.commandsRunsOn ?? '';
		commandTimeout = limit(P.commandTimeout, a?.commandTimeoutMinutes);
		maxTurns = limit(P.maxTurns, a?.maxTurns);
		maxMinutes = limit(P.maxMinutes, a?.maxSessionMinutes);
		perDay = limit(P.perDay, a?.maxSessionsPerDay);
		concurrent = limit(P.concurrent, a?.maxConcurrentSessions);
		tokens = limit(P.tokens, a?.tokenBudgetPerSession);
		retention = limit(P.retention, a?.logRetentionDays);
		privateNetworks = a?.allowPrivateNetworks ?? true;
		debugToolCalls = a?.debugToolCalls ?? false;
		repoProviders = a?.allowRepoProviders ?? true;
		repoAgents = a?.allowRepoAgents ?? true;
		fileAgents = a?.allowFileAgents ?? true;
	}

	function valuesOf(s: Settings | null): Record<Path, unknown> {
		const a = s?.git?.agents;
		return {
			[P.enabled]: a?.enabled ?? false,
			[P.allowCommands]: a?.allowCommands ?? false,
			[P.runsOn]: a?.commandsRunsOn ?? '',
			[P.commandTimeout]: a?.commandTimeoutMinutes ?? 20,
			[P.maxTurns]: a?.maxTurns ?? 40,
			[P.maxMinutes]: a?.maxSessionMinutes ?? 0,
			[P.perDay]: a?.maxSessionsPerDay ?? 0,
			[P.concurrent]: a?.maxConcurrentSessions ?? 2,
			[P.tokens]: Number(a?.tokenBudgetPerSession ?? 0n),
			[P.retention]: a?.logRetentionDays ?? 90,
			[P.privateNetworks]: a?.allowPrivateNetworks ?? true,
			[P.debugToolCalls]: a?.debugToolCalls ?? false,
			[P.repoProviders]: a?.allowRepoProviders ?? true,
			[P.repoAgents]: a?.allowRepoAgents ?? true,
			[P.fileAgents]: a?.allowFileAgents ?? true
		};
	}

	function formValues(): Record<Path, unknown> {
		return {
			[P.enabled]: enabled,
			[P.allowCommands]: allowCommands,
			[P.runsOn]: runsOn.trim(),
			[P.commandTimeout]: whole(commandTimeout),
			[P.maxTurns]: whole(maxTurns),
			[P.maxMinutes]: whole(maxMinutes),
			[P.perDay]: whole(perDay),
			[P.concurrent]: whole(concurrent),
			[P.tokens]: whole(tokens),
			[P.retention]: whole(retention),
			[P.privateNetworks]: privateNetworks,
			[P.debugToolCalls]: debugToolCalls,
			[P.repoProviders]: repoProviders,
			[P.repoAgents]: repoAgents,
			[P.fileAgents]: fileAgents
		};
	}

	function buildPatch(keep: Path[], f: Record<Path, unknown>): SettingsPatch {
		const has = (p: Path) => keep.includes(p);
		return {
			git: {
				agents: {
					...(has(P.enabled) ? { enabled: f[P.enabled] as boolean } : {}),
					...(has(P.allowCommands) ? { allowCommands: f[P.allowCommands] as boolean } : {}),
					...(has(P.runsOn) ? { commandsRunsOn: f[P.runsOn] as string } : {}),
					...(has(P.commandTimeout)
						? { commandTimeoutMinutes: f[P.commandTimeout] as number }
						: {}),
					...(has(P.maxTurns) ? { maxTurns: f[P.maxTurns] as number } : {}),
					...(has(P.maxMinutes) ? { maxSessionMinutes: f[P.maxMinutes] as number } : {}),
					...(has(P.perDay) ? { maxSessionsPerDay: f[P.perDay] as number } : {}),
					...(has(P.concurrent) ? { maxConcurrentSessions: f[P.concurrent] as number } : {}),
					...(has(P.tokens) ? { tokenBudgetPerSession: BigInt(f[P.tokens] as number) } : {}),
					...(has(P.retention) ? { logRetentionDays: f[P.retention] as number } : {}),
					...(has(P.privateNetworks)
						? { allowPrivateNetworks: f[P.privateNetworks] as boolean }
						: {}),
					...(has(P.debugToolCalls) ? { debugToolCalls: f[P.debugToolCalls] as boolean } : {}),
					...(has(P.repoProviders) ? { allowRepoProviders: f[P.repoProviders] as boolean } : {}),
					...(has(P.repoAgents) ? { allowRepoAgents: f[P.repoAgents] as boolean } : {}),
					...(has(P.fileAgents) ? { allowFileAgents: f[P.fileAgents] as boolean } : {})
				}
			}
		};
	}

	async function load() {
		loading = true;
		try {
			const [own, parent] = await Promise.all([
				rpcClient.settings.getEffectiveSettings({ scope }, silentCallOptions),
				parentScope
					? rpcClient.settings.getEffectiveSettings({ scope: parentScope }, silentCallOptions)
					: Promise.resolve(null)
			]);
			prov = own.provenance;
			inherited = parent?.settings ?? null;
			seed(own.settings ?? null);
		} catch (err) {
			toast.error(errText(err));
		} finally {
			loading = false;
		}
	}

	// Child scopes store only values that differ from the parent, limits switched off inherit
	async function save() {
		saving = true;
		try {
			const f = formValues();
			const base = valuesOf(inherited);
			const writable = paths.filter((p) => !locked(p));
			const keep = parentScope
				? writable.filter((p) => f[p] !== base[p] && !(limitPaths.includes(p) && f[p] === 0))
				: writable;
			const res = await patchSettings(scope, buildPatch(keep, f), writable);
			prov = res.provenance;
			if (res.effective) seed(res.effective);
			if (isSystem && res.effective) configStore.settings = res.effective;
			toast.success('Agent settings saved');
		} catch (err) {
			toast.error(errText(err));
		} finally {
			saving = false;
		}
	}

	async function resetAll() {
		resetting = true;
		try {
			const res = await patchSettings(
				scope,
				{},
				paths.filter((p) => !locked(p))
			);
			prov = res.provenance;
			if (res.effective) seed(res.effective);
			toast.success('Reset to inherited values');
		} catch (err) {
			toast.error(errText(err));
		} finally {
			resetting = false;
		}
	}

	onMount(load);
</script>

{#if loading}
	<Skeleton class="h-72 w-full rounded-lg" />
{:else}
	{#if !isRepo}
		<FormSection
			title="Policy"
			description={isSystem
				? 'Instance wide switch and what lower tiers may configure themselves.'
				: 'Whether agents run in this organization and what repositories may configure.'}
		>
			<FormField
				label="Agents enabled"
				layout="toggle"
				tag={tag(P.enabled)}
				help={locked(P.enabled)
					? 'Pinned by the config file'
					: 'Sessions run for repositories that switch the unit on'}
			>
				<Switch bind:checked={enabled} disabled={!canEdit || locked(P.enabled)} />
			</FormField>
			{#if isSystem}
				<FormField
					label="Private network connectors"
					layout="toggle"
					tag={tag(P.privateNetworks)}
					help="Inference endpoints on loopback or private ranges, usual for self hosted models"
				>
					<Switch bind:checked={privateNetworks} disabled={!canEdit || locked(P.privateNetworks)} />
				</FormField>
				<FormField
					label="Debug tool calls"
					layout="toggle"
					tag={tag(P.debugToolCalls)}
					help="Every agent tool call and its result is written to the server log, for troubleshooting"
				>
					<Switch bind:checked={debugToolCalls} disabled={!canEdit || locked(P.debugToolCalls)} />
				</FormField>
			{/if}
			<FormField
				label="Repository connectors"
				layout="toggle"
				tag={tag(P.repoProviders)}
				help="Repository admins may register their own inference connectors"
			>
				<Switch bind:checked={repoProviders} disabled={!canEdit || locked(P.repoProviders)} />
			</FormField>
			<FormField
				label="Repository agents"
				layout="toggle"
				tag={tag(P.repoAgents)}
				help="Repository admins may define agents"
			>
				<Switch bind:checked={repoAgents} disabled={!canEdit || locked(P.repoAgents)} />
			</FormField>
		</FormSection>
	{/if}

	<FormSection
		title="Capabilities"
		description="What sessions may do. Lower tiers can only tighten what the tier above allows."
	>
		<FormField
			label="File defined agents"
			layout="toggle"
			tag={tag(P.fileAgents)}
			help="Agents declared under .distroface/agents or .github/agents in the repository"
		>
			<Switch bind:checked={fileAgents} disabled={!canEdit || locked(P.fileAgents)} />
		</FormField>
		<FormField
			label="Run commands"
			layout="toggle"
			tag={tag(P.allowCommands)}
			help="Sessions may run scripts on a workflow runner, builds and tests included"
		>
			<Switch bind:checked={allowCommands} disabled={!canEdit || locked(P.allowCommands)} />
		</FormField>
		<FormField
			label="Command runner label"
			id="agents-runs-on"
			tag={tag(P.runsOn)}
			help="runs-on label for command jobs, blank uses the actions default"
			span="half"
		>
			<Input
				id="agents-runs-on"
				bind:value={runsOn}
				placeholder="ubuntu-latest"
				class="font-mono"
				disabled={!canEdit || locked(P.runsOn)}
			/>
		</FormField>
		<FormField
			label="Command timeout"
			id="agents-command-timeout"
			tag={tag(P.commandTimeout)}
			layout="inline"
			help="Per command, the workspace stays open between commands"
		>
			<LimitInput
				id="agents-command-timeout"
				label="command timeout"
				unit="minutes"
				bind:value={commandTimeout}
				fallback={fallback(P.commandTimeout)}
				seed={20}
				disabled={!canEdit || locked(P.commandTimeout)}
			/>
		</FormField>
	</FormSection>

	<FormSection
		title="Limits"
		description={parentScope
			? 'Caps per session and per repository, only tighter than the tier above.'
			: 'Caps per session and per repository.'}
	>
		<FormField
			label="Model turns"
			id="agents-max-turns"
			tag={tag(P.maxTurns)}
			layout="inline"
			help="Per session"
		>
			<LimitInput
				id="agents-max-turns"
				label="model turns"
				unit="turns"
				bind:value={maxTurns}
				fallback={fallback(P.maxTurns)}
				seed={40}
				disabled={!canEdit || locked(P.maxTurns)}
			/>
		</FormField>
		<FormField
			label="Token budget"
			id="agents-tokens"
			tag={tag(P.tokens)}
			layout="inline"
			help="Transcript size, every token counted once"
		>
			<LimitInput
				id="agents-tokens"
				label="token budget"
				unit="tokens"
				bind:value={tokens}
				fallback={fallback(P.tokens)}
				seed={500000}
				disabled={!canEdit || locked(P.tokens)}
			/>
		</FormField>
		<FormField
			label="Session time limit"
			id="agents-max-minutes"
			tag={tag(P.maxMinutes)}
			layout="inline"
			help="Wall clock per session"
		>
			<LimitInput
				id="agents-max-minutes"
				label="session time"
				unit="minutes"
				bind:value={maxMinutes}
				fallback={fallback(P.maxMinutes)}
				seed={60}
				disabled={!canEdit || locked(P.maxMinutes)}
			/>
		</FormField>
		<FormField
			label="Concurrent sessions"
			id="agents-concurrent"
			tag={tag(P.concurrent)}
			layout="inline"
			help="Per repository"
		>
			<LimitInput
				id="agents-concurrent"
				label="concurrent sessions"
				unit="sessions"
				bind:value={concurrent}
				fallback={fallback(P.concurrent)}
				seed={2}
				disabled={!canEdit || locked(P.concurrent)}
			/>
		</FormField>
		<FormField
			label="Sessions per day"
			id="agents-per-day"
			tag={tag(P.perDay)}
			layout="inline"
			help="Per repository"
		>
			<LimitInput
				id="agents-per-day"
				label="sessions per day"
				unit="sessions"
				bind:value={perDay}
				fallback={fallback(P.perDay)}
				seed={50}
				disabled={!canEdit || locked(P.perDay)}
			/>
		</FormField>
		<FormField
			label="Transcript retention"
			id="agents-retention"
			tag={tag(P.retention)}
			layout="inline"
			help="Finished sessions are deleted after this"
		>
			<LimitInput
				id="agents-retention"
				label="transcript retention"
				unit="days"
				bind:value={retention}
				fallback={fallback(P.retention)}
				seed={90}
				disabled={!canEdit || locked(P.retention)}
			/>
		</FormField>
	</FormSection>

	<FormActions>
		<Button onclick={save} disabled={saving || resetting || !canEdit}>
			{#if saving}
				<Loader2 class="animate-spin" />
			{:else}
				<Save />
			{/if}
			{saving ? 'Saving' : 'Save changes'}
		</Button>
		{#if parentScope && overrideCount > 0}
			<Button variant="ghost-muted" onclick={resetAll} disabled={resetting || saving || !canEdit}>
				{#if resetting}
					<Loader2 class="animate-spin" />
				{:else}
					<Undo2 />
				{/if}
				{resetting ? 'Resetting' : 'Reset to inherited'}
			</Button>
		{/if}
	</FormActions>
{/if}

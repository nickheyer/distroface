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
		canEdit = true,
		showMirror = true
	}: {
		scope: ScopeInit;
		parentScope?: ScopeInit; // One tier up enables custom tags and reset
		canEdit?: boolean;
		showMirror?: boolean; // Mirror trigger only matters where mirror fetches happen
	} = $props();

	const P = {
		enabled: 'git.actions.enabled',
		runsOn: 'git.actions.default_runs_on',
		runOnMirror: 'git.actions.run_on_mirror',
		artifactDays: 'git.actions.artifact_retention_days',
		logDays: 'git.actions.log_retention_days'
	} as const;
	type Path = (typeof P)[keyof typeof P];

	const isSystem = $derived(scope.type === SettingsScopeType.SYSTEM);
	const isRepo = $derived(scope.type === SettingsScopeType.REPO);
	const ownTier = $derived(
		scope.type === SettingsScopeType.ORG
			? SettingsTier.ORG
			: isRepo
				? SettingsTier.REPO
				: SettingsTier.SYSTEM
	);

	// Repositories cannot flip the switch
	const paths = $derived.by((): Path[] => {
		const defaults: Path[] = [P.runsOn, P.runOnMirror, P.artifactDays, P.logDays];
		return isRepo ? defaults : [P.enabled, ...defaults];
	});

	let loading = $state(true);
	let saving = $state(false);
	let resetting = $state(false);
	let prov = $state<FieldProvenance[]>([]);
	let inherited = $state<Settings | null>(null);

	let enabled = $state(false);
	let runsOn = $state('');
	let runOnMirror = $state(false);
	let artifactDays = $state<number | undefined>();
	let logDays = $state<number | undefined>();

	const overridden = (path: string) => !!parentScope && tierOf(prov, path) === ownTier;
	const tag = (path: string) => (overridden(path) ? 'Custom' : undefined);
	const locked = (path: string) => isLocked(prov, path);
	const overrideCount = $derived(paths.filter(overridden).length);
	const clampDays = (v: number | null | undefined) =>
		Math.max(0, Math.min(3650, Math.round(Number(v) || 0)));
	const limitPaths: Path[] = [P.artifactDays, P.logDays];

	// Lower tiers only show a retention they set themselves
	function limit(path: Path, v: number | undefined): number | undefined {
		if (!v || v <= 0) return undefined;
		return parentScope && !overridden(path) ? undefined : v;
	}

	// Placeholder while retention is off
	function unset(path: Path): string {
		if (!parentScope) return 'Forever';
		const v = Number(valuesOf(inherited)[path] ?? 0);
		return v > 0 ? `Inherits ${v} days` : 'Forever';
	}

	function seed(s: Settings | null) {
		const a = s?.git?.actions;
		enabled = a?.enabled ?? false;
		runsOn = a?.defaultRunsOn ?? '';
		runOnMirror = a?.runOnMirror ?? false;
		artifactDays = limit(P.artifactDays, a?.artifactRetentionDays);
		logDays = limit(P.logDays, a?.logRetentionDays);
	}

	function valuesOf(s: Settings | null): Record<Path, unknown> {
		const a = s?.git?.actions;
		return {
			[P.enabled]: a?.enabled ?? false,
			[P.runsOn]: a?.defaultRunsOn ?? '',
			[P.runOnMirror]: a?.runOnMirror ?? false,
			[P.artifactDays]: a?.artifactRetentionDays ?? 0,
			[P.logDays]: a?.logRetentionDays ?? 0
		};
	}

	function formValues(): Record<Path, unknown> {
		return {
			[P.enabled]: enabled,
			[P.runsOn]: runsOn.trim(),
			[P.runOnMirror]: runOnMirror,
			[P.artifactDays]: clampDays(artifactDays),
			[P.logDays]: clampDays(logDays)
		};
	}

	function buildPatch(keep: Path[], f: Record<Path, unknown>): SettingsPatch {
		const has = (p: Path) => keep.includes(p);
		return {
			git: {
				actions: {
					...(has(P.enabled) ? { enabled: f[P.enabled] as boolean } : {}),
					...(has(P.runsOn) ? { defaultRunsOn: f[P.runsOn] as string } : {}),
					...(has(P.runOnMirror) ? { runOnMirror: f[P.runOnMirror] as boolean } : {}),
					...(has(P.artifactDays) ? { artifactRetentionDays: f[P.artifactDays] as number } : {}),
					...(has(P.logDays) ? { logRetentionDays: f[P.logDays] as number } : {})
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

	// Child scopes store only values that differ from the parent
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
			toast.success('Actions settings saved');
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
	<Skeleton class="h-56 w-full rounded-lg" />
{:else}
	{#if !isRepo}
		<FormSection
			title="Policy"
			description={isSystem
				? 'Instance wide switch, organizations and repositories inherit it.'
				: 'Whether workflows run for repositories in this organization.'}
		>
			<FormField
				label="Actions enabled"
				layout="toggle"
				tag={tag(P.enabled)}
				help={locked(P.enabled)
					? 'Pinned by the config file'
					: 'Workflows run on registered runners'}
			>
				<Switch bind:checked={enabled} disabled={!canEdit || locked(P.enabled)} />
			</FormField>
		</FormSection>
	{/if}

	<FormSection
		title="Workflow defaults"
		description={isSystem
			? 'Defaults every organization and repository inherits.'
			: 'Inherited from the organization or instance until changed here.'}
	>
		<FormField
			label="Default runs-on"
			id="actions-runs-on"
			tag={tag(P.runsOn)}
			help={locked(P.runsOn) ? 'Pinned by the config file' : 'Label a job gets when it names none'}
			span="half"
		>
			<Input
				id="actions-runs-on"
				bind:value={runsOn}
				placeholder="ubuntu-latest"
				class="font-mono"
				disabled={!canEdit || locked(P.runsOn)}
			/>
		</FormField>
		{#if showMirror}
			<FormField
				label="Run on mirror fetch"
				layout="toggle"
				tag={tag(P.runOnMirror)}
				help="Commits arriving through a mirror sync trigger workflows too"
			>
				<Switch bind:checked={runOnMirror} disabled={!canEdit || locked(P.runOnMirror)} />
			</FormField>
		{/if}
	</FormSection>

	<FormSection title="Retention" description="How long job artifacts and logs are stored.">
		<FormField
			label="Artifact retention"
			id="actions-artifact-days"
			tag={tag(P.artifactDays)}
			layout="inline"
		>
			<LimitInput
				id="actions-artifact-days"
				label="artifact retention"
				unit="days"
				bind:value={artifactDays}
				unset={unset(P.artifactDays)}
				seed={30}
				max={3650}
				disabled={!canEdit || locked(P.artifactDays)}
			/>
		</FormField>
		<FormField label="Log retention" id="actions-log-days" tag={tag(P.logDays)} layout="inline">
			<LimitInput
				id="actions-log-days"
				label="log retention"
				unit="days"
				bind:value={logDays}
				unset={unset(P.logDays)}
				seed={90}
				max={3650}
				disabled={!canEdit || locked(P.logDays)}
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

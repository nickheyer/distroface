<script lang="ts">
	import { onMount } from 'svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { toast } from 'svelte-sonner';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Switch } from '$lib/components/ui/switch';
	import { Input } from '$lib/components/ui/input';
	import UnitInput from '$lib/components/unit-input.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import FormActions from '$lib/components/form-actions.svelte';
	import { Save, Undo2, Loader2 } from '@lucide/svelte';
	import { errText } from '$lib/act.svelte';
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
		runsOn: 'git.actions.default_runs_on',
		runOnMirror: 'git.actions.run_on_mirror',
		artifactDays: 'git.actions.artifact_retention_days',
		logDays: 'git.actions.log_retention_days'
	} as const;
	type Path = (typeof P)[keyof typeof P];
	const ALL = Object.values(P) as Path[];

	const ownTier = $derived(
		scope.type === SettingsScopeType.ORG
			? SettingsTier.ORG
			: scope.type === SettingsScopeType.REPO
				? SettingsTier.REPO
				: SettingsTier.SYSTEM
	);

	let loading = $state(true);
	let saving = $state(false);
	let resetting = $state(false);
	let prov = $state<FieldProvenance[]>([]);
	let inherited = $state<Settings | null>(null);

	let runsOn = $state('');
	let runOnMirror = $state(false);
	let artifactDays = $state(0);
	let logDays = $state(0);

	const overridden = (path: string) => !!parentScope && tierOf(prov, path) === ownTier;
	const tag = (path: string) => (overridden(path) ? 'Custom' : undefined);
	const locked = (path: string) => isLocked(prov, path);
	const overrideCount = $derived(ALL.filter(overridden).length);
	const clampDays = (v: number) => Math.max(0, Math.min(3650, Math.round(Number(v) || 0)));

	function seed(s: Settings | null) {
		const a = s?.git?.actions;
		runsOn = a?.defaultRunsOn ?? '';
		runOnMirror = a?.runOnMirror ?? false;
		artifactDays = a?.artifactRetentionDays ?? 0;
		logDays = a?.logRetentionDays ?? 0;
	}

	function valuesOf(s: Settings | null): Record<Path, unknown> {
		const a = s?.git?.actions;
		return {
			[P.runsOn]: a?.defaultRunsOn ?? '',
			[P.runOnMirror]: a?.runOnMirror ?? false,
			[P.artifactDays]: a?.artifactRetentionDays ?? 0,
			[P.logDays]: a?.logRetentionDays ?? 0
		};
	}

	function formValues(): Record<Path, unknown> {
		return {
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
			const paths = ALL.filter((p) => !locked(p));
			const keep = parentScope ? paths.filter((p) => f[p] !== base[p]) : paths;
			const res = await patchSettings(scope, buildPatch(keep, f), paths);
			prov = res.provenance;
			if (res.effective) seed(res.effective);
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
				ALL.filter((p) => !locked(p))
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
	<FormSection
		title="Workflow defaults"
		description="Inherited from the organization or instance until changed here."
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

	<FormSection
		title="Retention"
		description="How long job artifacts and logs are stored, in Days. Zero inherits application setting."
	>
		<FormField
			label="Artifact retention"
			id="actions-artifact-days"
			tag={tag(P.artifactDays)}
			layout="inline"
		>
			<UnitInput
				id="actions-artifact-days"
				unit="days"
				class="w-32"
				bind:value={artifactDays}
				min={0}
				max={3650}
				disabled={!canEdit || locked(P.artifactDays)}
			/>
		</FormField>
		<FormField label="Log retention" id="actions-log-days" tag={tag(P.logDays)} layout="inline">
			<UnitInput
				id="actions-log-days"
				unit="days"
				class="w-32"
				bind:value={logDays}
				min={0}
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

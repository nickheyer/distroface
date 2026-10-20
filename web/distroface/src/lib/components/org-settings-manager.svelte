<script lang="ts">
	import { onMount } from 'svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { toast } from 'svelte-sonner';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Switch } from '$lib/components/ui/switch';
	import * as Select from '$lib/components/ui/select';
	import LimitInput from '$lib/components/limit-input.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import { Save, Undo, Loader } from '$lib/icons';
	import { Act } from '$lib/act.svelte';
	import { orgScope, patchSettings, systemScope, tierOf } from '$lib/settings-utils';
	import {
		SettingsTier,
		type FieldProvenance,
		type Settings
	} from '$lib/proto/distroface/v1/settings_pb';

	let { orgId }: { orgId: string } = $props();

	const PATHS = {
		retentionEnabled: 'artifacts.retention.enabled',
		maxVersions: 'artifacts.retention.max_versions',
		maxAgeDays: 'artifacts.retention.max_age_days',
		maxTotalSize: 'artifacts.retention.max_total_size_bytes',
		excludeLatest: 'artifacts.retention.exclude_latest',
		maxFileSizeMb: 'artifacts.max_file_size_mb',
		privateByDefault: 'artifacts.private_by_default',
		portalsIsolated: 'portals.isolated',
		anonymousAccess: 'auth.anonymous_access'
	} as const;
	// Portal policy controls apply live, the save flow skips them
	type LivePath = typeof PATHS.portalsIsolated | typeof PATHS.anonymousAccess;
	type SavePath = Exclude<(typeof PATHS)[keyof typeof PATHS], LivePath>;
	const ALL_PATHS = Object.values(PATHS).filter(
		(p): p is SavePath => p !== PATHS.portalsIsolated && p !== PATHS.anonymousAccess
	);

	type AnonymousMode = 'inherit' | 'allow' | 'deny';
	const anonymousOptions: { value: AnonymousMode; label: string }[] = [
		{ value: 'inherit', label: 'Inherit from instance' },
		{ value: 'allow', label: 'Allow' },
		{ value: 'deny', label: 'Deny' }
	];

	let loading = $state(true);
	let saving = $state(false);
	let resetting = $state(false);
	let prov = $state<FieldProvenance[]>([]);
	let inherited = $state<Settings | null>(null);

	const overridden = (path: string) => tierOf(prov, path) === SettingsTier.ORG;
	const customTag = (path: string) => (overridden(path) ? 'Custom' : undefined);
	const overrideCount = $derived(ALL_PATHS.filter(overridden).length);

	let retentionEnabled = $state(false);
	let maxVersions = $state<number | undefined>();
	let maxAgeDays = $state<number | undefined>();
	let maxTotalSizeMb = $state<number | undefined>();
	let excludeLatest = $state(true);
	let maxFileSizeMb = $state<number | undefined>();
	let privateByDefault = $state(false);
	let portalsIsolated = $state(false);
	let anonymousMode = $state<AnonymousMode>('inherit');
	const isolatedAct = new Act();
	const anonymousAct = new Act();

	const instanceAnonymous = $derived(inherited?.auth?.anonymousAccess ?? false);
	const anonymousLabel = $derived(
		anonymousMode === 'inherit'
			? `Inherit from instance (${instanceAnonymous ? 'allowed' : 'denied'})`
			: anonymousMode === 'allow'
				? 'Allow'
				: 'Deny'
	);

	const LIMIT_PATHS: SavePath[] = [
		PATHS.maxVersions,
		PATHS.maxAgeDays,
		PATHS.maxTotalSize,
		PATHS.maxFileSizeMb
	];
	const whole = (v: number | null | undefined) => Math.max(0, Math.round(Number(v) || 0));

	// Only a limit the org set itself shows a value
	function limit(path: string, v: number | bigint | undefined): number | undefined {
		const n = Number(v ?? 0);
		return n > 0 && overridden(path) ? n : undefined;
	}

	// Placeholder while a limit is off
	// Inherited limit while this scope sets none
	function fallback(v: number | bigint | undefined): number | undefined {
		const n = Number(v ?? 0);
		return n > 0 ? n : undefined;
	}

	function seed(eff: Settings) {
		retentionEnabled = eff.artifacts?.retention?.enabled ?? false;
		maxVersions = limit(PATHS.maxVersions, eff.artifacts?.retention?.maxVersions);
		maxAgeDays = limit(PATHS.maxAgeDays, eff.artifacts?.retention?.maxAgeDays);
		maxTotalSizeMb = limit(
			PATHS.maxTotalSize,
			Math.round(Number(eff.artifacts?.retention?.maxTotalSizeBytes ?? 0n) / (1024 * 1024))
		);
		excludeLatest = eff.artifacts?.retention?.excludeLatest ?? true;
		maxFileSizeMb = limit(PATHS.maxFileSizeMb, eff.artifacts?.maxFileSizeMb);
		privateByDefault = eff.artifacts?.privateByDefault ?? false;
		portalsIsolated = eff.portals?.isolated ?? false;
	}

	// Org tier only, effective values cannot show inherit
	function seedAnonymous(p: FieldProvenance[], eff: Settings) {
		if (tierOf(p, PATHS.anonymousAccess) !== SettingsTier.ORG) {
			anonymousMode = 'inherit';
			return;
		}
		anonymousMode = eff.auth?.anonymousAccess ? 'allow' : 'deny';
	}

	// Applies live, inherit clears the org row
	async function applyAnonymous(v: string) {
		const prev = anonymousMode;
		anonymousMode = v as AnonymousMode;
		const ok = await anonymousAct.run(async () => {
			const res = await patchSettings(
				orgScope(orgId),
				anonymousMode === 'inherit' ? {} : { auth: { anonymousAccess: anonymousMode === 'allow' } },
				[PATHS.anonymousAccess]
			);
			prov = res.provenance;
			if (res.effective) {
				seed(res.effective);
				seedAnonymous(res.provenance, res.effective);
			}
		});
		if (!ok) anonymousMode = prev;
	}

	// Applies live, values matching the instance tier clear back to inherit
	async function applyIsolated(v: boolean) {
		portalsIsolated = v;
		const inheritedIsolated = inherited?.portals?.isolated ?? false;
		const ok = await isolatedAct.run(async () => {
			const res = await patchSettings(
				orgScope(orgId),
				v === inheritedIsolated ? {} : { portals: { isolated: v } },
				['portals.isolated']
			);
			prov = res.provenance;
			if (res.effective) seed(res.effective);
		});
		if (!ok) portalsIsolated = !v;
	}

	async function load() {
		loading = true;
		try {
			const [org, sys] = await Promise.all([
				rpcClient.settings.getEffectiveSettings({ scope: orgScope(orgId) }, silentCallOptions),
				rpcClient.settings.getEffectiveSettings({ scope: systemScope }, silentCallOptions)
			]);
			prov = org.provenance;
			inherited = sys.settings ?? null;
			if (org.settings) {
				seed(org.settings);
				seedAnonymous(org.provenance, org.settings);
			}
		} catch {
			toast.error('Failed to load organization settings');
		} finally {
			loading = false;
		}
	}

	// Values matching the instance tier clear back to inherit, limits switched off inherit
	async function save() {
		saving = true;
		try {
			const inh = inherited;
			const form = {
				[PATHS.retentionEnabled]: retentionEnabled,
				[PATHS.maxVersions]: whole(maxVersions),
				[PATHS.maxAgeDays]: whole(maxAgeDays),
				[PATHS.maxTotalSize]: whole(maxTotalSizeMb) * 1024 * 1024,
				[PATHS.excludeLatest]: excludeLatest,
				[PATHS.maxFileSizeMb]: whole(maxFileSizeMb),
				[PATHS.privateByDefault]: privateByDefault
			} as const;
			const base = {
				[PATHS.retentionEnabled]: inh?.artifacts?.retention?.enabled ?? false,
				[PATHS.maxVersions]: inh?.artifacts?.retention?.maxVersions ?? 0,
				[PATHS.maxAgeDays]: inh?.artifacts?.retention?.maxAgeDays ?? 0,
				[PATHS.maxTotalSize]: Number(inh?.artifacts?.retention?.maxTotalSizeBytes ?? 0n),
				[PATHS.excludeLatest]: inh?.artifacts?.retention?.excludeLatest ?? true,
				[PATHS.maxFileSizeMb]: Number(inh?.artifacts?.maxFileSizeMb ?? 0n),
				[PATHS.privateByDefault]: inh?.artifacts?.privateByDefault ?? false
			} as const;

			const keep = ALL_PATHS.filter(
				(p) => form[p] !== base[p] && !(LIMIT_PATHS.includes(p) && form[p] === 0)
			);
			const patch = {
				artifacts: {
					...(keep.includes(PATHS.maxFileSizeMb)
						? { maxFileSizeMb: BigInt(form[PATHS.maxFileSizeMb]) }
						: {}),
					...(keep.includes(PATHS.privateByDefault)
						? { privateByDefault: form[PATHS.privateByDefault] }
						: {}),
					retention: {
						...(keep.includes(PATHS.retentionEnabled)
							? { enabled: form[PATHS.retentionEnabled] }
							: {}),
						...(keep.includes(PATHS.maxVersions) ? { maxVersions: form[PATHS.maxVersions] } : {}),
						...(keep.includes(PATHS.maxAgeDays) ? { maxAgeDays: form[PATHS.maxAgeDays] } : {}),
						...(keep.includes(PATHS.maxTotalSize)
							? { maxTotalSizeBytes: BigInt(form[PATHS.maxTotalSize]) }
							: {}),
						...(keep.includes(PATHS.excludeLatest)
							? { excludeLatest: form[PATHS.excludeLatest] }
							: {})
					}
				}
			};
			const res = await patchSettings(orgScope(orgId), patch, ALL_PATHS);
			prov = res.provenance;
			if (res.effective) seed(res.effective);
			toast.success('Settings saved');
		} catch {
			// Error interceptor already toasted
		} finally {
			saving = false;
		}
	}

	async function resetAll() {
		resetting = true;
		try {
			const res = await patchSettings(orgScope(orgId), {}, ALL_PATHS);
			prov = res.provenance;
			if (res.effective) seed(res.effective);
			toast.success('Reset to instance defaults');
		} catch {
			// Error interceptor already toasted
		} finally {
			resetting = false;
		}
	}

	onMount(load);
</script>

{#if loading}
	<Skeleton class="h-72 w-full rounded-lg" />
{:else}
	<FormSection
		title="Portals"
		description="How this organization's portals expose content. Changes apply immediately."
	>
		<FormField
			label="Isolate portals"
			layout="toggle"
			tag={isolatedAct.tag ?? customTag(PATHS.portalsIsolated)}
			error={isolatedAct.error}
		>
			<Switch
				checked={portalsIsolated}
				disabled={isolatedAct.busy}
				onCheckedChange={applyIsolated}
			/>
		</FormField>
		<FormField
			label="Anonymous access"
			id="org-anonymous"
			layout="inline"
			tag={anonymousAct.tag ?? customTag(PATHS.anonymousAccess)}
			error={anonymousAct.error}
		>
			<Select.Root
				type="single"
				value={anonymousMode}
				onValueChange={applyAnonymous}
				disabled={anonymousAct.busy}
			>
				<Select.Trigger id="org-anonymous" class="w-64">{anonymousLabel}</Select.Trigger>
				<Select.Content>
					{#each anonymousOptions as option (option.value)}
						<Select.Item value={option.value} label={option.label} />
					{/each}
				</Select.Content>
			</Select.Root>
		</FormField>
	</FormSection>

	<FormSection title="Artifact uploads" description="Defaults for new artifact repositories.">
		<FormField label="Private by default" tag={customTag(PATHS.privateByDefault)} layout="toggle">
			<Switch bind:checked={privateByDefault} />
		</FormField>
		<FormField
			label="Max upload size"
			id="art-max-file"
			tag={customTag(PATHS.maxFileSizeMb)}
			layout="inline"
		>
			<LimitInput
				id="art-max-file"
				label="upload size"
				unit="MB"
				bind:value={maxFileSizeMb}
				fallback={fallback(inherited?.artifacts?.maxFileSizeMb)}
				seed={512}
			/>
		</FormField>
	</FormSection>

	<FormSection
		title="Artifact retention"
		description="Prunes old artifacts automatically. Blank fields inherit the instance."
	>
		<FormField label="Enable retention" tag={customTag(PATHS.retentionEnabled)} layout="toggle">
			<Switch bind:checked={retentionEnabled} />
		</FormField>
		{#if retentionEnabled}
			<div class="form-subfields">
				<FormField
					label="Max versions per path"
					id="art-max-versions"
					tag={customTag(PATHS.maxVersions)}
					layout="inline"
				>
					<LimitInput
						id="art-max-versions"
						label="versions per path"
						unit="versions"
						bind:value={maxVersions}
						fallback={fallback(inherited?.artifacts?.retention?.maxVersions)}
						seed={10}
					/>
				</FormField>
				<FormField
					label="Max age"
					id="art-max-age"
					tag={customTag(PATHS.maxAgeDays)}
					layout="inline"
				>
					<LimitInput
						id="art-max-age"
						label="age"
						unit="days"
						bind:value={maxAgeDays}
						fallback={fallback(inherited?.artifacts?.retention?.maxAgeDays)}
						seed={90}
					/>
				</FormField>
				<FormField
					label="Max total size"
					id="art-max-total"
					tag={customTag(PATHS.maxTotalSize)}
					layout="inline"
					help="Per repository"
				>
					<LimitInput
						id="art-max-total"
						label="total size"
						unit="MB"
						bind:value={maxTotalSizeMb}
						fallback={fallback(
							Math.round(
								Number(inherited?.artifacts?.retention?.maxTotalSizeBytes ?? 0n) / (1024 * 1024)
							)
						)}
						seed={10240}
					/>
				</FormField>
				<FormField label="Keep latest" tag={customTag(PATHS.excludeLatest)} layout="toggle">
					<Switch bind:checked={excludeLatest} />
				</FormField>
			</div>
		{/if}
		{#snippet footer()}
			<Button onclick={save} disabled={saving || resetting}>
				{#if saving}
					<Loader class="animate-spin" />
					Saving
				{:else}
					<Save />
					Save changes
				{/if}
			</Button>
			{#if overrideCount > 0}
				<Button variant="ghost-muted" onclick={resetAll} disabled={resetting || saving}>
					{#if resetting}
						<Loader class="animate-spin" />
						Resetting
					{:else}
						<Undo />
						Reset to defaults
					{/if}
				</Button>
			{/if}
		{/snippet}
	</FormSection>
{/if}

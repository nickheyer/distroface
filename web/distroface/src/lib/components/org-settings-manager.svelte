<script lang="ts">
	import { onMount } from 'svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { toast } from 'svelte-sonner';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Switch } from '$lib/components/ui/switch';
	import { Input } from '$lib/components/ui/input';
	import * as Select from '$lib/components/ui/select';
	import UnitInput from '$lib/components/unit-input.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormCard from '$lib/components/form-card.svelte';
	import { Globe, Package, Save, Undo2 } from '@lucide/svelte';
	import { Act } from '$lib/act.svelte';
	import { orgScope, patchSettings, systemScope, tierOf } from '$lib/settings-utils';
	import { SettingsTier, type FieldProvenance, type Settings } from '$lib/proto/distroface/v1/settings_pb';

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
	let maxVersions = $state(0);
	let maxAgeDays = $state(0);
	let maxTotalSizeMb = $state(0);
	let excludeLatest = $state(true);
	let maxFileSizeMb = $state(0);
	let privateByDefault = $state(false);
	let portalsIsolated = $state(false);
	let anonymousMode = $state<AnonymousMode>('inherit');
	const isolatedAct = new Act();
	const anonymousAct = new Act();

	const instanceAnonymous = $derived(inherited?.auth?.anonymousAccess ?? false);
	const anonymousLabel = $derived(
		anonymousMode === 'inherit'
			? `Inherit from instance (${instanceAnonymous ? 'allowed' : 'denied'})`
			: anonymousMode === 'allow' ? 'Allow' : 'Deny'
	);

	function seed(eff: Settings) {
		retentionEnabled = eff.artifacts?.retention?.enabled ?? false;
		maxVersions = eff.artifacts?.retention?.maxVersions ?? 0;
		maxAgeDays = eff.artifacts?.retention?.maxAgeDays ?? 0;
		maxTotalSizeMb = Math.round(Number(eff.artifacts?.retention?.maxTotalSizeBytes ?? 0n) / (1024 * 1024));
		excludeLatest = eff.artifacts?.retention?.excludeLatest ?? true;
		maxFileSizeMb = Number(eff.artifacts?.maxFileSizeMb ?? 0n);
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

	// Values matching the instance tier clear back to inherit
	async function save() {
		saving = true;
		try {
			const inh = inherited;
			const form = {
				[PATHS.retentionEnabled]: retentionEnabled,
				[PATHS.maxVersions]: Math.max(0, Math.round(maxVersions)),
				[PATHS.maxAgeDays]: Math.max(0, Math.round(maxAgeDays)),
				[PATHS.maxTotalSize]: Math.max(0, Math.round(maxTotalSizeMb)) * 1024 * 1024,
				[PATHS.excludeLatest]: excludeLatest,
				[PATHS.maxFileSizeMb]: Math.max(0, Math.round(maxFileSizeMb)),
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

			const keep = ALL_PATHS.filter((p) => form[p] !== base[p]);
			const patch = {
				artifacts: {
					...(keep.includes(PATHS.maxFileSizeMb) ? { maxFileSizeMb: BigInt(form[PATHS.maxFileSizeMb]) } : {}),
					...(keep.includes(PATHS.privateByDefault) ? { privateByDefault: form[PATHS.privateByDefault] } : {}),
					retention: {
						...(keep.includes(PATHS.retentionEnabled) ? { enabled: form[PATHS.retentionEnabled] } : {}),
						...(keep.includes(PATHS.maxVersions) ? { maxVersions: form[PATHS.maxVersions] } : {}),
						...(keep.includes(PATHS.maxAgeDays) ? { maxAgeDays: form[PATHS.maxAgeDays] } : {}),
						...(keep.includes(PATHS.maxTotalSize) ? { maxTotalSizeBytes: BigInt(form[PATHS.maxTotalSize]) } : {}),
						...(keep.includes(PATHS.excludeLatest) ? { excludeLatest: form[PATHS.excludeLatest] } : {})
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
	<Skeleton class="h-72 w-full rounded-xl" />
{:else}
	<FormCard
		title="Portals"
		description="How this organization's portals expose content."
		icon={Globe}
	>
		<FormField
			label="Isolate portals"
			horizontal
			tag={isolatedAct.tag ?? customTag(PATHS.portalsIsolated)}
			error={isolatedAct.error}
			help="Portals serve only this organization's content"
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
			horizontal
			tag={anonymousAct.tag ?? customTag(PATHS.anonymousAccess)}
			error={anonymousAct.error}
			help="Default for this organization's portals, each portal may override. Signed out visitors get the anonymous role grants"
		>
			<Select.Root type="single" value={anonymousMode} onValueChange={applyAnonymous} disabled={anonymousAct.busy}>
				<Select.Trigger id="org-anonymous" class="w-64">{anonymousLabel}</Select.Trigger>
				<Select.Content>
					{#each anonymousOptions as option (option.value)}
						<Select.Item value={option.value} label={option.label} />
					{/each}
				</Select.Content>
			</Select.Root>
		</FormField>
	</FormCard>

	<FormCard
		title="Artifacts"
		description="Retention and upload limits for artifact repositories."
		icon={Package}
	>
		<div class="space-y-5">
			<div class="space-y-3">
				<p class="text-xs font-medium uppercase tracking-wide text-muted-foreground">Uploads</p>
				<FormField label="Private by default" tag={customTag(PATHS.privateByDefault)} horizontal>
					<Switch bind:checked={privateByDefault} />
				</FormField>
				<FormField label="Max upload size" id="art-max-file" tag={customTag(PATHS.maxFileSizeMb)} help="0 means unlimited">
					<UnitInput id="art-max-file" unit="MB" bind:value={maxFileSizeMb} min={0} class="w-36" />
				</FormField>
			</div>

			<div class="space-y-3 pt-4 border-t border-border/40">
				<p class="text-xs font-medium uppercase tracking-wide text-muted-foreground">Retention</p>
				<FormField label="Enable retention" tag={customTag(PATHS.retentionEnabled)} help="Prunes old artifacts automatically" horizontal>
					<Switch bind:checked={retentionEnabled} />
				</FormField>
				{#if retentionEnabled}
					<div class="space-y-3 pl-3 border-l-2 border-border/40">
						<FormField label="Max versions per path" id="art-max-versions" tag={customTag(PATHS.maxVersions)} help="0 means unlimited">
							<Input id="art-max-versions" type="number" bind:value={maxVersions} min={0} class="w-36" />
						</FormField>
						<FormField label="Max age" id="art-max-age" tag={customTag(PATHS.maxAgeDays)} help="0 disables age pruning">
							<UnitInput id="art-max-age" unit="days" bind:value={maxAgeDays} min={0} class="w-36" />
						</FormField>
						<FormField label="Max total size" id="art-max-total" tag={customTag(PATHS.maxTotalSize)} help="Per repo, 0 means unlimited">
							<UnitInput id="art-max-total" unit="MB" bind:value={maxTotalSizeMb} min={0} class="w-36" />
						</FormField>
						<FormField label="Keep latest" tag={customTag(PATHS.excludeLatest)} help="Always keep the newest (per path), regardless of retention." horizontal>
							<Switch bind:checked={excludeLatest} />
						</FormField>
					</div>
				{/if}
			</div>
		</div>
		{#snippet footer()}
			{#if overrideCount > 0}
				<Button variant="ghost" onclick={resetAll} disabled={resetting || saving} class="gap-2 mr-auto text-muted-foreground">
					<Undo2 class="h-4 w-4" />
					{resetting ? 'Resetting...' : 'Reset to defaults'}
				</Button>
			{/if}
			<Button onclick={save} disabled={saving || resetting} class="gap-2">
				<Save class="h-4 w-4" />
				{saving ? 'Saving...' : 'Save Changes'}
			</Button>
		{/snippet}
	</FormCard>
{/if}

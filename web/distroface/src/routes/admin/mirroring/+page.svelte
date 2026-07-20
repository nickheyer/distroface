<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Switch } from '$lib/components/ui/switch';
	import UnitInput from '$lib/components/unit-input.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormCard from '$lib/components/form-card.svelte';
	import { Button } from '$lib/components/ui/button';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { Act, errText } from '$lib/act.svelte';
	import { isLocked, patchSettings, systemScope, type SettingsPatch } from '$lib/settings-utils';
	import type { FieldProvenance, Settings } from '$lib/proto/distroface/v1/settings_pb';

	type NumField = {
		key: string;
		path: string;
		label: string;
		help: string;
		unit: string;
		min: number;
		max: number;
	};

	const scheduleFields: NumField[] = [
		{
			key: 'defaultIntervalMinutes',
			path: 'mirror.default_interval_minutes',
			label: 'Default sync interval',
			help: 'When a repository sets no interval',
			unit: 'min',
			min: 1,
			max: 10080
		},
		{
			key: 'minIntervalMinutes',
			path: 'mirror.min_interval_minutes',
			label: 'Minimum sync interval',
			help: 'Floor for repository intervals, 0 disables',
			unit: 'min',
			min: 0,
			max: 1440
		}
	];

	const clampFields: NumField[] = [
		{
			key: 'maxConcurrentSyncs',
			path: 'mirror.max_concurrent_syncs',
			label: 'Concurrent syncs',
			help: 'Repositories synced in parallel per sweep',
			unit: 'repos',
			min: 1,
			max: 32
		},
		{
			key: 'perHostSpacingMs',
			path: 'mirror.per_host_spacing_ms',
			label: 'Request spacing',
			help: 'Delay between requests to one upstream host',
			unit: 'ms',
			min: 0,
			max: 60000
		},
		{
			key: 'syncTimeoutMinutes',
			path: 'mirror.sync_timeout_minutes',
			label: 'Sync timeout',
			help: 'Cancels a repository sync after this',
			unit: 'min',
			min: 1,
			max: 720
		},
		{
			key: 'maxSyncDepth',
			path: 'mirror.max_sync_depth',
			label: 'Max releases per sync',
			help: 'Newest releases or tags per repository, 0 unlimited',
			unit: 'items',
			min: 0,
			max: 10000
		},
		{
			key: 'rateLimitCooldownMinutes',
			path: 'mirror.rate_limit_cooldown_minutes',
			label: 'Rate limit cooldown',
			help: 'Pause when rate limited without a deadline',
			unit: 'min',
			min: 1,
			max: 1440
		},
		{
			key: 'maxCooldownMinutes',
			path: 'mirror.max_cooldown_minutes',
			label: 'Cooldown ceiling',
			help: 'Longest honored cooldown or failure backoff',
			unit: 'min',
			min: 1,
			max: 10080
		},
		{
			key: 'failureBackoffMinutes',
			path: 'mirror.failure_backoff_minutes',
			label: 'Failure backoff',
			help: 'Doubles with each consecutive failure',
			unit: 'min',
			min: 1,
			max: 1440
		}
	];

	let eff = $state<Settings | null>(null);
	let prov = $state<FieldProvenance[]>([]);
	let loading = $state(true);
	let loadError = $state('');

	let enabled = $state(true);
	let allowPrivate = $state(false);
	let values = $state<Record<string, number>>({});

	const enabledAct = new Act();
	const privateAct = new Act();
	const acts: Record<string, Act> = {};
	for (const f of [...scheduleFields, ...clampFields]) acts[f.key] = new Act();

	let canEdit = $derived(authStore.canUpdateSettings);

	const locked = (path: string) => isLocked(prov, path);
	const lockHelp = (path: string, help?: string) =>
		locked(path) ? 'Pinned by the config file' : help;

	function mirrorNum(s: Settings | null, key: string): number {
		return Number((s?.mirror as Record<string, unknown> | undefined)?.[key] ?? 0);
	}

	function seedForm(s: Settings) {
		enabled = s.mirror?.enabled ?? true;
		allowPrivate = s.mirror?.allowPrivateNetworks ?? false;
		for (const f of [...scheduleFields, ...clampFields]) {
			values[f.key] = mirrorNum(s, f.key);
		}
	}

	async function load() {
		loading = true;
		loadError = '';
		try {
			const resp = await rpcClient.settings.getEffectiveSettings({ scope: systemScope }, silentCallOptions);
			eff = resp.settings ?? null;
			prov = resp.provenance;
			if (eff) seedForm(eff);
		} catch (err) {
			loadError = errText(err);
		} finally {
			loading = false;
		}
	}

	// Settings apply on interaction, a failed patch reverts the control
	async function apply(act: Act, settings: SettingsPatch, paths: string[]) {
		const ok = await act.run(async () => {
			const res = await patchSettings(systemScope, settings, paths);
			if (res.effective) {
				eff = res.effective;
				prov = res.provenance;
				seedForm(res.effective);
			}
		});
		if (!ok && eff) seedForm(eff);
	}

	function commitNum(f: NumField) {
		const v = Math.min(Math.max(Math.round(values[f.key] ?? 0), f.min), f.max);
		values[f.key] = v;
		if (v === mirrorNum(eff, f.key)) return;
		apply(acts[f.key], { mirror: { [f.key]: v } }, [f.path]);
	}

	onMount(() => {
		if (!authStore.hasPermission('settings', 'read')) { goto(resolve('/admin')); return; }
		load();
	});
</script>

{#snippet numRow(f: NumField)}
	<FormField
		label={f.label}
		id={f.key}
		horizontal
		bordered={false}
		help={lockHelp(f.path, f.help)}
		tag={acts[f.key].tag}
		error={acts[f.key].error}
	>
		<UnitInput
			id={f.key}
			unit={f.unit}
			bind:value={values[f.key]}
			min={f.min}
			max={f.max}
			class="w-32"
			disabled={!canEdit || acts[f.key].busy || locked(f.path)}
			onblur={() => commitNum(f)}
			onkeydown={(e) => { if (e.key === 'Enter') commitNum(f); }}
		/>
	</FormField>
{/snippet}

{#if loading}
	<div class="space-y-6">
		<Skeleton class="h-52 w-full rounded-xl" />
		<Skeleton class="h-64 w-full rounded-xl" />
	</div>
{:else if loadError}
	<div class="rounded-xl border border-destructive/40 bg-destructive/5 px-6 py-10 text-center space-y-3">
		<p class="text-sm text-destructive">{loadError}</p>
		<Button variant="outline" size="sm" onclick={load}>Retry</Button>
	</div>
{:else if eff}
	<div class="space-y-6">
		<FormCard title="Mirroring" description="Scheduled pulls from registries and release hosts">
			<div class="-my-3 divide-y divide-border/50">
				<FormField
					label="Enabled"
					horizontal
					bordered={false}
					help={lockHelp('mirror.enabled', 'Pauses every scheduled and manual sync when off')}
					tag={enabledAct.tag}
					error={enabledAct.error}
				>
					<Switch
						checked={enabled}
						disabled={!canEdit || enabledAct.busy || locked('mirror.enabled')}
						onCheckedChange={(v) => { enabled = v; apply(enabledAct, { mirror: { enabled: v } }, ['mirror.enabled']); }}
					/>
				</FormField>
				{#each scheduleFields as f (f.key)}
					{@render numRow(f)}
				{/each}
				<FormField
					label="Allow private networks"
					horizontal
					bordered={false}
					help={lockHelp('mirror.allow_private_networks')}
					tag={privateAct.tag}
					error={privateAct.error}
				>
					<Switch
						checked={allowPrivate}
						disabled={!canEdit || privateAct.busy || locked('mirror.allow_private_networks')}
						onCheckedChange={(v) => { allowPrivate = v; apply(privateAct, { mirror: { allowPrivateNetworks: v } }, ['mirror.allow_private_networks']); }}
					/>
				</FormField>
			</div>
		</FormCard>

		<FormCard title="Sync Clamps" description="Instance-wide limits protecting shared upstream quotas">
			<div class="-my-3 divide-y divide-border/50">
				{#each clampFields as f (f.key)}
					{@render numRow(f)}
				{/each}
			</div>
		</FormCard>
	</div>
{/if}

<script lang="ts">
	import { onMount } from 'svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { toast } from 'svelte-sonner';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Switch } from '$lib/components/ui/switch';
	import { Input } from '$lib/components/ui/input';
	import FormField from '$lib/components/form-field.svelte';
	import FormCard from '$lib/components/form-card.svelte';
	import { Package, Save, Undo2 } from '@lucide/svelte';

	let { orgName }: { orgName: string } = $props();

	// Override keys mirrored in internal/artifacts/manager.go
	const KEYS = {
		retentionEnabled: 'artifacts.retention.enabled',
		maxVersions: 'artifacts.retention.max_versions',
		maxAgeDays: 'artifacts.retention.max_age_days',
		maxTotalSize: 'artifacts.retention.max_total_size',
		excludeLatest: 'artifacts.retention.exclude_latest',
		maxFileSizeMb: 'artifacts.max_file_size_mb',
		privateByDefault: 'artifacts.private_by_default'
	} as const;

	let loading = $state(true);
	let saving = $state(false);
	let resetting = $state(false);
	let defaults = $state<Record<string, string>>({});
	let overriddenKeys = $state<Set<string>>(new Set());

	const customTag = (key: string) => (overriddenKeys.has(key) ? 'Custom' : undefined);

	let retentionEnabled = $state(false);
	let maxVersions = $state(0);
	let maxAgeDays = $state(0);
	let maxTotalSizeMb = $state(0);
	let excludeLatest = $state(true);
	let maxFileSizeMb = $state(0);
	let privateByDefault = $state(false);

	async function load() {
		loading = true;
		try {
			const resp = await rpcClient.organization.getOrgSettings({ orgName });
			defaults = resp.defaults;
			overriddenKeys = new Set(Object.keys(resp.overrides));
			const val = (key: string) => resp.overrides[key] ?? resp.defaults[key] ?? '';
			retentionEnabled = val(KEYS.retentionEnabled) === 'true';
			maxVersions = Number(val(KEYS.maxVersions)) || 0;
			maxAgeDays = Number(val(KEYS.maxAgeDays)) || 0;
			maxTotalSizeMb = Math.round((Number(val(KEYS.maxTotalSize)) || 0) / (1024 * 1024));
			excludeLatest = val(KEYS.excludeLatest) === 'true';
			maxFileSizeMb = Number(val(KEYS.maxFileSizeMb)) || 0;
			privateByDefault = val(KEYS.privateByDefault) === 'true';
		} catch {
			toast.error('Failed to load organization settings');
		} finally {
			loading = false;
		}
	}

	async function save() {
		saving = true;
		try {
			const values: Record<string, string> = {
				[KEYS.retentionEnabled]: String(retentionEnabled),
				[KEYS.maxVersions]: String(Math.max(0, Math.round(maxVersions))),
				[KEYS.maxAgeDays]: String(Math.max(0, Math.round(maxAgeDays))),
				[KEYS.maxTotalSize]: String(Math.max(0, Math.round(maxTotalSizeMb)) * 1024 * 1024),
				[KEYS.excludeLatest]: String(excludeLatest),
				[KEYS.maxFileSizeMb]: String(Math.max(0, Math.round(maxFileSizeMb))),
				[KEYS.privateByDefault]: String(privateByDefault)
			};
			// Only values differing from defaults are stored as overrides
			const set: Record<string, string> = {};
			const reset: string[] = [];
			for (const [key, value] of Object.entries(values)) {
				if (value !== (defaults[key] ?? '')) {
					set[key] = value;
				} else if (overriddenKeys.has(key)) {
					reset.push(key);
				}
			}
			await rpcClient.organization.updateOrgSettings({ orgName, set, reset });
			toast.success('Settings saved');
			await load();
		} catch {
			// Error interceptor already toasted
		} finally {
			saving = false;
		}
	}

	async function resetAll() {
		resetting = true;
		try {
			await rpcClient.organization.updateOrgSettings({ orgName, reset: Object.values(KEYS) });
			toast.success('Reset to instance defaults');
			await load();
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
		title="Artifacts"
		description="Retention and upload limits for this organization's artifact repositories. Values differing from the instance defaults are stored as overrides."
		icon={Package}
	>
		<div class="space-y-5">
			<div class="space-y-3">
				<p class="text-xs font-medium uppercase tracking-wide text-muted-foreground">Uploads</p>
				<FormField label="Private by default" tag={customTag(KEYS.privateByDefault)} help="New artifact repositories created under this org start private." horizontal>
					<Switch bind:checked={privateByDefault} />
				</FormField>
				<FormField label="Max upload size (MB)" id="art-max-file" tag={customTag(KEYS.maxFileSizeMb)} help="Largest single artifact allowed; 0 means unlimited.">
					<Input id="art-max-file" type="number" bind:value={maxFileSizeMb} min={0} class="w-36" />
				</FormField>
			</div>

			<div class="space-y-3 pt-4 border-t border-border/40">
				<p class="text-xs font-medium uppercase tracking-wide text-muted-foreground">Retention</p>
				<FormField label="Enable retention" tag={customTag(KEYS.retentionEnabled)} help="Automatically prune old artifacts on upload and on the scheduled reaper." horizontal>
					<Switch bind:checked={retentionEnabled} />
				</FormField>
				{#if retentionEnabled}
					<div class="space-y-3 pl-3 border-l-2 border-border/40">
						<FormField label="Max versions per path" id="art-max-versions" tag={customTag(KEYS.maxVersions)} help="Newest versions kept per artifact path; 0 means unlimited.">
							<Input id="art-max-versions" type="number" bind:value={maxVersions} min={0} class="w-36" />
						</FormField>
						<FormField label="Max age (days)" id="art-max-age" tag={customTag(KEYS.maxAgeDays)} help="Prune artifacts older than this; 0 disables age pruning.">
							<Input id="art-max-age" type="number" bind:value={maxAgeDays} min={0} class="w-36" />
						</FormField>
						<FormField label="Max total size (MB)" id="art-max-total" tag={customTag(KEYS.maxTotalSize)} help="Cap on summed artifact size per repo; 0 means unlimited.">
							<Input id="art-max-total" type="number" bind:value={maxTotalSizeMb} min={0} class="w-36" />
						</FormField>
						<FormField label="Keep latest" tag={customTag(KEYS.excludeLatest)} help="Never prune the newest artifact of a path, even over limits." horizontal>
							<Switch bind:checked={excludeLatest} />
						</FormField>
					</div>
				{/if}
			</div>
		</div>
		{#snippet footer()}
			{#if overriddenKeys.size > 0}
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

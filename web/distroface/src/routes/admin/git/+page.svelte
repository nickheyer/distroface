<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Switch } from '$lib/components/ui/switch';
	import { Input } from '$lib/components/ui/input';
	import { Button } from '$lib/components/ui/button';
	import UnitInput from '$lib/components/unit-input.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import { Loader2, RadioTower } from '@lucide/svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { Act, errText } from '$lib/act.svelte';
	import { isLocked, patchSettings, systemScope, type SettingsPatch } from '$lib/settings-utils';
	import type { FieldProvenance, Settings } from '$lib/proto/distroface/v1/settings_pb';
	import { cn, formatBytes } from '$lib/utils';
	import type { GetGitServerInfoResponse } from '$lib/proto/distroface/v1/settings_pb';

	type BoolField = {
		key: string;
		path: string;
		label: string;
		help?: string;
		get: (s: Settings) => boolean;
		patch: (v: boolean) => SettingsPatch;
		// Error text blocks the save, empty string allows it
		verify?: (v: boolean) => Promise<string>;
	};
	type NumField = {
		key: string;
		path: string;
		label: string;
		help?: string;
		unit: string;
		min: number;
		max: number;
		get: (s: Settings) => number;
		patch: (v: number) => SettingsPatch;
		verify?: (v: number) => Promise<string>;
	};
	type TextField = {
		key: string;
		path: string;
		label: string;
		help?: string;
		placeholder?: string;
		get: (s: Settings) => string;
		patch: (v: string) => SettingsPatch;
	};

	const git = (s: Settings) => s.git;

	const bools: Record<string, BoolField> = {
		enabled: {
			key: 'enabled',
			path: 'git.enabled',
			label: 'Enabled',
			help: 'Serves git repositories under the Git tab',
			get: (s) => git(s)?.enabled ?? false,
			patch: (v) => ({ git: { enabled: v } })
		},
		privateByDefault: {
			key: 'privateByDefault',
			path: 'git.private_by_default',
			label: 'Private by default',
			help: 'New repositories start private',
			get: (s) => git(s)?.privateByDefault ?? false,
			patch: (v) => ({ git: { privateByDefault: v } })
		},
		allowForks: {
			key: 'allowForks',
			path: 'git.allow_forks',
			label: 'Allow forks',
			help: 'Users may fork repositories they can read',
			get: (s) => git(s)?.allowForks ?? true,
			patch: (v) => ({ git: { allowForks: v } })
		},
		releaseOnTag: {
			key: 'releaseOnTag',
			path: 'git.release_on_tag',
			label: 'Release on tag',
			help: 'Annotated tags create draft releases',
			get: (s) => git(s)?.releaseOnTag ?? false,
			patch: (v) => ({ git: { releaseOnTag: v } })
		},
		sshEnabled: {
			key: 'sshEnabled',
			path: 'git.ssh_enabled',
			label: 'SSH enabled',
			help: 'Opens the listener and shows SSH clone urls, the port is probed first',
			get: (s) => git(s)?.sshEnabled ?? false,
			patch: (v) => ({ git: { sshEnabled: v } }),
			verify: async (v) => (v ? probe(numValues.sshPort ?? 22) : '')
		},
		lfsEnabled: {
			key: 'lfsEnabled',
			path: 'git.lfs_enabled',
			label: 'LFS enabled',
			help: 'Large files stored in the blob store',
			get: (s) => git(s)?.lfsEnabled ?? false,
			patch: (v) => ({ git: { lfsEnabled: v } })
		},
		gcEnabled: {
			key: 'gcEnabled',
			path: 'git.gc.enabled',
			label: 'Scheduled gc',
			help: 'Repacks and prunes every repository on a schedule',
			get: (s) => git(s)?.gc?.enabled ?? false,
			patch: (v) => ({ git: { gc: { enabled: v } } })
		},
		signingEnabled: {
			key: 'signingEnabled',
			path: 'git.signing.enabled',
			label: 'Sign web commits',
			help: 'The instance key signs commits and merges made in the browser',
			get: (s) => git(s)?.signing?.enabled ?? false,
			patch: (v) => ({ git: { signing: { enabled: v } } })
		},
		actionsEnabled: {
			key: 'actionsEnabled',
			path: 'git.actions.enabled',
			label: 'Actions enabled',
			help: 'Workflows run on registered runners',
			get: (s) => git(s)?.actions?.enabled ?? false,
			patch: (v) => ({ git: { actions: { enabled: v } } })
		},
		runOnMirror: {
			key: 'runOnMirror',
			path: 'git.actions.run_on_mirror',
			label: 'Run on mirror fetch',
			help: 'Mirror updates trigger workflows too',
			get: (s) => git(s)?.actions?.runOnMirror ?? false,
			patch: (v) => ({ git: { actions: { runOnMirror: v } } })
		}
	};

	const nums: Record<string, NumField> = {
		maxRepoSizeMb: {
			key: 'maxRepoSizeMb',
			path: 'git.max_repo_size_mb',
			label: 'Max repository size',
			help: 'Pushes past this are rejected, 0 unlimited',
			unit: 'MB',
			min: 0,
			max: 1_000_000,
			get: (s) => Number(git(s)?.maxRepoSizeMb ?? 0n),
			patch: (v) => ({ git: { maxRepoSizeMb: BigInt(v) } })
		},
		sshPort: {
			key: 'sshPort',
			path: 'git.ssh_port',
			label: 'SSH port',
			help: 'Default 22, probed on this host before it is saved and rebinds live',
			unit: 'port',
			min: 1,
			max: 65535,
			get: (s) => git(s)?.sshPort ?? 22,
			patch: (v) => ({ git: { sshPort: v } }),
			verify: async (v) => (boolValues.sshEnabled ? probe(v) : '')
		},
		maxLfsObjectMb: {
			key: 'maxLfsObjectMb',
			path: 'git.max_lfs_object_mb',
			label: 'Max LFS object',
			help: 'Largest single object accepted',
			unit: 'MB',
			min: 1,
			max: 1_000_000,
			get: (s) => Number(git(s)?.maxLfsObjectMb ?? 0n),
			patch: (v) => ({ git: { maxLfsObjectMb: BigInt(v) } })
		},
		maxConcurrentTransfers: {
			key: 'maxConcurrentTransfers',
			path: 'git.max_concurrent_transfers',
			label: 'Concurrent transfers',
			help: 'Fetches and pushes served in parallel',
			unit: 'ops',
			min: 1,
			max: 1024,
			get: (s) => git(s)?.maxConcurrentTransfers ?? 32,
			patch: (v) => ({ git: { maxConcurrentTransfers: v } })
		},
		gcIntervalHours: {
			key: 'gcIntervalHours',
			path: 'git.gc.interval_hours',
			label: 'GC interval',
			help: 'Hours between sweeps',
			unit: 'hours',
			min: 1,
			max: 8760,
			get: (s) => git(s)?.gc?.intervalHours ?? 168,
			patch: (v) => ({ git: { gc: { intervalHours: v } } })
		},
		artifactRetentionDays: {
			key: 'artifactRetentionDays',
			path: 'git.actions.artifact_retention_days',
			label: 'Artifact retention',
			help: 'Days before run artifacts are deleted, 0 keeps forever',
			unit: 'days',
			min: 0,
			max: 3650,
			get: (s) => git(s)?.actions?.artifactRetentionDays ?? 0,
			patch: (v) => ({ git: { actions: { artifactRetentionDays: v } } })
		},
		logRetentionDays: {
			key: 'logRetentionDays',
			path: 'git.actions.log_retention_days',
			label: 'Log retention',
			help: 'Days before job logs are deleted, 0 keeps forever',
			unit: 'days',
			min: 0,
			max: 3650,
			get: (s) => git(s)?.actions?.logRetentionDays ?? 0,
			patch: (v) => ({ git: { actions: { logRetentionDays: v } } })
		}
	};

	const texts: Record<string, TextField> = {
		defaultBranch: {
			key: 'defaultBranch',
			path: 'git.default_branch',
			label: 'Default branch',
			help: 'Name of the first branch in new repositories',
			placeholder: 'main',
			get: (s) => git(s)?.defaultBranch ?? '',
			patch: (v) => ({ git: { defaultBranch: v } })
		},
		sshPublicHost: {
			key: 'sshPublicHost',
			path: 'git.ssh_public_host',
			label: 'SSH public host',
			help: 'Host or host:port in clone urls, blank uses the public hostname',
			placeholder: configStore.publicHostname.replace(/:\d+$/, ''),
			get: (s) => git(s)?.sshPublicHost ?? '',
			patch: (v) => ({ git: { sshPublicHost: v } })
		},
		defaultRunsOn: {
			key: 'defaultRunsOn',
			path: 'git.actions.default_runs_on',
			label: 'Default runs-on',
			help: 'Label used when a job names none',
			placeholder: 'ubuntu-latest',
			get: (s) => git(s)?.actions?.defaultRunsOn ?? '',
			patch: (v) => ({ git: { actions: { defaultRunsOn: v } } })
		}
	};

	let eff = $state<Settings | null>(null);
	let prov = $state<FieldProvenance[]>([]);
	let loading = $state(true);
	let loadError = $state('');

	let boolValues = $state<Record<string, boolean>>({});
	let numValues = $state<Record<string, number>>({});
	let textValues = $state<Record<string, string>>({});
	const acts: Record<string, Act> = {};
	for (const k of [...Object.keys(bools), ...Object.keys(nums), ...Object.keys(texts)])
		acts[k] = new Act();

	const canEdit = $derived(authStore.canUpdateSettings);
	const locked = (path: string) => isLocked(prov, path);
	const lockHelp = (path: string, help?: string) =>
		locked(path) ? 'Pinned by the config file' : help;

	// Listener state of the saved port, refreshed after every ssh change
	type SSHStatus = { port: number; ok: boolean; listening: boolean; message: string };
	let sshStatus = $state<SSHStatus | null>(null);
	// Host keys, signing key, storage path and usage
	let info = $state<GetGitServerInfoResponse | null>(null);
	let showSigningKey = $state(false);
	let sshChecking = $state(false);

	async function probe(port: number): Promise<string> {
		try {
			const resp = await rpcClient.settings.probeGitSSHPort({ port }, silentCallOptions);
			return resp.ok ? '' : resp.message || `Port ${port} cannot be used`;
		} catch (err) {
			return errText(err);
		}
	}

	async function refreshSSHStatus() {
		if (!eff || !boolValues.sshEnabled) {
			sshStatus = null;
			return;
		}
		sshChecking = true;
		try {
			const resp = await rpcClient.settings.probeGitSSHPort({ port: 0 }, silentCallOptions);
			sshStatus = {
				port: resp.port,
				ok: resp.ok,
				listening: resp.listening,
				message: resp.message
			};
		} catch (err) {
			sshStatus = {
				port: numValues.sshPort ?? 22,
				ok: false,
				listening: false,
				message: errText(err)
			};
		} finally {
			sshChecking = false;
		}
	}

	function seedForm(s: Settings) {
		for (const f of Object.values(bools)) boolValues[f.key] = f.get(s);
		for (const f of Object.values(nums)) numValues[f.key] = f.get(s);
		for (const f of Object.values(texts)) textValues[f.key] = f.get(s);
	}

	async function load() {
		loading = true;
		loadError = '';
		try {
			const resp = await rpcClient.settings.getEffectiveSettings(
				{ scope: systemScope },
				silentCallOptions
			);
			eff = resp.settings ?? null;
			prov = resp.provenance;
			if (eff) seedForm(eff);
			await refreshSSHStatus();
			info = await rpcClient.settings.getGitServerInfo({}, silentCallOptions);
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
				configStore.settings = res.effective;
			}
		});
		if (!ok && eff) seedForm(eff);
		if (paths.some((p) => p.startsWith('git.ssh') || p === 'git.enabled')) await refreshSSHStatus();
	}

	async function commitBool(f: BoolField, v: boolean) {
		boolValues[f.key] = v;
		if (f.verify) {
			const act = acts[f.key];
			act.busy = true;
			act.error = '';
			const problem = await f.verify(v);
			act.busy = false;
			if (problem) {
				act.error = problem;
				if (eff) boolValues[f.key] = f.get(eff);
				return;
			}
		}
		await apply(acts[f.key], f.patch(v), [f.path]);
	}

	async function commitNum(f: NumField) {
		const v = Math.min(Math.max(Math.round(numValues[f.key] ?? 0), f.min), f.max);
		numValues[f.key] = v;
		if (eff && v === f.get(eff)) return;
		if (f.verify) {
			const act = acts[f.key];
			act.busy = true;
			act.error = '';
			const problem = await f.verify(v);
			act.busy = false;
			if (problem) {
				act.error = problem;
				if (eff) numValues[f.key] = f.get(eff);
				return;
			}
		}
		await apply(acts[f.key], f.patch(v), [f.path]);
	}

	function commitText(f: TextField) {
		const v = (textValues[f.key] ?? '').trim();
		textValues[f.key] = v;
		if (eff && v === f.get(eff)) return;
		apply(acts[f.key], f.patch(v), [f.path]);
	}

	onMount(() => {
		if (!authStore.hasPermission('settings', 'read')) {
			goto(resolve('/admin'));
			return;
		}
		load();
	});
</script>

{#snippet boolRow(f: BoolField)}
	<FormField
		label={f.label}
		layout="toggle"
		help={lockHelp(f.path, f.help)}
		tag={acts[f.key].tag}
		error={acts[f.key].error}
	>
		<Switch
			checked={boolValues[f.key] ?? false}
			disabled={!canEdit || acts[f.key].busy || locked(f.path)}
			onCheckedChange={(v) => commitBool(f, v)}
		/>
	</FormField>
{/snippet}

{#snippet numRow(f: NumField)}
	<FormField
		label={f.label}
		id={f.key}
		layout="inline"
		help={lockHelp(f.path, f.help)}
		tag={acts[f.key].tag}
		error={acts[f.key].error}
	>
		<UnitInput
			id={f.key}
			unit={f.unit}
			bind:value={numValues[f.key]}
			min={f.min}
			max={f.max}
			class="w-32"
			disabled={!canEdit || acts[f.key].busy || locked(f.path)}
			onblur={() => commitNum(f)}
			onkeydown={(e) => {
				if (e.key === 'Enter') commitNum(f);
			}}
		/>
	</FormField>
{/snippet}

{#snippet textRow(f: TextField)}
	<FormField
		label={f.label}
		id={f.key}
		layout="inline"
		help={lockHelp(f.path, f.help)}
		tag={acts[f.key].tag}
		error={acts[f.key].error}
	>
		<Input
			id={f.key}
			bind:value={textValues[f.key]}
			placeholder={f.placeholder}
			class="w-40 sm:w-56"
			disabled={!canEdit || acts[f.key].busy || locked(f.path)}
			onblur={() => commitText(f)}
			onkeydown={(e) => {
				if (e.key === 'Enter') commitText(f);
			}}
		/>
	</FormField>
{/snippet}

{#if loading}
	<div class="space-y-6">
		<Skeleton class="h-52 w-full rounded-lg" />
		<Skeleton class="h-52 w-full rounded-lg" />
		<Skeleton class="h-40 w-full rounded-lg" />
	</div>
{:else if loadError}
	<div class="flex flex-col items-center rounded-lg border bg-card px-6 py-10 text-center">
		<p class="text-sm text-destructive">{loadError}</p>
		<Button variant="outline" size="sm" class="mt-3" onclick={load}>Retry</Button>
	</div>
{:else if eff}
	<FormPage
		level="section"
		title="Git"
		description="Repository hosting, SSH, storage, and workflow policy. Changes apply immediately."
	>
		<FormSection
			title="Hosting"
			description="Repository hosting and defaults for new repositories."
		>
			{@render boolRow(bools.enabled)}
			{@render boolRow(bools.privateByDefault)}
			{@render textRow(texts.defaultBranch)}
			{@render boolRow(bools.allowForks)}
			{@render boolRow(bools.releaseOnTag)}
			{@render numRow(nums.maxRepoSizeMb)}
		</FormSection>

		<FormSection
			title="SSH"
			description="Git over SSH with user and deploy keys, off until enabled here."
		>
			{@render boolRow(bools.sshEnabled)}
			{@render numRow(nums.sshPort)}
			{@render textRow(texts.sshPublicHost)}
			{#snippet footer()}
				<div class="flex min-w-0 flex-1 items-center gap-2 text-sm">
					{#if !boolValues.sshEnabled}
						<span class="status-dot bg-muted-foreground/40"></span>
						<span class="text-muted-foreground"
							>Off. Clone over SSH and deploy keys are hidden until enabled.</span
						>
					{:else if sshChecking && !sshStatus}
						<Loader2 class="size-3.5 animate-spin text-muted-foreground" />
						<span class="text-muted-foreground">Checking port</span>
					{:else if sshStatus?.listening}
						<span class="status-dot bg-success"></span>
						<span
							>Listening on port <span class="font-mono tabular-nums">{sshStatus.port}</span></span
						>
					{:else if sshStatus && !sshStatus.ok}
						<span class="status-dot bg-destructive"></span>
						<span class={cn('wrap-break-word text-destructive')}>{sshStatus.message}</span>
					{:else if sshStatus}
						<span class="status-dot bg-warning"></span>
						<span class="text-muted-foreground"
							>Port <span class="font-mono tabular-nums">{sshStatus.port}</span> is free but not bound
							yet, the listener opens shortly.</span
						>
					{/if}
				</div>
				<Button
					variant="outline"
					size="sm"
					onclick={refreshSSHStatus}
					disabled={sshChecking || !boolValues.sshEnabled}
				>
					{#if sshChecking}
						<Loader2 class="animate-spin" />
					{:else}
						<RadioTower />
					{/if}
					Check port
				</Button>
			{/snippet}
		</FormSection>

		<FormSection
			title="Storage and transfers"
			description="Large file support and concurrency limits."
		>
			{@render boolRow(bools.lfsEnabled)}
			{@render numRow(nums.maxLfsObjectMb)}
			{@render numRow(nums.maxConcurrentTransfers)}
		</FormSection>

		<FormSection title="Instance" description="What clients see and where repositories live.">
			{#if info}
				<div class="divide-y text-sm">
					<div class="pb-3">
						<p class="font-medium">SSH host keys</p>
						{#if info.sshHostKeyFingerprints.length === 0}
							<p class="mt-0.5 text-muted-foreground">
								Git over SSH is not wired on this instance.
							</p>
						{:else}
							<ul class="mt-1 space-y-0.5">
								{#each info.sshHostKeyFingerprints as fp (fp)}
									<li class="font-mono text-xs break-all">{fp}</li>
								{/each}
							</ul>
						{/if}
					</div>
					<div class="py-3">
						<div class="flex items-center justify-between gap-3">
							<p class="font-medium">Signing public key</p>
							{#if info.signingPublicKey}
								<Button
									variant="ghost-muted"
									size="sm"
									onclick={() => (showSigningKey = !showSigningKey)}
									>{showSigningKey ? 'Hide' : 'Show'}</Button
								>
							{/if}
						</div>
						{#if !info.signingPublicKey}
							<p class="mt-0.5 text-muted-foreground">
								Generated the first time the instance signs a commit.
							</p>
						{:else if showSigningKey}
							<pre class="code-inline mt-1 whitespace-pre-wrap">{info.signingPublicKey}</pre>
						{:else}
							<p class="mt-0.5 text-muted-foreground">
								Import it to verify web commits and merges made here.
							</p>
						{/if}
					</div>
					<div class="pt-3">
						<p class="font-medium">Storage</p>
						<p class="mt-0.5 font-mono text-xs break-all">{info.storagePath}</p>
						<p class="mt-0.5 text-muted-foreground">
							{info.repositoryCount} repositor{info.repositoryCount === 1n ? 'y' : 'ies'}, {formatBytes(
								Number(info.repositoryBytes)
							)} of objects, {formatBytes(Number(info.lfsBytes))} of LFS
						</p>
					</div>
				</div>
			{:else}
				<Skeleton class="h-20 w-full" />
			{/if}
		</FormSection>

		<FormSection title="Maintenance" description="Scheduled repack and prune.">
			{@render boolRow(bools.gcEnabled)}
			{@render numRow(nums.gcIntervalHours)}
		</FormSection>

		<FormSection title="Signing" description="Instance key for commits made through the web.">
			{@render boolRow(bools.signingEnabled)}
		</FormSection>

		<FormSection
			title="Actions"
			description="Workflow execution policy, organizations may enable their own."
		>
			{@render boolRow(bools.actionsEnabled)}
			{@render boolRow(bools.runOnMirror)}
			{@render textRow(texts.defaultRunsOn)}
			{@render numRow(nums.artifactRetentionDays)}
			{@render numRow(nums.logRetentionDays)}
		</FormSection>
	</FormPage>
{/if}

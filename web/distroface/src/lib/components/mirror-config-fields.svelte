<script lang="ts" module>
	import type { MirrorConfig } from '$lib/proto/distroface/v1/types_pb';

	export type MirrorKind = 'github' | 'gitlab' | 'gitea' | 'oci' | 'git';

	export type MirrorForm = {
		upstream: string;
		authToken: string;
		clearToken: boolean;
		username: string;
		pattern: string;
		includePrereleases: boolean;
		// Undefined keeps everything
		syncDepth: number | undefined;
		syncIntervalMinutes: number;
		paused: boolean;
		// Git mirrors copy upstream releases and their files
		mirrorReleases: boolean;
	};

	export function emptyMirrorForm(): MirrorForm {
		return {
			upstream: '',
			authToken: '',
			clearToken: false,
			username: '',
			pattern: '',
			includePrereleases: false,
			syncDepth: undefined,
			syncIntervalMinutes: 0,
			paused: false,
			mirrorReleases: false
		};
	}

	export function mirrorFormFrom(cfg?: MirrorConfig): MirrorForm {
		const form = emptyMirrorForm();
		if (!cfg) return form;
		form.upstream = cfg.upstream;
		form.username = cfg.username;
		form.pattern = cfg.pattern;
		form.includePrereleases = cfg.includePrereleases;
		form.syncDepth = cfg.syncDepth > 0 ? cfg.syncDepth : undefined;
		form.syncIntervalMinutes = cfg.syncIntervalMinutes;
		form.paused = cfg.paused;
		form.mirrorReleases = cfg.mirrorReleases;
		return form;
	}

	// Blank token is omitted so the server keeps the stored one
	export function mirrorInit(form: MirrorForm) {
		return {
			upstream: form.upstream.trim(),
			...(form.authToken
				? { authToken: form.authToken }
				: form.clearToken
					? { authToken: '' }
					: {}),
			username: form.username.trim(),
			pattern: form.pattern.trim(),
			includePrereleases: form.includePrereleases,
			syncDepth: Math.max(0, Math.trunc(Number(form.syncDepth) || 0)),
			syncIntervalMinutes: Math.max(0, Math.trunc(Number(form.syncIntervalMinutes) || 0)),
			paused: form.paused,
			mirrorReleases: form.mirrorReleases
		};
	}
</script>

<script lang="ts">
	import { onMount } from 'svelte';
	import { Input } from '$lib/components/ui/input';
	import { Switch } from '$lib/components/ui/switch';
	import { Button } from '$lib/components/ui/button';
	import { Alert, AlertTitle, AlertDescription } from '$lib/components/ui/alert';
	import FormField from '$lib/components/form-field.svelte';
	import PasswordInput from '$lib/components/password-input.svelte';
	import LimitInput from '$lib/components/limit-input.svelte';
	import DurationInput from '$lib/components/duration-input.svelte';
	import { CheckCircle, AlertTriangle, Hourglass, Undo } from '$lib/icons';
	import { relativeTime } from '$lib/utils';
	import { fetchMirrorLimits, type MirrorLimits } from '$lib/mirror';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import type { Timestamp } from '@bufbuild/protobuf/wkt';

	let {
		form,
		kind,
		tokenSet = false,
		lastSync,
		lastError = '',
		nextAttempt,
		idPrefix = 'mirror',
		part = 'all'
	}: {
		form: MirrorForm;
		kind: MirrorKind;
		tokenSet?: boolean;
		lastSync?: Timestamp;
		lastError?: string;
		nextAttempt?: Timestamp;
		idPrefix?: string;
		part?: 'source' | 'sync' | 'all';
	} = $props();

	const cooldownActive = $derived(
		!!nextAttempt && timestampDate(nextAttempt).getTime() > Date.now()
	);

	const copy = $derived(
		{
			github: {
				upstreamLabel: 'GitHub repository',
				upstreamPlaceholder: 'nickheyer/distroface',
				upstreamHelp: 'Owner/repo or URL',
				tokenHelp: 'For private repos and higher rate limits',
				patternLabel: 'Asset filter',
				patternHelp: 'Glob over asset names, empty mirrors all',
				depthUnit: 'releases',
				depthHelp: 'Newest releases kept per sync',
				syncTarget: 'GitHub'
			},
			gitlab: {
				upstreamLabel: 'GitLab project',
				upstreamPlaceholder: 'inkscape/inkscape',
				upstreamHelp: 'Owner/repo or URL',
				tokenHelp: 'read_api scope, for private projects',
				patternLabel: 'Asset filter',
				patternHelp: 'Glob over asset names, empty mirrors all',
				depthUnit: 'releases',
				depthHelp: 'Newest releases kept per sync',
				syncTarget: 'GitLab'
			},
			gitea: {
				upstreamLabel: 'Gitea / Forgejo repository',
				upstreamPlaceholder: 'forgejo/forgejo',
				upstreamHelp: 'Owner/repo or URL',
				tokenHelp: 'For private repos',
				patternLabel: 'Asset filter',
				patternHelp: 'Glob over asset names, empty mirrors all',
				depthUnit: 'releases',
				depthHelp: 'Newest releases kept per sync',
				syncTarget: 'Gitea'
			},
			oci: {
				upstreamLabel: 'Upstream image',
				upstreamPlaceholder: 'ghcr.io/nickheyer/distroface',
				upstreamHelp: 'Owner/repo (docker hub) or URL',
				tokenHelp: 'Basic auth with the username',
				patternLabel: 'Tag filter',
				patternHelp: 'Glob over tags, empty mirrors all',
				depthUnit: 'tags',
				depthHelp: 'Newest tags kept per sync',
				syncTarget: 'the upstream'
			},
			git: {
				upstreamLabel: 'Upstream repository',
				upstreamPlaceholder: 'https://github.com/nickheyer/distroface.git',
				upstreamHelp: 'https or ssh clone URL, owner/repo works for GitHub, GitLab, and Gitea',
				tokenHelp: 'Sent as basic auth for private https upstreams',
				patternLabel: 'Ref filter',
				patternHelp: 'Glob over branch and tag names, empty mirrors all',
				depthUnit: 'refs',
				depthHelp: 'Ignored for git mirrors',
				syncTarget: 'the upstream'
			}
		}[kind]
	);

	let limits = $state<MirrorLimits | null>(null);
	onMount(async () => {
		if (part !== 'source') limits = await fetchMirrorLimits();
	});

	const minInterval = $derived(limits?.minIntervalMinutes ?? 0);
	const depthCap = $derived(limits?.maxSyncDepth ?? 0);

	// Zero means system default, show the real number instead
	$effect(() => {
		const def = limits?.defaultIntervalMinutes ?? 0;
		if (def > 0 && form.syncIntervalMinutes === 0) form.syncIntervalMinutes = def;
	});

	const intervalError = $derived.by(() => {
		const v = Math.trunc(Number(form.syncIntervalMinutes) || 0);
		return minInterval > 0 && v > 0 && v < minInterval ? `Minimum ${minInterval} min` : '';
	});

	const depthHelp = $derived(
		depthCap > 0 ? `${copy.depthHelp}, at most ${depthCap}` : copy.depthHelp
	);
</script>

<div class="form-fields">
	{#if part !== 'sync'}
		{#if lastSync || lastError || cooldownActive}
			<Alert variant={lastError ? 'destructive' : cooldownActive ? 'warning' : 'success'}>
				{#if lastError}
					<AlertTriangle />
				{:else}
					<CheckCircle />
				{/if}
				<AlertTitle class="flex items-center justify-between gap-2">
					<span>{lastError ? 'Last sync failed' : 'Last sync succeeded'}</span>
					{#if lastSync}
						<span class="font-normal text-muted-foreground"
							>{relativeTime(timestampDate(lastSync))}</span
						>
					{/if}
				</AlertTitle>
				{#if lastError || (cooldownActive && nextAttempt)}
					<AlertDescription>
						{#if lastError}
							<p class="wrap-break-word">{lastError}</p>
						{/if}
						{#if cooldownActive && nextAttempt}
							<p class="flex items-center gap-1.5">
								<Hourglass class="size-3.5 shrink-0" />
								Backing off, next attempt after {timestampDate(nextAttempt).toLocaleString()}
							</p>
						{/if}
					</AlertDescription>
				{/if}
			</Alert>
		{/if}

		<FormField
			label={copy.upstreamLabel}
			id="{idPrefix}-upstream"
			required
			help={copy.upstreamHelp}
		>
			<Input
				id="{idPrefix}-upstream"
				name="{idPrefix}-upstream"
				bind:value={form.upstream}
				placeholder={copy.upstreamPlaceholder}
				autocomplete="off"
				data-1p-ignore
				data-lpignore="true"
				data-bwignore
			/>
		</FormField>

		{#if kind === 'oci' || kind === 'git'}
			<FormField label="Username" id="{idPrefix}-username" span="half">
				<Input
					id="{idPrefix}-username"
					name="{idPrefix}-username"
					bind:value={form.username}
					placeholder="username"
					autocomplete="off"
					data-1p-ignore
					data-lpignore="true"
					data-bwignore
				/>
			</FormField>
		{/if}

		<FormField
			label="Access token"
			id="{idPrefix}-token"
			help={form.clearToken
				? 'Stored token clears on save'
				: tokenSet
					? 'Blank keeps the stored token'
					: copy.tokenHelp}
		>
			<div class="flex items-center gap-2">
				<div class="min-w-0 flex-1">
					<PasswordInput
						id="{idPrefix}-token"
						name="{idPrefix}-token"
						bind:value={form.authToken}
						placeholder={form.clearToken
							? 'Token will be cleared'
							: tokenSet
								? '••••••••  (stored)'
								: 'Token'}
						autocomplete="new-password"
						disabled={form.clearToken}
						data-1p-ignore
						data-lpignore="true"
						data-bwignore
					/>
				</div>
				{#if tokenSet}
					{#if form.clearToken}
						<Button variant="outline" class="shrink-0" onclick={() => (form.clearToken = false)}>
							<Undo />Keep token
						</Button>
					{:else}
						<Button
							variant="outline"
							class="shrink-0"
							onclick={() => {
								form.clearToken = true;
								form.authToken = '';
							}}
						>
							Clear token
						</Button>
					{/if}
				{/if}
			</div>
		</FormField>
	{/if}

	{#if part !== 'source'}
		<FormField
			label={copy.patternLabel}
			id="{idPrefix}-pattern"
			help={copy.patternHelp}
			span="half"
		>
			<Input id="{idPrefix}-pattern" bind:value={form.pattern} placeholder="*" class="font-mono" />
		</FormField>

		{#if kind !== 'oci' && kind !== 'git'}
			<FormField label="Include prereleases" layout="toggle">
				<Switch bind:checked={form.includePrereleases} />
			</FormField>
		{/if}

		{#if kind !== 'git'}
			<FormField label="Sync depth" id="{idPrefix}-depth" help={depthHelp} span="half">
				<LimitInput
					id="{idPrefix}-depth"
					label="sync depth"
					unit={copy.depthUnit}
					bind:value={form.syncDepth}
					max={depthCap > 0 ? depthCap : undefined}
					seed={depthCap > 0 ? Math.min(10, depthCap) : 10}
				/>
			</FormField>
		{/if}
		<FormField
			label="Sync interval"
			id="{idPrefix}-interval"
			help="Mirror sync frequency with {copy.syncTarget}"
			error={intervalError}
			span="third"
		>
			<DurationInput
				id="{idPrefix}-interval"
				min={minInterval > 0 ? minInterval : 0}
				unit="minutes"
				interval
				bind:value={form.syncIntervalMinutes}
				aria-invalid={!!intervalError}
				class="w-full"
			/>
		</FormField>

		{#if kind === 'git'}
			<FormField
				label="Mirror releases"
				layout="toggle"
				help="Copies upstream releases and their asset files on every sync"
			>
				<Switch bind:checked={form.mirrorReleases} />
			</FormField>
		{/if}

		<FormField label="Paused" layout="toggle" help="Skips scheduled syncs">
			<Switch bind:checked={form.paused} />
		</FormField>
	{/if}
</div>

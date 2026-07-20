<script lang="ts" module>
	import type { MirrorConfig } from '$lib/proto/distroface/v1/types_pb';

	export type MirrorKind = 'github' | 'gitlab' | 'gitea' | 'oci';

	export type MirrorForm = {
		upstream: string;
		authToken: string;
		clearToken: boolean;
		username: string;
		pattern: string;
		includePrereleases: boolean;
		syncDepth: number;
		syncIntervalMinutes: number;
		paused: boolean;
	};

	export function emptyMirrorForm(): MirrorForm {
		return {
			upstream: '',
			authToken: '',
			clearToken: false,
			username: '',
			pattern: '',
			includePrereleases: false,
			syncDepth: 0,
			syncIntervalMinutes: 0,
			paused: false
		};
	}

	export function mirrorFormFrom(cfg?: MirrorConfig): MirrorForm {
		const form = emptyMirrorForm();
		if (!cfg) return form;
		form.upstream = cfg.upstream;
		form.username = cfg.username;
		form.pattern = cfg.pattern;
		form.includePrereleases = cfg.includePrereleases;
		form.syncDepth = cfg.syncDepth;
		form.syncIntervalMinutes = cfg.syncIntervalMinutes;
		form.paused = cfg.paused;
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
			paused: form.paused
		};
	}
</script>

<script lang="ts">
	import { onMount } from 'svelte';
	import { Input } from '$lib/components/ui/input';
	import { Switch } from '$lib/components/ui/switch';
	import { Button } from '$lib/components/ui/button';
	import FormField from '$lib/components/form-field.svelte';
	import PasswordInput from '$lib/components/password-input.svelte';
	import UnitInput from '$lib/components/unit-input.svelte';
	import { CheckCircle, AlertTriangle, Hourglass, Undo2 } from '@lucide/svelte';
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
		idPrefix = 'mirror'
	}: {
		form: MirrorForm;
		kind: MirrorKind;
		tokenSet?: boolean;
		lastSync?: Timestamp;
		lastError?: string;
		nextAttempt?: Timestamp;
		idPrefix?: string;
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
				depthHelp: 'Number of releases to keep, 0 for all',
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
				depthHelp: 'Number of releases to keep, 0 for all',
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
				depthHelp: 'Number of releases to keep, 0 for all',
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
				depthHelp: 'Number of tags to keep, 0 for all',
				syncTarget: 'the upstream'
			}
		}[kind]
	);

	let limits = $state<MirrorLimits | null>(null);
	onMount(async () => {
		limits = await fetchMirrorLimits();
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
		depthCap > 0 ? `Set to 0 to keep the newest ${depthCap} ${copy.depthUnit}` : copy.depthHelp
	);
</script>

<div class="space-y-3">
	{#if lastSync || lastError || cooldownActive}
		<div class="rounded-lg border border-border/50 bg-muted/20 px-3 py-2.5 text-[13px] space-y-1">
			<div class="flex items-center gap-1.5">
				{#if lastError}
					<AlertTriangle class="h-3.5 w-3.5 text-destructive shrink-0" />
					<span class="font-medium text-destructive">Last sync failed</span>
				{:else}
					<CheckCircle class="h-3.5 w-3.5 text-emerald-500 shrink-0" />
					<span class="font-medium">Last sync succeeded</span>
				{/if}
				{#if lastSync}
					<span class="text-muted-foreground ml-auto">{relativeTime(timestampDate(lastSync))}</span>
				{/if}
			</div>
			{#if lastError}
				<p class="text-xs text-muted-foreground wrap-break-word">{lastError}</p>
			{/if}
			{#if cooldownActive && nextAttempt}
				<div class="flex items-center gap-1.5 text-amber-600 dark:text-amber-400">
					<Hourglass class="h-3.5 w-3.5 shrink-0" />
					<span class="text-xs">Backing off, next attempt after {timestampDate(nextAttempt).toLocaleString()}</span>
				</div>
			{/if}
		</div>
	{/if}

	<FormField label={copy.upstreamLabel} id="{idPrefix}-upstream" required help={copy.upstreamHelp}>
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

	{#if kind === 'oci'}
		<FormField label="Username" id="{idPrefix}-username">
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
			<div class="flex-1 min-w-0">
				<PasswordInput
					id="{idPrefix}-token"
					name="{idPrefix}-token"
					bind:value={form.authToken}
					placeholder={form.clearToken ? 'Token will be cleared' : tokenSet ? '••••••••  (stored)' : 'Token'}
					autocomplete="new-password"
					disabled={form.clearToken}
					data-1p-ignore
					data-lpignore="true"
					data-bwignore
				/>
			</div>
			{#if tokenSet}
				{#if form.clearToken}
					<Button variant="outline" size="sm" class="h-9 shrink-0" onclick={() => (form.clearToken = false)}>
						<Undo2 class="h-3.5 w-3.5 mr-1.5" />Keep token
					</Button>
				{:else}
					<Button
						variant="outline" size="sm" class="h-9 shrink-0"
						onclick={() => { form.clearToken = true; form.authToken = ''; }}
					>
						Clear token
					</Button>
				{/if}
			{/if}
		</div>
	</FormField>

	<FormField label={copy.patternLabel} id="{idPrefix}-pattern" help={copy.patternHelp}>
		<Input id="{idPrefix}-pattern" bind:value={form.pattern} placeholder="*" class="font-mono text-sm" />
	</FormField>

	{#if kind !== 'oci'}
		<FormField label="Include prereleases" horizontal>
			<Switch bind:checked={form.includePrereleases} />
		</FormField>
	{/if}

	<div class="grid grid-cols-2 gap-3">
		<FormField label="Sync depth" id="{idPrefix}-depth" help={depthHelp}>
			<UnitInput
				id="{idPrefix}-depth"
				min={0}
				max={depthCap > 0 ? depthCap : undefined}
				unit={copy.depthUnit}
				bind:value={form.syncDepth}
			/>
		</FormField>
		<FormField
			label="Sync interval"
			id="{idPrefix}-interval"
			help="Mirror sync frequency with {copy.syncTarget}"
			error={intervalError}
		>
			<UnitInput
				id="{idPrefix}-interval"
				min={minInterval > 0 ? minInterval : 0}
				unit="min"
				bind:value={form.syncIntervalMinutes}
			/>
		</FormField>
	</div>

	<FormField label="Paused" horizontal help="Skips scheduled syncs">
		<Switch bind:checked={form.paused} />
	</FormField>
</div>

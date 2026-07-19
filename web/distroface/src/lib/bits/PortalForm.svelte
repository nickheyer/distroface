<script module lang="ts">
	import type { CertSource as CertSourceT } from '$lib/proto/distroface/v1/certificate_pb';

	export interface PortalFields {
		name: string;
		hostname: string;
		port: number;
		mapUnqualified: boolean;
		rules: { pattern: string; replace: string }[];
		allowPush: boolean;
		requireAuth: boolean;
		tls: boolean;
		certSource: CertSourceT;
	}
</script>

<script lang="ts">
	import { CertSource } from '$lib/proto/distroface/v1/certificate_pb';
	import type { RegistryPortal } from '$lib/proto/distroface/v1/portal_pb';
	import { placementError, effectiveAddress, appHostPort } from '$lib/net';

	let {
		portal,
		busy = false,
		submitLabel,
		onsave
	}: {
		portal?: RegistryPortal;
		busy?: boolean;
		submitLabel: string;
		onsave: (f: PortalFields) => void | Promise<void>;
	} = $props();

	// Editable copy of the initial values, parent re-keys on change
	// svelte-ignore state_referenced_locally
	const given = portal;

	let name = $state(given?.name ?? '');
	let hostname = $state(given?.hostname ?? '');
	let port = $state(given?.port ? String(given.port) : '');
	let mapUnqualified = $state(given?.mapUnqualified ?? true);
	let rules = $state<{ pattern: string; replace: string }[]>(
		given?.rules.map((r) => ({ pattern: r.pattern, replace: r.replace })) ?? []
	);
	let allowPush = $state(given?.allowPush ?? false);
	let requireAuth = $state(given?.requireAuth ?? false);
	let tls = $state(given?.tls ?? false);
	let certSource = $state<CertSource>(
		given && given.certSource !== CertSource.UNSPECIFIED ? given.certSource : CertSource.NONE
	);

	const placeFault = $derived(placementError(hostname, port));
	const dialAddress = $derived(
		placeFault ? '' : effectiveAddress(hostname.trim().toLowerCase(), Number(port || '0'))
	);

	function addRule() {
		rules = [...rules, { pattern: '', replace: '' }];
	}

	function dropRule(i: number) {
		rules = rules.filter((_, j) => j !== i);
	}

	async function submit(e: Event) {
		e.preventDefault();
		if (placeFault || !name.trim()) return;
		await onsave({
			name: name.trim(),
			hostname: hostname.trim().toLowerCase(),
			port: Number(port || '0'),
			mapUnqualified,
			rules: rules.filter((r) => r.pattern.trim() !== ''),
			allowPush,
			requireAuth,
			tls,
			certSource
		});
	}
</script>

<form onsubmit={submit}>
	<label class="field">
		<span>Name</span>
		<input type="text" bind:value={name} required />
	</label>

	<div class="row">
		<label class="field" style="flex: 2; min-width: 16rem">
			<span>Hostname</span>
			<input type="text" bind:value={hostname} placeholder="registry.example.org" />
			<span class="hint">Empty answers any hostname arriving on the port.</span>
		</label>
		<label class="field" style="flex: 1; min-width: 8rem">
			<span>Port</span>
			<input type="text" bind:value={port} placeholder={appHostPort().port || 'main port'} />
			<span class="hint">Empty serves on the main port. Ports may be shared.</span>
		</label>
	</div>
	{#if placeFault && (hostname || port)}
		<p class="note wax-ink">† {placeFault}</p>
	{:else if dialAddress}
		<p class="note">Clients will dial <span class="mono">{dialAddress}</span>.</p>
	{/if}

	<fieldset class="field gap-top">
		<span>Name mapping</span>
		<label class="tick">
			<input type="checkbox" bind:checked={mapUnqualified} />
			Map unqualified names into the organization namespace
			<span class="hint">So that pulling <span class="mono">app</span> resolves to <span class="mono">org/app</span>.</span>
		</label>
	</fieldset>

	<fieldset class="field" style="max-width: 44rem">
		<span>Rewrite rules</span>
		{#if rules.length === 0}
			<p class="note">None. Custom rules run before unqualified mapping, first match wins.</p>
		{/if}
		{#each rules as rule, i (i)}
			<div class="row" style="margin-bottom: 0.4rem">
				<input
					type="text"
					style="flex: 1; min-width: 10rem"
					placeholder="pattern, anchored regex"
					bind:value={rule.pattern}
				/>
				<span class="mono faint">→</span>
				<input
					type="text"
					style="flex: 1; min-width: 10rem"
					placeholder="replacement, $1 works"
					bind:value={rule.replace}
				/>
				<button class="rowact plain" type="button" onclick={() => dropRule(i)}>drop</button>
			</div>
		{/each}
		<button class="rowact" type="button" onclick={addRule}>add rule</button>
	</fieldset>

	<fieldset class="field gap-top">
		<span>Access</span>
		<label class="tick">
			<input type="checkbox" bind:checked={allowPush} />
			Accept pushes
			<span class="hint">Unticked keeps the portal read-only.</span>
		</label>
		<label class="tick">
			<input type="checkbox" bind:checked={requireAuth} />
			Require credentials for every request
			<span class="hint">Including pulls of public repositories.</span>
		</label>
	</fieldset>

	<fieldset class="field">
		<span>Certificate source</span>
		<select bind:value={certSource} style="max-width: 18rem">
			<option value={CertSource.NONE}>cleartext, no certificate</option>
			<option value={CertSource.ACME}>ACME issuance</option>
			<option value={CertSource.ORG_CA}>issued from the org CA</option>
			<option value={CertSource.ORG_CERT}>the org's shared certificate</option>
			<option value={CertSource.MANUAL}>uploaded for this portal</option>
		</select>
		<label class="tick" style="margin-top: 0.6rem">
			<input type="checkbox" bind:checked={tls} disabled={certSource === CertSource.NONE} />
			Enforce https
			<span class="hint">Cleartext requests are redirected once a certificate serves.</span>
		</label>
	</fieldset>

	<div class="row gap-top">
		<button class="act wax" type="submit" disabled={busy || !!placeFault || !name.trim()}
			>{submitLabel}</button>
	</div>
</form>

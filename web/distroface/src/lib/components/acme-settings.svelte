<script lang="ts">
	import { onMount } from 'svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Switch } from '$lib/components/ui/switch';
	import { Input } from '$lib/components/ui/input';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import PasswordInput from '$lib/components/password-input.svelte';
	import PemInput from '$lib/components/pem-input.svelte';
	import { Loader } from '$lib/icons';
	import { Act, errText } from '$lib/act.svelte';
	import {
		isLocked,
		patchSettings,
		tierOf,
		type ScopeInit,
		type SettingsPatch
	} from '$lib/settings-utils';
	import {
		ACMEChallengeType,
		DNSProvider,
		SettingsScopeType,
		SettingsTier,
		type FieldProvenance,
		type Settings
	} from '$lib/proto/distroface/v1/settings_pb';

	let {
		scope,
		parentScope,
		deferred = false,
		onsaved
	}: {
		// Absent until the owner exists, commit then names it
		scope?: ScopeInit;
		// Tier the scope inherits from, absent at system
		parentScope?: ScopeInit;
		// Host form saves everything through commit
		deferred?: boolean;
		onsaved?: () => void;
	} = $props();

	type ACMEPatch = NonNullable<SettingsPatch['acme']>;
	type DNSPatch = NonNullable<ACMEPatch['dns']>;

	const P = {
		email: 'acme.email',
		directory: 'acme.directory_url',
		accountKey: 'acme.account_key',
		eabKeyId: 'acme.eab_key_id',
		eabHmacKey: 'acme.eab_hmac_key',
		caBundle: 'acme.ca_bundle',
		challenge: 'acme.challenge',
		provider: 'acme.dns.provider',
		server: 'acme.dns.server',
		tsigKeyName: 'acme.dns.tsig_key_name',
		tsigAlgorithm: 'acme.dns.tsig_algorithm',
		tsigSecret: 'acme.dns.tsig_secret',
		apiToken: 'acme.dns.api_token',
		resolvers: 'acme.dns.resolvers',
		propagation: 'acme.dns.propagation_timeout_seconds'
	} as const;
	const CREDENTIAL_PATHS = [P.accountKey, P.eabKeyId, P.eabHmacKey, P.caBundle];
	const ACCOUNT_PATHS = [P.email, P.directory, ...CREDENTIAL_PATHS];
	const DNS_PATHS = [
		P.provider,
		P.server,
		P.tsigKeyName,
		P.tsigAlgorithm,
		P.tsigSecret,
		P.apiToken,
		P.resolvers,
		P.propagation
	];
	const VALIDATION_PATHS = [P.challenge, ...DNS_PATHS];

	const challengeOptions = [
		{
			value: ACMEChallengeType.ACME_CHALLENGE_TYPE_UNSPECIFIED,
			label: 'Automatic',
			help: 'The CA picks HTTP-01 or TLS-ALPN-01, whichever this instance offers'
		},
		{
			value: ACMEChallengeType.ACME_CHALLENGE_TYPE_HTTP_01,
			label: 'HTTP-01',
			help: 'The CA fetches a token from the challenge port, reached as port 80'
		},
		{
			value: ACMEChallengeType.ACME_CHALLENGE_TYPE_TLS_ALPN_01,
			label: 'TLS-ALPN-01',
			help: 'The CA validates with a handshake on port 443'
		},
		{
			value: ACMEChallengeType.ACME_CHALLENGE_TYPE_DNS_01,
			label: 'DNS-01',
			help: 'Publishes a TXT record, needs no inbound port and is the only way to get wildcards'
		}
	];
	const providerOptions = [
		{ value: DNSProvider.DNS_PROVIDER_RFC2136, label: 'RFC 2136 dynamic update' },
		{ value: DNSProvider.DNS_PROVIDER_CLOUDFLARE, label: 'Cloudflare' }
	];

	const parentIsOrg = $derived(parentScope?.type === SettingsScopeType.ORG);
	const ownTier = $derived(
		!parentScope ? SettingsTier.SYSTEM : parentIsOrg ? SettingsTier.PORTAL : SettingsTier.ORG
	);
	const parentName = $derived(parentIsOrg ? 'organization' : 'instance');

	let loading = $state(true);
	let loadError = $state('');
	let prov = $state<FieldProvenance[]>([]);
	let inherited = $state<Settings | null>(null);

	const emailAct = new Act();
	const directoryAct = new Act();
	const credentialsAct = new Act();
	const validationAct = new Act();
	const resetAccountAct = new Act();
	const resetValidationAct = new Act();

	let email = $state('');
	let savedEmail = $state('');
	let directory = $state('');
	let savedDirectory = $state('');
	let useAccountKey = $state(false);
	let accountKey = $state('');
	let accountKeySet = $state(false);
	let useEab = $state(false);
	let eabKeyId = $state('');
	let eabHmacKey = $state('');
	let eabHmacKeySet = $state(false);
	let caBundle = $state('');

	let challenge = $state<ACMEChallengeType>(ACMEChallengeType.ACME_CHALLENGE_TYPE_UNSPECIFIED);
	let ownDns = $state(false);
	let dnsInherited = $state(false);
	let provider = $state<DNSProvider>(DNSProvider.DNS_PROVIDER_RFC2136);
	let server = $state('');
	let tsigKeyName = $state('');
	let tsigAlgorithm = $state('');
	let tsigSecret = $state('');
	let tsigSecretSet = $state(false);
	let apiToken = $state('');
	let apiTokenSet = $state(false);
	let resolvers = $state('');
	let propagation = $state(120);

	const overridden = (path: string) => !!parentScope && tierOf(prov, path) === ownTier;
	const locked = (path: string) => isLocked(prov, path);
	const tag = (path: string, act?: Act) => act?.tag ?? (overridden(path) ? 'Custom' : undefined);
	const help = (path: string, text: string) => (locked(path) ? 'Pinned by the config file' : text);

	const acmeOff = $derived(!!parentScope && !inherited?.acme?.enabled);
	// Credentials belong to whichever tier names the directory
	const credentialsLocked = $derived(
		!!parentScope && (deferred ? directory.trim() === '' : savedDirectory === '')
	);
	const dnsLocked = $derived(!!parentScope && !ownDns);
	// Inherited secrets stay behind when the block becomes own
	const secretKept = $derived(!(dnsInherited && ownDns));
	const dns01 = $derived(challenge === ACMEChallengeType.ACME_CHALLENGE_TYPE_DNS_01);
	const rfc2136 = $derived(provider === DNSProvider.DNS_PROVIDER_RFC2136);
	const challengeOption = $derived(
		challengeOptions.find((o) => o.value === challenge) ?? challengeOptions[0]
	);
	const providerLabel = $derived(
		providerOptions.find((o) => o.value === provider)?.label ?? 'Pick a provider'
	);
	const accountOverrides = $derived(ACCOUNT_PATHS.some(overridden));
	const validationOverrides = $derived(VALIDATION_PATHS.some(overridden));

	// Lower tiers edit their own value over the inherited placeholder
	const ownText = (path: string, effective: string | undefined) =>
		!parentScope || overridden(path) ? (effective ?? '') : '';

	// Everything the user can change, compared to spot edits
	const snapshot = () =>
		JSON.stringify([
			email,
			directory,
			useAccountKey,
			accountKey,
			useEab,
			eabKeyId,
			eabHmacKey,
			caBundle,
			challenge,
			ownDns,
			provider,
			server,
			tsigKeyName,
			tsigAlgorithm,
			tsigSecret,
			apiToken,
			resolvers,
			propagation
		]);
	let seeded = '';

	function seed(s: Settings, provenance: FieldProvenance[]) {
		prov = provenance;
		const a = s.acme;
		const d = a?.dns;
		email = savedEmail = ownText(P.email, a?.email);
		directory = savedDirectory = ownText(P.directory, a?.directoryUrl);
		// Inherited credentials never apply to a directory set here
		const own = !parentScope || overridden(P.directory) ? a : undefined;
		accountKey = '';
		accountKeySet = own?.accountKeySet ?? false;
		useAccountKey = accountKeySet;
		eabKeyId = own?.eabKeyId ?? '';
		eabHmacKey = '';
		eabHmacKeySet = own?.eabHmacKeySet ?? false;
		useEab = eabKeyId !== '' || eabHmacKeySet;
		caBundle = own?.caBundle ?? '';

		challenge = a?.challenge ?? ACMEChallengeType.ACME_CHALLENGE_TYPE_UNSPECIFIED;
		ownDns = overridden(P.provider);
		dnsInherited = !!parentScope && !ownDns;
		provider = d?.provider || DNSProvider.DNS_PROVIDER_RFC2136;
		server = d?.server ?? '';
		tsigKeyName = d?.tsigKeyName ?? '';
		tsigAlgorithm = d?.tsigAlgorithm ?? '';
		tsigSecret = '';
		tsigSecretSet = d?.tsigSecretSet ?? false;
		apiToken = '';
		apiTokenSet = d?.apiTokenSet ?? false;
		resolvers = (d?.resolvers ?? []).join(', ');
		propagation = d?.propagationTimeoutSeconds ?? 120;
		seeded = snapshot();
	}

	async function load() {
		loading = true;
		loadError = '';
		try {
			const [own, parent] = await Promise.all([
				scope ? rpcClient.settings.getEffectiveSettings({ scope }, silentCallOptions) : null,
				parentScope
					? rpcClient.settings.getEffectiveSettings({ scope: parentScope }, silentCallOptions)
					: null
			]);
			inherited = parent?.settings ?? null;
			// An owner not created yet starts from what it would inherit
			const start = own ?? parent;
			if (start?.settings) seed(start.settings, start.provenance);
		} catch (err) {
			loadError = errText(err);
		} finally {
			loading = false;
		}
	}

	type Patch = { acme: ACMEPatch; paths: string[] };

	// Masked paths missing from the patch clear back to inherited
	async function send(target: ScopeInit, { acme, paths }: Patch) {
		const res = await patchSettings(
			target,
			{ acme },
			paths.filter((p) => !locked(p))
		);
		if (res.effective) seed(res.effective, res.provenance);
		onsaved?.();
	}

	function apply(act: Act, patch: Patch) {
		if (scope) act.run(() => send(scope, patch));
	}

	// A lower tier clears the override when the field is emptied
	function textPatch(path: string, field: 'email' | 'directoryUrl', value: string): Patch {
		const patch: Patch = { acme: parentScope && !value ? {} : { [field]: value }, paths: [path] };
		// Credentials leave with the directory they were issued for
		if (path === P.directory && parentScope && !value) patch.paths.push(...CREDENTIAL_PATHS);
		return patch;
	}

	function commitText(act: Act, path: string, field: 'email' | 'directoryUrl', value: string) {
		const saved = field === 'email' ? savedEmail : savedDirectory;
		if (deferred || value === saved || locked(path)) return;
		apply(act, textPatch(path, field, value));
	}

	// Secrets left blank stay out of the mask and keep their stored value
	function credentialsPatch(): Patch {
		const acme: ACMEPatch = {};
		const paths: string[] = [P.eabKeyId, P.caBundle];
		if (!useAccountKey || accountKey.trim()) paths.push(P.accountKey);
		if (useAccountKey && accountKey.trim()) acme.accountKey = accountKey.trim();
		if (useEab) acme.eabKeyId = eabKeyId.trim();
		if (!useEab || eabHmacKey) paths.push(P.eabHmacKey);
		if (useEab && eabHmacKey) acme.eabHmacKey = eabHmacKey;
		if (caBundle.trim()) acme.caBundle = caBundle.trim();
		return { acme, paths };
	}

	function validationPatch(): Patch {
		const acme: ACMEPatch = {};
		const paths: string[] = [P.challenge];
		const inheritedChallenge =
			inherited?.acme?.challenge ?? ACMEChallengeType.ACME_CHALLENGE_TYPE_UNSPECIFIED;
		if (!parentScope || challenge !== inheritedChallenge) acme.challenge = challenge;

		if (dns01 && dnsLocked) {
			paths.push(...DNS_PATHS);
		} else if (dns01) {
			const dns: DNSPatch = {
				provider,
				resolvers: resolvers
					.split(',')
					.map((r) => r.trim())
					.filter((r) => r !== ''),
				propagationTimeoutSeconds: Math.max(0, Math.round(Number(propagation) || 0))
			};
			paths.push(P.provider, P.resolvers, P.propagation, P.server, P.tsigKeyName, P.tsigAlgorithm);
			if (rfc2136) {
				dns.server = server.trim();
				dns.tsigKeyName = tsigKeyName.trim();
				if (tsigAlgorithm.trim()) dns.tsigAlgorithm = tsigAlgorithm.trim();
				if (tsigSecret) dns.tsigSecret = tsigSecret;
				paths.push(P.apiToken);
				if (tsigSecret) paths.push(P.tsigSecret);
			} else {
				if (apiToken) dns.apiToken = apiToken;
				paths.push(P.tsigSecret);
				if (apiToken) paths.push(P.apiToken);
			}
			acme.dns = dns;
		}
		return { acme, paths };
	}

	// One save for a host form, rejects with the server's reason
	export async function commit(target: ScopeInit) {
		if (loading || loadError || snapshot() === seeded) return;
		const parts = [
			textPatch(P.email, 'email', email.trim()),
			textPatch(P.directory, 'directoryUrl', directory.trim()),
			credentialsLocked ? { acme: {}, paths: CREDENTIAL_PATHS } : credentialsPatch(),
			validationPatch()
		];
		await send(target, {
			acme: Object.assign({}, ...parts.map((p) => p.acme)),
			paths: [...new Set(parts.flatMap((p) => p.paths))]
		});
	}

	onMount(load);
</script>

{#snippet saveButton(act: Act, label: string, onclick: () => void)}
	<Button variant="outline" disabled={act.busy} {onclick}>
		{#if act.busy}
			<Loader class="animate-spin" />
		{/if}
		{label}
	</Button>
	{#if act.error}
		<p class="text-sm text-destructive">{act.error}</p>
	{:else if act.saved}
		<p class="text-sm text-success">Saved</p>
	{/if}
{/snippet}

{#snippet resetButton(act: Act, paths: string[])}
	<Button variant="ghost-muted" disabled={act.busy} onclick={() => apply(act, { acme: {}, paths })}>
		{#if act.busy}
			<Loader class="animate-spin" />
		{/if}
		Reset to inherited
	</Button>
	{#if act.error}
		<p class="text-sm text-destructive">{act.error}</p>
	{/if}
{/snippet}

{#if loading}
	<Skeleton class="h-64 w-full rounded-lg" />
{:else if loadError}
	<div class="flex flex-col items-center rounded-lg border bg-card px-6 py-10 text-center">
		<p class="text-sm text-destructive">{loadError}</p>
		<Button variant="outline" size="sm" class="mt-3" onclick={load}>Retry</Button>
	</div>
{:else}
	<FormSection
		title="ACME account"
		description={acmeOff
			? 'ACME is turned off at the instance level, these values apply once an admin enables it.'
			: deferred
				? `Account this portal orders with. Empty fields inherit from the ${parentName}.`
				: parentScope
					? `Account this scope orders with. Empty fields inherit from the ${parentName}, email and directory apply on blur.`
					: 'Account certificates are ordered with. Email and directory apply on blur.'}
	>
		<FormField
			label="Account email"
			id="acme-email"
			span="half"
			help={help(
				P.email,
				parentScope
					? `Empty inherits the ${parentName} value`
					: 'Receives expiry notices from the CA'
			)}
			tag={tag(P.email, emailAct)}
			error={emailAct.error}
		>
			<Input
				id="acme-email"
				bind:value={email}
				placeholder={inherited?.acme?.email || 'admin@example.com'}
				disabled={emailAct.busy || locked(P.email)}
				onblur={() => commitText(emailAct, P.email, 'email', email.trim())}
				onkeydown={(e) => {
					if (e.key === 'Enter') commitText(emailAct, P.email, 'email', email.trim());
				}}
			/>
		</FormField>
		<FormField
			label="Directory URL"
			id="acme-directory"
			span="half"
			help={help(
				P.directory,
				parentScope
					? `Empty inherits the ${parentName} value`
					: 'Empty uses Lets Encrypt production'
			)}
			tag={tag(P.directory, directoryAct)}
			error={directoryAct.error}
		>
			<Input
				id="acme-directory"
				bind:value={directory}
				class="font-mono"
				placeholder={inherited?.acme?.directoryUrl ||
					'https://acme-v02.api.letsencrypt.org/directory'}
				disabled={directoryAct.busy || locked(P.directory)}
				onblur={() => commitText(directoryAct, P.directory, 'directoryUrl', directory.trim())}
				onkeydown={(e) => {
					if (e.key === 'Enter')
						commitText(directoryAct, P.directory, 'directoryUrl', directory.trim());
				}}
			/>
		</FormField>

		{#if credentialsLocked}
			<p class="text-sm text-muted-foreground">
				Account credentials follow the directory. Set a directory URL here to give this scope its
				own account key, external account binding and CA bundle.
			</p>
		{:else}
			<FormField
				label="Existing account"
				layout="toggle"
				tag={tag(P.accountKey)}
				help={help(
					P.accountKey,
					'Orders with an account already registered at the CA, off registers a new one'
				)}
			>
				<Switch bind:checked={useAccountKey} disabled={locked(P.accountKey)} />
			</FormField>
			{#if useAccountKey}
				<div class="form-subfields">
					<FormField
						label="Account key"
						id="acme-account-key"
						help={accountKeySet
							? 'A key is stored, leave blank to keep it'
							: 'Private key as PEM, base64 DER or JWK, never shown again'}
					>
						<PemInput
							id="acme-account-key"
							bind:value={accountKey}
							secret
							placeholder={accountKeySet ? 'Unchanged' : '-----BEGIN PRIVATE KEY-----'}
							disabled={locked(P.accountKey)}
						/>
					</FormField>
				</div>
			{/if}
			<FormField
				label="External account binding"
				layout="toggle"
				tag={tag(P.eabKeyId)}
				help={help(
					P.eabKeyId,
					'For a CA that issues a key id and HMAC key before it accepts registrations'
				)}
			>
				<Switch bind:checked={useEab} disabled={locked(P.eabKeyId)} />
			</FormField>
			{#if useEab}
				<div class="form-subfields">
					<FormField label="Key id" id="acme-eab-kid" span="half">
						<Input
							id="acme-eab-kid"
							bind:value={eabKeyId}
							class="font-mono"
							autocomplete="off"
							disabled={locked(P.eabKeyId)}
						/>
					</FormField>
					<FormField
						label="HMAC key"
						id="acme-eab-mac"
						help={eabHmacKeySet ? 'A key is stored, leave blank to keep it' : 'Never shown again'}
						span="half"
					>
						<PasswordInput
							id="acme-eab-mac"
							bind:value={eabHmacKey}
							placeholder={eabHmacKeySet ? 'Unchanged' : 'HMAC key'}
							autocomplete="new-password"
							disabled={locked(P.eabHmacKey)}
						/>
					</FormField>
				</div>
			{/if}
			<FormField
				label="Directory CA bundle"
				id="acme-ca-bundle"
				tag={tag(P.caBundle)}
				help={help(
					P.caBundle,
					"PEM roots that sign the directory's own certificate, empty trusts the system roots"
				)}
			>
				<PemInput
					id="acme-ca-bundle"
					bind:value={caBundle}
					rows={3}
					placeholder="-----BEGIN CERTIFICATE-----"
					disabled={locked(P.caBundle)}
				/>
			</FormField>
		{/if}
		{#snippet footer()}
			{#if !deferred && !credentialsLocked}
				{@render saveButton(credentialsAct, 'Save credentials', () =>
					apply(credentialsAct, credentialsPatch())
				)}
			{/if}
			{#if !deferred && accountOverrides}
				{@render resetButton(resetAccountAct, ACCOUNT_PATHS)}
			{/if}
		{/snippet}
	</FormSection>

	<FormSection
		title="ACME validation"
		description="How the CA confirms control of a hostname before it issues."
	>
		<FormField
			label="Challenge"
			id="acme-challenge"
			tag={tag(P.challenge)}
			help={help(P.challenge, challengeOption.help)}
			span="half"
		>
			<Select
				type="single"
				value={String(challenge)}
				onValueChange={(v) => {
					if (v) challenge = Number(v) as ACMEChallengeType;
				}}
				disabled={locked(P.challenge)}
			>
				<SelectTrigger id="acme-challenge" class="w-full">{challengeOption.label}</SelectTrigger>
				<SelectContent>
					{#each challengeOptions as o (o.value)}
						<SelectItem value={String(o.value)}>{o.label}</SelectItem>
					{/each}
				</SelectContent>
			</Select>
		</FormField>
		{#if dns01}
			{#if parentScope}
				<FormField
					label="Own DNS provider"
					layout="toggle"
					tag={tag(P.provider)}
					help={help(
						P.provider,
						"Publishes through this scope's provider and credentials, off keeps the inherited one"
					)}
				>
					<Switch bind:checked={ownDns} disabled={locked(P.provider)} />
				</FormField>
			{/if}
			<div class="form-subfields">
				<FormField label="Provider" id="acme-dns-provider" span="half">
					<Select
						type="single"
						value={String(provider)}
						onValueChange={(v) => {
							if (v) provider = Number(v) as DNSProvider;
						}}
						disabled={locked(P.provider) || dnsLocked}
					>
						<SelectTrigger id="acme-dns-provider" class="w-full">{providerLabel}</SelectTrigger>
						<SelectContent>
							{#each providerOptions as o (o.value)}
								<SelectItem value={String(o.value)}>{o.label}</SelectItem>
							{/each}
						</SelectContent>
					</Select>
				</FormField>
				{#if rfc2136}
					<FormField
						label="Nameserver"
						id="acme-dns-server"
						help="Host or host:port that accepts dynamic updates"
						span="half"
					>
						<Input
							id="acme-dns-server"
							bind:value={server}
							class="font-mono"
							placeholder="ns1.example.com:53"
							disabled={locked(P.server) || dnsLocked}
						/>
					</FormField>
					<FormField label="TSIG key name" id="acme-tsig-name" span="half">
						<Input
							id="acme-tsig-name"
							bind:value={tsigKeyName}
							class="font-mono"
							autocomplete="off"
							disabled={locked(P.tsigKeyName) || dnsLocked}
						/>
					</FormField>
					<FormField label="TSIG algorithm" id="acme-tsig-alg" span="half">
						<Input
							id="acme-tsig-alg"
							bind:value={tsigAlgorithm}
							class="font-mono"
							placeholder="hmac-sha256"
							disabled={locked(P.tsigAlgorithm) || dnsLocked}
						/>
					</FormField>
					<FormField
						label="TSIG secret"
						id="acme-tsig-secret"
						help={tsigSecretSet && secretKept
							? 'A secret is stored, leave blank to keep it'
							: 'Base64 key, never shown again'}
						span="half"
					>
						<PasswordInput
							id="acme-tsig-secret"
							bind:value={tsigSecret}
							placeholder={tsigSecretSet && secretKept ? 'Unchanged' : 'Secret'}
							autocomplete="new-password"
							disabled={locked(P.tsigSecret) || dnsLocked}
						/>
					</FormField>
				{:else}
					<FormField
						label="API token"
						id="acme-dns-token"
						help={apiTokenSet && secretKept
							? 'A token is stored, leave blank to keep it'
							: 'Needs DNS edit on the zone, never shown again'}
						span="half"
					>
						<PasswordInput
							id="acme-dns-token"
							bind:value={apiToken}
							placeholder={apiTokenSet && secretKept ? 'Unchanged' : 'Token'}
							autocomplete="new-password"
							disabled={locked(P.apiToken) || dnsLocked}
						/>
					</FormField>
				{/if}
				<FormField
					label="Resolvers"
					id="acme-dns-resolvers"
					help="Comma separated nameservers that confirm the record, empty uses system DNS"
					span="two-thirds"
					newRow
				>
					<Input
						id="acme-dns-resolvers"
						bind:value={resolvers}
						class="font-mono"
						placeholder="10.0.0.2:53, 10.0.0.3:53"
						disabled={locked(P.resolvers) || dnsLocked}
					/>
				</FormField>
				<FormField
					label="Propagation timeout"
					id="acme-dns-wait"
					help="Seconds, zero skips the wait"
					span="third"
				>
					<Input
						id="acme-dns-wait"
						type="number"
						min={0}
						bind:value={propagation}
						disabled={locked(P.propagation) || dnsLocked}
					/>
				</FormField>
			</div>
		{/if}
		{#snippet footer()}
			{#if !deferred}
				{@render saveButton(validationAct, 'Save validation', () =>
					apply(validationAct, validationPatch())
				)}
				{#if validationOverrides}
					{@render resetButton(resetValidationAct, VALIDATION_PATHS)}
				{/if}
			{/if}
		{/snippet}
	</FormSection>
{/if}

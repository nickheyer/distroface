<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Switch } from '$lib/components/ui/switch';
	import { Input } from '$lib/components/ui/input';
	import UnitInput from '$lib/components/unit-input.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import { Button } from '$lib/components/ui/button';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { Act, errText } from '$lib/act.svelte';
	import { isLocked, patchSettings, systemScope, type SettingsPatch } from '$lib/settings-utils';
	import type { FieldProvenance, Settings } from '$lib/proto/distroface/v1/settings_pb';

	let eff = $state<Settings | null>(null);
	let prov = $state<FieldProvenance[]>([]);
	let loading = $state(true);
	let loadError = $state('');

	let localEnabled = $state(true);
	let registrationEnabled = $state(false);
	let anonymousAccess = $state(false);
	let timeoutMinutes = $state(60);

	let oidcEnabled = $state(false);
	let oidcIssuer = $state('');
	let oidcClientId = $state('');
	let oidcClientSecret = $state('');
	let oidcSecretSet = $state(false);
	let oidcRedirect = $state('');
	let oidcRoleClaim = $state('');
	let oidcGroupClaim = $state('');

	const localAct = new Act();
	const registrationAct = new Act();
	const anonymousAct = new Act();
	const timeoutAct = new Act();
	const oidcSwitchAct = new Act();
	const issuerAct = new Act();
	const clientIdAct = new Act();
	const secretAct = new Act();
	const redirectAct = new Act();
	const roleClaimAct = new Act();
	const groupClaimAct = new Act();

	let canEdit = $derived(authStore.canUpdateSettings);

	const locked = (path: string) => isLocked(prov, path);
	const lockHelp = (path: string, help?: string) =>
		locked(path) ? 'Pinned by the config file' : help;

	function seedForm(s: Settings) {
		localEnabled = s.auth?.localEnabled ?? true;
		registrationEnabled = s.auth?.localAllowRegistration ?? false;
		anonymousAccess = s.auth?.anonymousAccess ?? false;
		timeoutMinutes = Math.round((s.auth?.sessionTimeoutSeconds ?? 86400) / 60);
		oidcEnabled = s.auth?.oidc?.enabled ?? false;
		oidcIssuer = s.auth?.oidc?.issuerUri ?? '';
		oidcClientId = s.auth?.oidc?.clientId ?? '';
		oidcClientSecret = '';
		oidcSecretSet = s.auth?.oidc?.clientSecretSet ?? false;
		oidcRedirect = s.auth?.oidc?.redirectUrl ?? '';
		oidcRoleClaim = s.auth?.oidc?.roleClaim ?? '';
		oidcGroupClaim = s.auth?.oidc?.groupClaim ?? '';
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

	function commitTimeout() {
		const seconds = Math.max(1, Math.round(timeoutMinutes)) * 60;
		if (seconds === (eff?.auth?.sessionTimeoutSeconds ?? 0)) return;
		apply(timeoutAct, { auth: { sessionTimeoutSeconds: seconds } }, [
			'auth.session_timeout_seconds'
		]);
	}

	// Blur commit for one oidc text field
	function commitOidc(act: Act, path: string, value: string, current: string) {
		if (value.trim() === current) return;
		const field = path.split('.').pop() ?? '';
		const camel = field.replace(/_([a-z])/g, (_, c) => c.toUpperCase());
		apply(act, { auth: { oidc: { [camel]: value.trim() } } }, [path]);
	}

	function commitOidcSecret() {
		if (oidcClientSecret.trim() === '') return;
		apply(secretAct, { auth: { oidc: { clientSecret: oidcClientSecret.trim() } } }, [
			'auth.oidc.client_secret'
		]);
	}

	onMount(() => {
		if (!authStore.hasPermission('settings', 'read')) {
			goto(resolve('/admin'));
			return;
		}
		load();
	});
</script>

{#if loading}
	<div class="space-y-6">
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
		title="Authentication"
		description="How people access this instance. Changes apply immediately."
	>
		<FormSection title="Sign-in" description="Local accounts, open registration, and sessions.">
			<FormField
				label="Local sign-in"
				layout="toggle"
				help={lockHelp('auth.local_enabled', 'Username and password accounts')}
				tag={localAct.tag}
				error={localAct.error}
			>
				<Switch
					checked={localEnabled}
					disabled={!canEdit || localAct.busy || locked('auth.local_enabled')}
					onCheckedChange={(v) => {
						localEnabled = v;
						apply(localAct, { auth: { localEnabled: v } }, ['auth.local_enabled']);
					}}
				/>
			</FormField>
			{#if localEnabled}
				<div class="form-subfields">
					<FormField
						label="Open registration"
						layout="toggle"
						help={lockHelp('auth.local_allow_registration', 'Account creation without an invite')}
						tag={registrationAct.tag}
						error={registrationAct.error}
					>
						<Switch
							checked={registrationEnabled}
							disabled={!canEdit || registrationAct.busy || locked('auth.local_allow_registration')}
							onCheckedChange={(v) => {
								registrationEnabled = v;
								apply(registrationAct, { auth: { localAllowRegistration: v } }, [
									'auth.local_allow_registration'
								]);
							}}
						/>
					</FormField>
				</div>
			{/if}
			<FormField
				label="Anonymous access"
				layout="toggle"
				help={lockHelp(
					'auth.anonymous_access',
					'Signed out visitors get the anonymous role grants, orgs may override for their portals'
				)}
				tag={anonymousAct.tag}
				error={anonymousAct.error}
			>
				<Switch
					checked={anonymousAccess}
					disabled={!canEdit || anonymousAct.busy || locked('auth.anonymous_access')}
					onCheckedChange={(v) => {
						anonymousAccess = v;
						apply(anonymousAct, { auth: { anonymousAccess: v } }, ['auth.anonymous_access']);
					}}
				/>
			</FormField>
			<FormField
				label="Session timeout"
				id="session-timeout"
				layout="inline"
				help={lockHelp('auth.session_timeout_seconds', 'Idle time before sign-in expires')}
				tag={timeoutAct.tag}
				error={timeoutAct.error}
			>
				<UnitInput
					id="session-timeout"
					unit="min"
					bind:value={timeoutMinutes}
					min={5}
					max={10080}
					class="w-32"
					disabled={!canEdit || timeoutAct.busy || locked('auth.session_timeout_seconds')}
					onblur={commitTimeout}
					onkeydown={(e) => {
						if (e.key === 'Enter') commitTimeout();
					}}
				/>
			</FormField>
		</FormSection>

		<FormSection title="OIDC / SSO" description="External identity provider sign-in.">
			<FormField
				label="Enabled"
				layout="toggle"
				help={lockHelp('auth.oidc.enabled')}
				tag={oidcSwitchAct.tag}
				error={oidcSwitchAct.error}
			>
				<Switch
					checked={oidcEnabled}
					disabled={!canEdit || oidcSwitchAct.busy || locked('auth.oidc.enabled')}
					onCheckedChange={(v) => {
						oidcEnabled = v;
						apply(oidcSwitchAct, { auth: { oidc: { enabled: v } } }, ['auth.oidc.enabled']);
					}}
				/>
			</FormField>
			{#if oidcEnabled}
				<div class="form-subfields">
					<FormField
						label="Issuer URI"
						id="oidc-issuer"
						help={lockHelp('auth.oidc.issuer_uri', 'Discovery is resolved from this address')}
						tag={issuerAct.tag}
						error={issuerAct.error}
					>
						<Input
							id="oidc-issuer"
							bind:value={oidcIssuer}
							class="font-mono"
							placeholder="https://idp.example.com/realms/main"
							disabled={!canEdit || issuerAct.busy || locked('auth.oidc.issuer_uri')}
							onblur={() =>
								commitOidc(
									issuerAct,
									'auth.oidc.issuer_uri',
									oidcIssuer,
									eff?.auth?.oidc?.issuerUri ?? ''
								)}
						/>
					</FormField>
					<FormField
						label="Client ID"
						id="oidc-client-id"
						span="half"
						help={lockHelp('auth.oidc.client_id')}
						tag={clientIdAct.tag}
						error={clientIdAct.error}
					>
						<Input
							id="oidc-client-id"
							bind:value={oidcClientId}
							class="font-mono"
							autocomplete="off"
							disabled={!canEdit || clientIdAct.busy || locked('auth.oidc.client_id')}
							onblur={() =>
								commitOidc(
									clientIdAct,
									'auth.oidc.client_id',
									oidcClientId,
									eff?.auth?.oidc?.clientId ?? ''
								)}
						/>
					</FormField>
					<FormField
						label="Client secret"
						id="oidc-client-secret"
						span="half"
						help={lockHelp(
							'auth.oidc.client_secret',
							oidcSecretSet
								? 'Type to replace the stored secret'
								: 'Stored server side, never shown'
						)}
						tag={secretAct.tag}
						error={secretAct.error}
					>
						<Input
							id="oidc-client-secret"
							type="password"
							bind:value={oidcClientSecret}
							placeholder={oidcSecretSet ? '••••••••' : ''}
							autocomplete="new-password"
							data-1p-ignore
							data-lpignore="true"
							data-bwignore
							disabled={!canEdit || secretAct.busy || locked('auth.oidc.client_secret')}
							onblur={commitOidcSecret}
						/>
					</FormField>
					<FormField
						label="Redirect URL"
						id="oidc-redirect"
						help={lockHelp(
							'auth.oidc.redirect_url',
							'Callback registered with the identity provider'
						)}
						tag={redirectAct.tag}
						error={redirectAct.error}
					>
						<Input
							id="oidc-redirect"
							bind:value={oidcRedirect}
							class="font-mono"
							disabled={!canEdit || redirectAct.busy || locked('auth.oidc.redirect_url')}
							onblur={() =>
								commitOidc(
									redirectAct,
									'auth.oidc.redirect_url',
									oidcRedirect,
									eff?.auth?.oidc?.redirectUrl ?? ''
								)}
						/>
					</FormField>
					<FormField
						label="Role claim"
						id="oidc-role-claim"
						span="half"
						help={lockHelp('auth.oidc.role_claim', 'Claim mapped to system roles')}
						tag={roleClaimAct.tag}
						error={roleClaimAct.error}
					>
						<Input
							id="oidc-role-claim"
							bind:value={oidcRoleClaim}
							class="font-mono"
							disabled={!canEdit || roleClaimAct.busy || locked('auth.oidc.role_claim')}
							onblur={() =>
								commitOidc(
									roleClaimAct,
									'auth.oidc.role_claim',
									oidcRoleClaim,
									eff?.auth?.oidc?.roleClaim ?? ''
								)}
						/>
					</FormField>
					<FormField
						label="Group claim"
						id="oidc-group-claim"
						span="half"
						help={lockHelp('auth.oidc.group_claim', 'Claim listing idp groups')}
						tag={groupClaimAct.tag}
						error={groupClaimAct.error}
					>
						<Input
							id="oidc-group-claim"
							bind:value={oidcGroupClaim}
							class="font-mono"
							disabled={!canEdit || groupClaimAct.busy || locked('auth.oidc.group_claim')}
							onblur={() =>
								commitOidc(
									groupClaimAct,
									'auth.oidc.group_claim',
									oidcGroupClaim,
									eff?.auth?.oidc?.groupClaim ?? ''
								)}
						/>
					</FormField>
				</div>
			{/if}
		</FormSection>
	</FormPage>
{/if}

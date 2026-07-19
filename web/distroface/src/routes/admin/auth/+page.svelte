<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Switch } from '$lib/components/ui/switch';
	import { Input } from '$lib/components/ui/input';
	import FormField from '$lib/components/form-field.svelte';
	import FormCard from '$lib/components/form-card.svelte';
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
	const oidcAct = new Act();

	let canEdit = $derived(authStore.canUpdateSettings);

	const locked = (path: string) => isLocked(prov, path);

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

	function commitTimeout() {
		const seconds = Math.max(1, Math.round(timeoutMinutes)) * 60;
		if (seconds === (eff?.auth?.sessionTimeoutSeconds ?? 0)) return;
		apply(timeoutAct, { auth: { sessionTimeoutSeconds: seconds } }, ['auth.session_timeout_seconds']);
	}

	// Blur commit for one oidc text field
	function commitOidc(path: string, value: string, current: string) {
		if (value.trim() === current) return;
		const field = path.split('.').pop() ?? '';
		const camel = field.replace(/_([a-z])/g, (_, c) => c.toUpperCase());
		apply(oidcAct, { auth: { oidc: { [camel]: value.trim() } } }, [path]);
	}

	function commitOidcSecret() {
		if (oidcClientSecret.trim() === '') return;
		apply(oidcAct, { auth: { oidc: { clientSecret: oidcClientSecret.trim() } } }, ['auth.oidc.client_secret']);
	}

	onMount(() => {
		if (!authStore.hasPermission('settings', 'read')) { goto(resolve('/admin')); return; }
		load();
	});
</script>

{#if loading}
	<div class="space-y-6">
		<Skeleton class="h-52 w-full rounded-xl" />
		<Skeleton class="h-40 w-full rounded-xl" />
	</div>
{:else if loadError}
	<div class="rounded-xl border border-destructive/40 bg-destructive/5 px-6 py-10 text-center space-y-3">
		<p class="text-sm text-destructive">{loadError}</p>
		<Button variant="outline" size="sm" onclick={load}>Retry</Button>
	</div>
{:else if eff}
	<div class="space-y-6">
		<FormCard title="Sign-in">
			<div class="space-y-3">
				<FormField
					label="Local sign-in"
					horizontal
					help={locked('auth.local_enabled') ? 'Pinned by the config file' : undefined}
					tag={localAct.tag}
					error={localAct.error}
				>
					<Switch
						checked={localEnabled}
						disabled={!canEdit || localAct.busy || locked('auth.local_enabled')}
						onCheckedChange={(v) => { localEnabled = v; apply(localAct, { auth: { localEnabled: v } }, ['auth.local_enabled']); }}
					/>
				</FormField>
				{#if localEnabled}
					<FormField
						label="Open registration"
						horizontal
						help={locked('auth.local_allow_registration') ? 'Pinned by the config file' : 'Account creation without an invite'}
						tag={registrationAct.tag}
						error={registrationAct.error}
						class="ml-7"
					>
						<Switch
							checked={registrationEnabled}
							disabled={!canEdit || registrationAct.busy || locked('auth.local_allow_registration')}
							onCheckedChange={(v) => { registrationEnabled = v; apply(registrationAct, { auth: { localAllowRegistration: v } }, ['auth.local_allow_registration']); }}
						/>
					</FormField>
				{/if}
				<FormField
					label="Anonymous access"
					horizontal
					help={locked('auth.anonymous_access') ? 'Pinned by the config file' : 'Browse public repos signed out'}
					tag={anonymousAct.tag}
					error={anonymousAct.error}
				>
					<Switch
						checked={anonymousAccess}
						disabled={!canEdit || anonymousAct.busy || locked('auth.anonymous_access')}
						onCheckedChange={(v) => { anonymousAccess = v; apply(anonymousAct, { auth: { anonymousAccess: v } }, ['auth.anonymous_access']); }}
					/>
				</FormField>
				<FormField
					label="Session timeout"
					id="session-timeout"
					horizontal
					tag={timeoutAct.tag}
					error={timeoutAct.error}
				>
					<div class="flex items-center gap-2">
						<Input
							id="session-timeout"
							type="number"
							bind:value={timeoutMinutes}
							min={5}
							max={10080}
							class="w-28"
							disabled={!canEdit || timeoutAct.busy || locked('auth.session_timeout_seconds')}
							onblur={commitTimeout}
							onkeydown={(e) => { if (e.key === 'Enter') commitTimeout(); }}
						/>
						<span class="text-[13px] text-muted-foreground">minutes</span>
					</div>
				</FormField>
			</div>
		</FormCard>

		<FormCard title="OIDC / SSO" description="External identity provider, applied live">
			<div class="space-y-3">
				<FormField
					label="Enabled"
					horizontal
					help={locked('auth.oidc.enabled') ? 'Pinned by the config file' : undefined}
					tag={oidcSwitchAct.tag}
					error={oidcSwitchAct.error}
				>
					<Switch
						checked={oidcEnabled}
						disabled={!canEdit || oidcSwitchAct.busy || locked('auth.oidc.enabled')}
						onCheckedChange={(v) => { oidcEnabled = v; apply(oidcSwitchAct, { auth: { oidc: { enabled: v } } }, ['auth.oidc.enabled']); }}
					/>
				</FormField>
				{#if oidcEnabled}
					<div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
						<FormField label="Issuer URI" id="oidc-issuer" tag={oidcAct.tag} error={oidcAct.error}>
							<Input
								id="oidc-issuer"
								bind:value={oidcIssuer}
								class="font-mono text-xs"
								placeholder="https://idp.example.com/realms/main"
								disabled={!canEdit || oidcAct.busy || locked('auth.oidc.issuer_uri')}
								onblur={() => commitOidc('auth.oidc.issuer_uri', oidcIssuer, eff?.auth?.oidc?.issuerUri ?? '')}
							/>
						</FormField>
						<FormField label="Client ID" id="oidc-client-id">
							<Input
								id="oidc-client-id"
								bind:value={oidcClientId}
								class="font-mono text-xs"
								disabled={!canEdit || oidcAct.busy || locked('auth.oidc.client_id')}
								onblur={() => commitOidc('auth.oidc.client_id', oidcClientId, eff?.auth?.oidc?.clientId ?? '')}
							/>
						</FormField>
						<FormField
							label="Client secret"
							id="oidc-client-secret"
							help={oidcSecretSet ? 'A secret is stored, type to replace it' : 'Stored server side, never shown'}
						>
							<Input
								id="oidc-client-secret"
								type="password"
								bind:value={oidcClientSecret}
								placeholder={oidcSecretSet ? '••••••••' : ''}
								disabled={!canEdit || oidcAct.busy || locked('auth.oidc.client_secret')}
								onblur={commitOidcSecret}
							/>
						</FormField>
						<FormField label="Redirect URL" id="oidc-redirect">
							<Input
								id="oidc-redirect"
								bind:value={oidcRedirect}
								class="font-mono text-xs"
								disabled={!canEdit || oidcAct.busy || locked('auth.oidc.redirect_url')}
								onblur={() => commitOidc('auth.oidc.redirect_url', oidcRedirect, eff?.auth?.oidc?.redirectUrl ?? '')}
							/>
						</FormField>
						<FormField label="Role claim" id="oidc-role-claim" help="Claim mapped to system roles">
							<Input
								id="oidc-role-claim"
								bind:value={oidcRoleClaim}
								class="font-mono text-xs"
								disabled={!canEdit || oidcAct.busy || locked('auth.oidc.role_claim')}
								onblur={() => commitOidc('auth.oidc.role_claim', oidcRoleClaim, eff?.auth?.oidc?.roleClaim ?? '')}
							/>
						</FormField>
						<FormField label="Group claim" id="oidc-group-claim" help="Claim listing idp groups">
							<Input
								id="oidc-group-claim"
								bind:value={oidcGroupClaim}
								class="font-mono text-xs"
								disabled={!canEdit || oidcAct.busy || locked('auth.oidc.group_claim')}
								onblur={() => commitOidc('auth.oidc.group_claim', oidcGroupClaim, eff?.auth?.oidc?.groupClaim ?? '')}
							/>
						</FormField>
					</div>
				{/if}
			</div>
		</FormCard>
	</div>
{/if}

<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Switch } from '$lib/components/ui/switch';
	import { Input } from '$lib/components/ui/input';
	import FormField from '$lib/components/form-field.svelte';
	import FormCard from '$lib/components/form-card.svelte';
	import { Button } from '$lib/components/ui/button';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { Act, errText } from '$lib/act.svelte';
	import type { GetAuthConfigResponse } from '$lib/proto/distroface/v1/auth_pb';

	let config = $state<GetAuthConfigResponse | null>(null);
	let loading = $state(true);
	let loadError = $state('');

	let localEnabled = $state(true);
	let registrationEnabled = $state(false);
	let anonymousAccess = $state(false);
	let sessionTimeout = $state(60);

	const localAct = new Act();
	const registrationAct = new Act();
	const anonymousAct = new Act();
	const timeoutAct = new Act();

	let canEdit = $derived(authStore.canUpdateSettings);

	const oidcFields = $derived(
		config
			? [
					{ label: 'Issuer URI', value: config.oidcIssuerUri },
					{ label: 'Client ID', value: config.oidcClientId },
					{ label: 'Redirect URL', value: config.oidcRedirectUrl },
					{ label: 'Role Claim', value: config.oidcRoleClaim }
				]
			: []
	);

	function seedForm(resp: GetAuthConfigResponse) {
		localEnabled = resp.localEnabled;
		registrationEnabled = resp.registrationEnabled;
		anonymousAccess = resp.anonymousAccess;
		sessionTimeout = resp.sessionTimeout;
	}

	async function loadConfig() {
		loading = true;
		loadError = '';
		try {
			const resp = await rpcClient.auth.getAuthConfig({}, silentCallOptions);
			config = resp;
			seedForm(resp);
		} catch (err) {
			loadError = errText(err);
		} finally {
			loading = false;
		}
	}

	// Settings apply on interaction, a failed patch reverts the control
	async function apply(act: Act) {
		const ok = await act.run(async () => {
			const resp = await rpcClient.auth.updateAuthSettings(
				{ localEnabled, registrationEnabled, anonymousAccess, sessionTimeout },
				silentCallOptions
			);
			if (resp.config) {
				config = resp.config;
				seedForm(resp.config);
			}
		});
		if (!ok && config) seedForm(config);
	}

	function commitTimeout() {
		if (config && sessionTimeout === config.sessionTimeout) return;
		apply(timeoutAct);
	}

	onMount(() => {
		if (!authStore.hasPermission('settings', 'read')) { goto(resolve('/admin')); return; }
		loadConfig();
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
		<Button variant="outline" size="sm" onclick={loadConfig}>Retry</Button>
	</div>
{:else if config}
	<div class="space-y-6">
		<FormCard title="Sign-in">
			<div class="space-y-3">
				<FormField
					label="Local sign-in"
					horizontal
					tag={localAct.tag}
					error={localAct.error}
				>
					<Switch
						checked={localEnabled}
						disabled={!canEdit || localAct.busy}
						onCheckedChange={(v) => { localEnabled = v; apply(localAct); }}
					/>
				</FormField>
				{#if localEnabled}
					<FormField
						label="Open registration"
						horizontal
						help="Account creation without an invite"
						tag={registrationAct.tag}
						error={registrationAct.error}
						class="ml-7"
					>
						<Switch
							checked={registrationEnabled}
							disabled={!canEdit || registrationAct.busy}
							onCheckedChange={(v) => { registrationEnabled = v; apply(registrationAct); }}
						/>
					</FormField>
				{/if}
				<FormField
					label="Anonymous access"
					horizontal
					help="Browse public repos signed out"
					tag={anonymousAct.tag}
					error={anonymousAct.error}
				>
					<Switch
						checked={anonymousAccess}
						disabled={!canEdit || anonymousAct.busy}
						onCheckedChange={(v) => { anonymousAccess = v; apply(anonymousAct); }}
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
							bind:value={sessionTimeout}
							min={5}
							max={10080}
							class="w-28"
							disabled={!canEdit || timeoutAct.busy}
							onblur={commitTimeout}
							onkeydown={(e) => { if (e.key === 'Enter') commitTimeout(); }}
						/>
						<span class="text-[13px] text-muted-foreground">minutes</span>
					</div>
				</FormField>
			</div>
		</FormCard>

		<FormCard title="OIDC / SSO">
			<div class="space-y-4">
				<div class="flex items-center gap-2">
					<span class="status-dot {config.oidcEnabled ? 'status-dot-active' : 'status-dot-inactive'}"></span>
					<span class="text-sm font-medium">{config.oidcEnabled ? 'Enabled' : 'Not configured'}</span>
					<span class="text-[13px] text-muted-foreground">&middot; set via environment variables</span>
				</div>

				{#if config.oidcEnabled}
					<div class="rounded-xl border border-border/60 overflow-hidden">
						<table class="w-full text-sm">
							<tbody>
								{#each oidcFields as field, i (field.label)}
									<tr class={i < oidcFields.length - 1 || config.oidcScopes.length > 0 ? 'border-b border-border/40' : ''}>
										<td class="th text-left w-36">{field.label}</td>
										<td class="px-3 py-2.5">
											<code class="text-xs bg-muted px-2 py-1 rounded font-mono">{field.value}</code>
										</td>
									</tr>
								{/each}
								{#if config.oidcScopes.length > 0}
									<tr>
										<td class="th text-left w-36">Scopes</td>
										<td class="px-3 py-2.5">
											<div class="flex gap-1 flex-wrap">
												{#each config.oidcScopes as scope (scope)}
													<Badge variant="outline" class="text-xs">{scope}</Badge>
												{/each}
											</div>
										</td>
									</tr>
								{/if}
							</tbody>
						</table>
					</div>
				{/if}
			</div>
		</FormCard>
	</div>
{/if}

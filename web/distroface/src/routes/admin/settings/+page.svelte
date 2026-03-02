<script lang="ts">
	import { onMount } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Switch } from '$lib/components/ui/switch';
	import { Input } from '$lib/components/ui/input';
	import FormField from '$lib/components/form-field.svelte';
	import FormCard from '$lib/components/form-card.svelte';
	import { Shield, Globe, Save } from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { toast } from 'svelte-sonner';
	import type { GetAuthConfigResponse } from '$lib/proto/distroface/v1/auth_pb';

	let config = $state<GetAuthConfigResponse | null>(null);
	let loading = $state(true);
	let saving = $state(false);

	let localEnabled = $state(true);
	let registrationEnabled = $state(false);
	let anonymousAccess = $state(false);
	let sessionTimeout = $state(60);

	let canEdit = $derived(authStore.canUpdateSettings);

	let hasChanges = $derived(
		config !== null &&
			(localEnabled !== config.localEnabled ||
				registrationEnabled !== config.registrationEnabled ||
				anonymousAccess !== config.anonymousAccess ||
				sessionTimeout !== config.sessionTimeout)
	);

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

	async function loadConfig() {
		loading = true;
		try {
			const resp = await rpcClient.auth.getAuthConfig({});
			config = resp;
			localEnabled = resp.localEnabled;
			registrationEnabled = resp.registrationEnabled;
			anonymousAccess = resp.anonymousAccess;
			sessionTimeout = resp.sessionTimeout;
		} catch {
			// error interceptor
		} finally {
			loading = false;
		}
	}

	async function saveSettings() {
		saving = true;
		try {
			const resp = await rpcClient.auth.updateAuthSettings({
				localEnabled,
				registrationEnabled,
				anonymousAccess,
				sessionTimeout
			});
			if (resp.config) {
				config = resp.config;
				localEnabled = resp.config.localEnabled;
				registrationEnabled = resp.config.registrationEnabled;
				anonymousAccess = resp.config.anonymousAccess;
				sessionTimeout = resp.config.sessionTimeout;
			}
			toast.success('Settings saved');
		} catch {
			// error interceptor
		} finally {
			saving = false;
		}
	}

	onMount(loadConfig);
</script>

{#if loading}
	<div class="space-y-6">
		<Skeleton class="h-52 w-full rounded-xl" />
		<Skeleton class="h-40 w-full rounded-xl" />
	</div>
{:else if config}
	<div class="space-y-6">
		<!-- Local Auth Card -->
		<FormCard title="Local Authentication" description="Username and password sign-in" icon={Shield}>
			<div class="space-y-3">
				<FormField label="Enable local authentication" help="Allow users to sign in with username and password." horizontal>
					<Switch bind:checked={localEnabled} disabled={!canEdit} />
				</FormField>
				<FormField label="Allow registration" help="Allow new users to create accounts without an invite." horizontal>
					<Switch bind:checked={registrationEnabled} disabled={!canEdit} />
				</FormField>
				<FormField label="Anonymous access" help="Allow unauthenticated users to browse public repositories." horizontal>
					<Switch bind:checked={anonymousAccess} disabled={!canEdit} />
				</FormField>
				<FormField
					label="Session timeout"
					id="session-timeout"
					help="Minutes before sessions expire (5-10,080)."
				>
					<Input
						id="session-timeout"
						type="number"
						bind:value={sessionTimeout}
						min={5}
						max={10080}
						class="w-36"
						disabled={!canEdit}
					/>
				</FormField>
			</div>
			{#snippet footer()}
				{#if hasChanges && canEdit}
					<Button onclick={saveSettings} disabled={saving} class="gap-2">
						<Save class="h-4 w-4" />
						{saving ? 'Saving...' : 'Save Changes'}
					</Button>
				{/if}
			{/snippet}
		</FormCard>

		<!-- OIDC Card -->
		<FormCard title="OIDC / SSO" description="OpenID Connect single sign-on" icon={Globe}>
			<div class="space-y-4">
				<div class="flex items-center gap-2">
					<span class="status-dot {config.oidcEnabled ? 'status-dot-active' : 'status-dot-inactive'}"></span>
					<span class="text-sm font-medium">{config.oidcEnabled ? 'Enabled' : 'Disabled'}</span>
				</div>

				{#if config.oidcEnabled}
					<div class="rounded-xl border border-border/60 overflow-hidden">
						<table class="w-full text-sm">
							<tbody>
								{#each oidcFields as field, i}
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
												{#each config.oidcScopes as scope}
													<Badge variant="outline" class="text-xs">{scope}</Badge>
												{/each}
											</div>
										</td>
									</tr>
								{/if}
							</tbody>
						</table>
					</div>
					<p class="text-[13px] text-muted-foreground">
						OIDC settings are configured via environment variables and cannot be changed here.
					</p>
				{:else}
					<p class="text-[13px] text-muted-foreground">
						OIDC is not configured. Set the OIDC environment variables to enable single sign-on.
					</p>
				{/if}
			</div>
		</FormCard>
	</div>
{/if}

<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Switch } from '$lib/components/ui/switch';
	import { Textarea } from '$lib/components/ui/textarea';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import { Loader2 } from '@lucide/svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { Act, errText } from '$lib/act.svelte';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime } from '$lib/utils';
	import { isLocked, patchSettings, systemScope, type SettingsPatch } from '$lib/settings-utils';
	import type { FieldProvenance, Settings } from '$lib/proto/distroface/v1/settings_pb';
	import type { CertificateDomain } from '$lib/proto/distroface/v1/certificate_pb';

	let eff = $state<Settings | null>(null);
	let prov = $state<FieldProvenance[]>([]);
	let pending = $state<CertificateDomain[]>([]);
	let loading = $state(true);
	let loadError = $state('');

	let requireApproval = $state(false);
	let blacklistText = $state('');

	const approvalSwitchAct = new Act();
	const blacklistAct = new Act();
	const approvalsAct = new Act();

	let approvalBusy = $state<string | null>(null);

	const blacklistPlaceholder = 'internal.corp\n*.example.org';

	// Pinned fields render disabled with a lock hint
	const locked = (path: string) => isLocked(prov, path);
	const lockHint = (path: string, help: string) =>
		locked(path) ? 'Pinned by the config file' : help;

	function seedForms(s: Settings) {
		requireApproval = s.portals?.requireHostnameApproval ?? false;
		blacklistText = (s.portals?.hostnameBlacklist ?? []).join('\n');
	}

	async function loadPending() {
		try {
			const resp = await rpcClient.certificate.listCertificateDomains(
				{
					pendingOnly: true,
					page: { pageSize: 200 }
				},
				silentCallOptions
			);
			pending = resp.domains;
		} catch {
			// List refreshes on the next action
		}
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
			if (eff) seedForms(eff);
			await loadPending();
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
				seedForms(res.effective);
			}
		});
		if (!ok && eff) seedForms(eff);
	}

	function setRequireApproval(v: boolean) {
		requireApproval = v;
		apply(approvalSwitchAct, { portals: { requireHostnameApproval: v } }, [
			'portals.require_hostname_approval'
		]);
	}

	function commitBlacklist() {
		const patterns = blacklistText
			.split('\n')
			.map((s) => s.trim())
			.filter(Boolean);
		if (patterns.join('\n') === (eff?.portals?.hostnameBlacklist ?? []).join('\n')) return;
		apply(blacklistAct, { portals: { hostnameBlacklist: patterns } }, [
			'portals.hostname_blacklist'
		]);
	}

	async function approve(domain: CertificateDomain) {
		approvalBusy = domain.id;
		await approvalsAct.run(() =>
			rpcClient.certificate.approveCertificateDomain({ id: domain.id }, silentCallOptions)
		);
		approvalBusy = null;
		await loadPending();
	}

	async function deny(domain: CertificateDomain) {
		approvalBusy = domain.id;
		await approvalsAct.run(() =>
			rpcClient.certificate.removeCertificateDomain({ id: domain.id }, silentCallOptions)
		);
		approvalBusy = null;
		await loadPending();
	}

	onMount(() => {
		if (!authStore.canManageSettings) {
			goto(resolve('/admin'));
			return;
		}
		load();
	});
</script>

{#if loading}
	<div class="space-y-6">
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
		title="Portals"
		description="Instance wide rules for the addresses organizations may claim."
	>
		<FormSection title="Portal hostnames" description="Rules for hostnames portals can claim.">
			<FormField
				label="Require approval"
				layout="toggle"
				help={lockHint(
					'portals.require_hostname_approval',
					'New portal hostnames wait for an admin'
				)}
				tag={approvalSwitchAct.tag}
				error={approvalSwitchAct.error}
			>
				<Switch
					checked={requireApproval}
					disabled={approvalSwitchAct.busy || locked('portals.require_hostname_approval')}
					onCheckedChange={setRequireApproval}
				/>
			</FormField>

			{#if pending.length > 0}
				<FormField
					label="Waiting for approval"
					help="Approve to allow issuance"
					tag={approvalsAct.tag}
					error={approvalsAct.error}
				>
					<div class="box divide-y">
						{#each pending as domain (domain.id)}
							<div class="flex items-center justify-between gap-3 px-4 py-2.5">
								<div class="flex min-w-0 flex-wrap items-center gap-2">
									<span class="font-mono text-sm">{domain.domain}</span>
									{#if domain.orgName}
										<a
											href={resolve('/orgs/[name]', { name: domain.orgName })}
											class="text-xs text-muted-foreground transition-colors hover:text-primary"
										>
											{domain.orgName}
										</a>
									{/if}
									{#if domain.createdAt}
										<span class="text-xs text-muted-foreground"
											>{relativeTime(timestampDate(domain.createdAt))}</span
										>
									{/if}
								</div>
								<div class="flex shrink-0 gap-1">
									<Button
										variant="outline"
										size="sm"
										disabled={approvalBusy !== null}
										onclick={() => approve(domain)}
									>
										{#if approvalBusy === domain.id}
											<Loader2 class="animate-spin" />
										{:else}
											Approve
										{/if}
									</Button>
									<Button
										variant="ghost"
										size="sm"
										class="text-destructive hover:text-destructive"
										disabled={approvalBusy !== null}
										onclick={() => deny(domain)}
									>
										Deny
									</Button>
								</div>
							</div>
						{/each}
					</div>
				</FormField>
			{/if}

			<FormField
				label="Blocked patterns"
				id="hostname-blacklist"
				help={lockHint('portals.hostname_blacklist', 'One pattern per line')}
				tag={blacklistAct.tag}
				error={blacklistAct.error}
			>
				<Textarea
					id="hostname-blacklist"
					bind:value={blacklistText}
					class="font-mono"
					rows={3}
					placeholder={blacklistPlaceholder}
					disabled={blacklistAct.busy || locked('portals.hostname_blacklist')}
					onblur={commitBlacklist}
				/>
			</FormField>
		</FormSection>
	</FormPage>
{/if}

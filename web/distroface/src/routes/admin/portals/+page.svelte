<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Switch } from '$lib/components/ui/switch';
	import { Textarea } from '$lib/components/ui/textarea';
	import FormCard from '$lib/components/form-card.svelte';
	import FormField from '$lib/components/form-field.svelte';
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
			const resp = await rpcClient.certificate.listCertificateDomains({
				pendingOnly: true,
				page: { pageSize: 200 }
			}, silentCallOptions);
			pending = resp.domains;
		} catch {
			// List refreshes on the next action
		}
	}

	async function load() {
		loading = true;
		loadError = '';
		try {
			const resp = await rpcClient.settings.getEffectiveSettings({ scope: systemScope }, silentCallOptions);
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
		apply(approvalSwitchAct, { portals: { requireHostnameApproval: v } }, ['portals.require_hostname_approval']);
	}

	function commitBlacklist() {
		const patterns = blacklistText.split('\n').map((s) => s.trim()).filter(Boolean);
		if (patterns.join('\n') === (eff?.portals?.hostnameBlacklist ?? []).join('\n')) return;
		apply(blacklistAct, { portals: { hostnameBlacklist: patterns } }, ['portals.hostname_blacklist']);
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
		if (!authStore.canManageSettings) { goto(resolve('/admin')); return; }
		load();
	});
</script>

{#if loading}
	<div class="space-y-6">
		<Skeleton class="h-40 w-full rounded-xl" />
	</div>
{:else if loadError}
	<div class="rounded-xl border border-destructive/40 bg-destructive/5 px-6 py-10 text-center space-y-3">
		<p class="text-sm text-destructive">{loadError}</p>
		<Button variant="outline" size="sm" onclick={load}>Retry</Button>
	</div>
{:else if eff}
	<div class="space-y-6">
		<FormCard title="Portal Hostnames" description="Rules for hostnames portals can claim">
			<div class="space-y-3">
				<FormField
					label="Require approval"
					horizontal
					help={lockHint('portals.require_hostname_approval', 'New portal hostnames wait for an admin')}
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
						<div class="rounded-lg border border-border/60 divide-y divide-border/40">
							{#each pending as domain (domain.id)}
								<div class="flex items-center justify-between gap-3 px-3 py-2.5">
									<div class="min-w-0 flex items-center gap-2 flex-wrap">
										<span class="font-mono text-sm">{domain.domain}</span>
										{#if domain.orgName}
											<a href={resolve('/orgs/[name]', { name: domain.orgName })} class="text-xs text-muted-foreground hover:text-primary transition-colors">
												{domain.orgName}
											</a>
										{/if}
										{#if domain.createdAt}
											<span class="text-xs text-muted-foreground/70">{relativeTime(timestampDate(domain.createdAt))}</span>
										{/if}
									</div>
									<div class="flex gap-1 shrink-0">
										<Button
											variant="outline"
											size="sm"
											class="h-7"
											disabled={approvalBusy !== null}
											onclick={() => approve(domain)}
										>
											{#if approvalBusy === domain.id}
												<Loader2 class="h-3.5 w-3.5 animate-spin" />
											{:else}
												Approve
											{/if}
										</Button>
										<Button
											variant="ghost"
											size="sm"
											class="h-7 px-2 text-xs text-destructive hover:text-destructive"
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
						class="font-mono text-xs"
						rows={3}
						placeholder={blacklistPlaceholder}
						disabled={blacklistAct.busy || locked('portals.hostname_blacklist')}
						onblur={commitBlacklist}
					/>
				</FormField>
			</div>
		</FormCard>
	</div>
{/if}

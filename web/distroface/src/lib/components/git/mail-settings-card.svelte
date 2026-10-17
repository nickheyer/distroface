<script lang="ts">
	import { onMount } from 'svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { toast } from 'svelte-sonner';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Switch } from '$lib/components/ui/switch';
	import { Input } from '$lib/components/ui/input';
	import {
		Select, SelectContent, SelectItem, SelectTrigger
	} from '$lib/components/ui/select';
	import UnitInput from '$lib/components/unit-input.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormCard from '$lib/components/form-card.svelte';
	import PasswordInput from '$lib/components/password-input.svelte';
	import { Mail, Inbox, Save, Undo2, Send } from '@lucide/svelte';
	import { Act, errText } from '$lib/act.svelte';
	import { isLocked, patchSettings, tierOf, type ScopeInit, type SettingsPatch } from '$lib/settings-utils';
	import {
		MailSecurity, SettingsScopeType, SettingsTier,
		type FieldProvenance, type Settings
	} from '$lib/proto/distroface/v1/settings_pb';

	let {
		scope,
		parentScope,
		canEdit = true
	}: {
		scope: ScopeInit;
		// Effective values one tier up, enables Custom tags and reset
		parentScope?: ScopeInit;
		canEdit?: boolean;
	} = $props();

	const P = {
		enabled: 'mail.enabled',
		smtpHost: 'mail.smtp_host',
		smtpPort: 'mail.smtp_port',
		security: 'mail.security',
		username: 'mail.username',
		password: 'mail.password',
		fromAddress: 'mail.from_address',
		fromName: 'mail.from_name',
		replyTo: 'mail.reply_to',
		allowPrivate: 'mail.allow_private_networks',
		skipTlsVerify: 'mail.skip_tls_verify',
		maxPerMinute: 'mail.max_per_minute',
		inEnabled: 'mail.incoming.enabled',
		imapHost: 'mail.incoming.imap_host',
		imapPort: 'mail.incoming.imap_port',
		imapTls: 'mail.incoming.tls',
		inUsername: 'mail.incoming.username',
		inPassword: 'mail.incoming.password',
		mailbox: 'mail.incoming.mailbox',
		replyAddress: 'mail.incoming.reply_address'
	} as const;
	type Path = (typeof P)[keyof typeof P];
	const PASSWORD_PATHS: Path[] = [P.password, P.inPassword];
	const VALUE_PATHS = (Object.values(P) as Path[]).filter((p) => !PASSWORD_PATHS.includes(p));
	// Lower tiers store the relay and sender together, never the throttle or inbox
	const RELAY_PATHS: Path[] = [P.smtpHost, P.smtpPort, P.security, P.username, P.password, P.fromAddress, P.fromName, P.replyTo, P.skipTlsVerify];
	const SCOPED_PATHS: Path[] = [P.enabled, ...RELAY_PATHS];

	const securityOptions = [
		{ value: MailSecurity.NONE, label: 'None' },
		{ value: MailSecurity.STARTTLS, label: 'STARTTLS' },
		{ value: MailSecurity.TLS, label: 'TLS' }
	];

	const ownTier = $derived(
		scope.type === SettingsScopeType.ORG ? SettingsTier.ORG
			: scope.type === SettingsScopeType.REPO ? SettingsTier.REPO
			: scope.type === SettingsScopeType.PORTAL ? SettingsTier.PORTAL
			: SettingsTier.SYSTEM
	);

	let loading = $state(true);
	let saving = $state(false);
	let resetting = $state(false);
	let prov = $state<FieldProvenance[]>([]);
	let eff = $state<Settings | null>(null);
	let inherited = $state<Settings | null>(null);
	const testAct = new Act();
	let testTo = $state('');

	let enabled = $state(false);
	let smtpHost = $state('');
	let smtpPort = $state(587);
	let security = $state<MailSecurity>(MailSecurity.STARTTLS);
	let username = $state('');
	let password = $state('');
	let passwordSet = $state(false);
	let fromAddress = $state('');
	let fromName = $state('');
	let replyTo = $state('');
	let allowPrivate = $state(true);
	let skipTlsVerify = $state(false);
	let maxPerMinute = $state(0);
	let ownRelay = $state(false);

	let inEnabled = $state(false);
	let imapHost = $state('');
	let imapPort = $state(993);
	let imapTls = $state(true);
	let inUsername = $state('');
	let inPassword = $state('');
	let inPasswordSet = $state(false);
	let mailbox = $state('');
	let replyAddress = $state('');

	const overridden = (path: string) => !!parentScope && tierOf(prov, path) === ownTier;
	const tag = (path: string) => (overridden(path) ? 'Custom' : undefined);
	const locked = (path: string) => isLocked(prov, path);
	const overrideCount = $derived((Object.values(P) as string[]).filter(overridden).length);
	const relayLocked = $derived(!!parentScope && !ownRelay);
	const securityLabel = $derived(securityOptions.find((o) => o.value === security)?.label ?? 'STARTTLS');

	function seed(s: Settings) {
		const m = s.mail;
		enabled = m?.enabled ?? false;
		smtpHost = m?.smtpHost ?? '';
		smtpPort = m?.smtpPort ?? 587;
		security = m?.security || MailSecurity.STARTTLS;
		username = m?.username ?? '';
		password = '';
		passwordSet = m?.passwordSet ?? false;
		fromAddress = m?.fromAddress ?? '';
		fromName = m?.fromName ?? '';
		replyTo = m?.replyTo ?? '';
		allowPrivate = m?.allowPrivateNetworks ?? true;
		skipTlsVerify = m?.skipTlsVerify ?? false;
		maxPerMinute = m?.maxPerMinute ?? 0;
		const i = m?.incoming;
		inEnabled = i?.enabled ?? false;
		imapHost = i?.imapHost ?? '';
		imapPort = i?.imapPort ?? 993;
		imapTls = i?.tls ?? true;
		inUsername = i?.username ?? '';
		inPassword = '';
		inPasswordSet = i?.passwordSet ?? false;
		mailbox = i?.mailbox ?? '';
		replyAddress = i?.replyAddress ?? '';
	}

	// Current form keyed by path, passwords excluded
	function formValues(): Record<Path, unknown> {
		return {
			[P.enabled]: enabled,
			[P.smtpHost]: smtpHost.trim(),
			[P.smtpPort]: Math.max(0, Math.round(Number(smtpPort) || 0)),
			[P.security]: security,
			[P.username]: username.trim(),
			[P.password]: undefined,
			[P.fromAddress]: fromAddress.trim(),
			[P.fromName]: fromName.trim(),
			[P.replyTo]: replyTo.trim(),
			[P.allowPrivate]: allowPrivate,
			[P.skipTlsVerify]: skipTlsVerify,
			[P.maxPerMinute]: Math.max(0, Math.round(Number(maxPerMinute) || 0)),
			[P.inEnabled]: inEnabled,
			[P.imapHost]: imapHost.trim(),
			[P.imapPort]: Math.max(0, Math.round(Number(imapPort) || 0)),
			[P.imapTls]: imapTls,
			[P.inUsername]: inUsername.trim(),
			[P.inPassword]: undefined,
			[P.mailbox]: mailbox.trim(),
			[P.replyAddress]: replyAddress.trim()
		};
	}

	function baseValues(s: Settings | null): Record<Path, unknown> {
		const m = s?.mail;
		const i = m?.incoming;
		return {
			[P.enabled]: m?.enabled ?? false,
			[P.smtpHost]: m?.smtpHost ?? '',
			[P.smtpPort]: m?.smtpPort ?? 587,
			[P.security]: m?.security || MailSecurity.STARTTLS,
			[P.username]: m?.username ?? '',
			[P.password]: undefined,
			[P.fromAddress]: m?.fromAddress ?? '',
			[P.fromName]: m?.fromName ?? '',
			[P.replyTo]: m?.replyTo ?? '',
			[P.allowPrivate]: m?.allowPrivateNetworks ?? true,
			[P.skipTlsVerify]: m?.skipTlsVerify ?? false,
			[P.maxPerMinute]: m?.maxPerMinute ?? 0,
			[P.inEnabled]: i?.enabled ?? false,
			[P.imapHost]: i?.imapHost ?? '',
			[P.imapPort]: i?.imapPort ?? 993,
			[P.imapTls]: i?.tls ?? true,
			[P.inUsername]: i?.username ?? '',
			[P.inPassword]: undefined,
			[P.mailbox]: i?.mailbox ?? '',
			[P.replyAddress]: i?.replyAddress ?? ''
		};
	}

	function buildPatch(keep: Path[], form: Record<Path, unknown>): SettingsPatch {
		const has = (p: Path) => keep.includes(p);
		return {
			mail: {
				...(has(P.enabled) ? { enabled: form[P.enabled] as boolean } : {}),
				...(has(P.smtpHost) ? { smtpHost: form[P.smtpHost] as string } : {}),
				...(has(P.smtpPort) ? { smtpPort: form[P.smtpPort] as number } : {}),
				...(has(P.security) ? { security: form[P.security] as MailSecurity } : {}),
				...(has(P.username) ? { username: form[P.username] as string } : {}),
				...(has(P.password) ? { password } : {}),
				...(has(P.fromAddress) ? { fromAddress: form[P.fromAddress] as string } : {}),
				...(has(P.fromName) ? { fromName: form[P.fromName] as string } : {}),
				...(has(P.replyTo) ? { replyTo: form[P.replyTo] as string } : {}),
				...(has(P.allowPrivate) ? { allowPrivateNetworks: form[P.allowPrivate] as boolean } : {}),
				...(has(P.skipTlsVerify) ? { skipTlsVerify: form[P.skipTlsVerify] as boolean } : {}),
				...(has(P.maxPerMinute) ? { maxPerMinute: form[P.maxPerMinute] as number } : {}),
				incoming: {
					...(has(P.inEnabled) ? { enabled: form[P.inEnabled] as boolean } : {}),
					...(has(P.imapHost) ? { imapHost: form[P.imapHost] as string } : {}),
					...(has(P.imapPort) ? { imapPort: form[P.imapPort] as number } : {}),
					...(has(P.imapTls) ? { tls: form[P.imapTls] as boolean } : {}),
					...(has(P.inUsername) ? { username: form[P.inUsername] as string } : {}),
					...(has(P.inPassword) ? { password: inPassword } : {}),
					...(has(P.mailbox) ? { mailbox: form[P.mailbox] as string } : {}),
					...(has(P.replyAddress) ? { replyAddress: form[P.replyAddress] as string } : {})
				}
			}
		};
	}

	async function load() {
		loading = true;
		try {
			const [own, parent] = await Promise.all([
				rpcClient.settings.getEffectiveSettings({ scope }, silentCallOptions),
				parentScope
					? rpcClient.settings.getEffectiveSettings({ scope: parentScope }, silentCallOptions)
					: Promise.resolve(null)
			]);
			prov = own.provenance;
			ownRelay = overridden(P.smtpHost);
			eff = own.settings ?? null;
			inherited = parent?.settings ?? null;
			if (eff) seed(eff);
		} catch (err) {
			toast.error(errText(err));
		} finally {
			loading = false;
		}
	}

	// Child scopes keep only values differing from the parent
	async function save() {
		saving = true;
		try {
			const form = formValues();
			const base = baseValues(inherited);
			const keep: Path[] = [];
			const paths: string[] = [];
			if (!parentScope) {
				for (const p of VALUE_PATHS) {
					if (locked(p)) continue;
					keep.push(p);
					paths.push(p);
				}
				if (password && !locked(P.password)) { keep.push(P.password); paths.push(P.password); }
				if (inPassword && !locked(P.inPassword)) { keep.push(P.inPassword); paths.push(P.inPassword); }
			} else {
				for (const p of SCOPED_PATHS) if (!locked(p)) paths.push(p);
				if (!locked(P.enabled) && form[P.enabled] !== base[P.enabled]) keep.push(P.enabled);
				if (ownRelay) {
					for (const p of RELAY_PATHS) if (!locked(p) && p !== P.password) keep.push(p);
					if (password && !locked(P.password)) keep.push(P.password);
					else {
						const i = paths.indexOf(P.password);
						if (i >= 0) paths.splice(i, 1);
					}
				}
			}
			const res = await patchSettings(scope, buildPatch(keep, form), paths);
			prov = res.provenance;
			ownRelay = overridden(P.smtpHost);
			if (res.effective) {
				eff = res.effective;
				seed(res.effective);
			}
			toast.success('Email settings saved');
		} catch (err) {
			toast.error(errText(err));
		} finally {
			saving = false;
		}
	}

	async function resetAll() {
		resetting = true;
		try {
			const res = await patchSettings(scope, {}, parentScope ? SCOPED_PATHS : Object.values(P));
			prov = res.provenance;
			ownRelay = overridden(P.smtpHost);
			if (res.effective) {
				eff = res.effective;
				seed(res.effective);
			}
			toast.success('Reset to inherited values');
		} catch (err) {
			toast.error(errText(err));
		} finally {
			resetting = false;
		}
	}

	async function sendTest() {
		const ok = await testAct.run(async () => {
			await rpcClient.mail.sendTestEmail({ scope, to: testTo.trim() }, silentCallOptions);
		});
		if (ok) toast.success('Test email queued');
	}

	onMount(load);
</script>

{#if loading}
	<div class="space-y-6">
		<Skeleton class="h-80 w-full rounded-xl" />
		<Skeleton class="h-40 w-full rounded-xl" />
	</div>
{:else}
	{#snippet actions()}
		{#if parentScope && overrideCount > 0}
			<Button variant="ghost" onclick={resetAll} disabled={resetting || saving || !canEdit} class="gap-2 mr-auto text-muted-foreground">
				<Undo2 class="h-4 w-4" />
				{resetting ? 'Resetting...' : 'Reset to inherited'}
			</Button>
		{/if}
		<Button onclick={save} disabled={saving || resetting || !canEdit} class="gap-2">
			<Save class="h-4 w-4" />
			{saving ? 'Saving...' : 'Save Changes'}
		</Button>
	{/snippet}
	<div class="space-y-6">
		<FormCard title="Outgoing mail" description="SMTP relay used for notifications, password resets, and invites." icon={Mail} footer={parentScope ? actions : undefined}>
			<div class="space-y-5">
				<FormField label="Enabled" horizontal tag={tag(P.enabled)} help={locked(P.enabled) ? 'Pinned by the config file' : 'Queues mail for delivery when on'}>
					<Switch bind:checked={enabled} disabled={!canEdit || locked(P.enabled)} />
				</FormField>
				{#if parentScope}
					<FormField label="Own relay" horizontal tag={tag(P.smtpHost)} help="Sends through this scope's SMTP relay and sender, off keeps the inherited relay and its sender">
						<Switch bind:checked={ownRelay} disabled={!canEdit || locked(P.smtpHost)} />
					</FormField>
				{/if}

				<div class="space-y-3">
					<p class="text-xs font-medium uppercase tracking-wide text-muted-foreground">Sender</p>
					<div class="grid gap-3 sm:grid-cols-2">
						<FormField label="From address" id="mail-from" tag={tag(P.fromAddress)}>
							<Input id="mail-from" type="email" bind:value={fromAddress} placeholder="noreply@example.com" disabled={!canEdit || locked(P.fromAddress) || relayLocked} />
						</FormField>
						<FormField label="From name" id="mail-from-name" tag={tag(P.fromName)}>
							<Input id="mail-from-name" bind:value={fromName} placeholder="Distroface" disabled={!canEdit || locked(P.fromName) || relayLocked} />
						</FormField>
					</div>
					<FormField label="Reply to" id="mail-reply-to" tag={tag(P.replyTo)} help="Optional address replies go to">
						<Input id="mail-reply-to" type="email" bind:value={replyTo} placeholder="support@example.com" disabled={!canEdit || locked(P.replyTo) || relayLocked} />
					</FormField>
				</div>

				<div class="space-y-3 pt-4 border-t border-border/40">
					<p class="text-xs font-medium uppercase tracking-wide text-muted-foreground">SMTP</p>
					<div class="grid gap-3 sm:grid-cols-[1fr_8rem_10rem]">
						<FormField label="Host" id="mail-host" tag={tag(P.smtpHost)}>
							<Input id="mail-host" bind:value={smtpHost} placeholder="smtp.example.com" disabled={!canEdit || locked(P.smtpHost) || relayLocked} />
						</FormField>
						<FormField label="Port" id="mail-port" tag={tag(P.smtpPort)}>
							<Input id="mail-port" type="number" min={1} max={65535} bind:value={smtpPort} disabled={!canEdit || locked(P.smtpPort) || relayLocked} />
						</FormField>
						<FormField label="Security" id="mail-security" tag={tag(P.security)}>
							<Select
								type="single"
								value={String(security)}
								onValueChange={(v) => { if (v) security = Number(v) as MailSecurity; }}
								disabled={!canEdit || locked(P.security) || relayLocked}
							>
								<SelectTrigger id="mail-security" class="w-full">{securityLabel}</SelectTrigger>
								<SelectContent>
									{#each securityOptions as o (o.value)}
										<SelectItem value={String(o.value)}>{o.label}</SelectItem>
									{/each}
								</SelectContent>
							</Select>
						</FormField>
					</div>
					<div class="grid gap-3 sm:grid-cols-2">
						<FormField label="Username" id="mail-user" tag={tag(P.username)}>
							<Input id="mail-user" bind:value={username} autocomplete="off" disabled={!canEdit || locked(P.username) || relayLocked} />
						</FormField>
						<FormField label="Password" id="mail-pass" tag={tag(P.password)} help={passwordSet ? 'A password is stored, leave blank to keep it' : 'Stored encrypted, never shown again'}>
							<PasswordInput id="mail-pass" bind:value={password} placeholder={passwordSet ? 'Unchanged' : 'Password'} autocomplete="new-password" disabled={!canEdit || locked(P.password) || relayLocked} />
						</FormField>
					</div>
					{#if !parentScope}
						<FormField label="Allow private networks" horizontal tag={tag(P.allowPrivate)} help="Relays on internal addresses are reachable">
							<Switch bind:checked={allowPrivate} disabled={!canEdit || locked(P.allowPrivate)} />
						</FormField>
					{/if}
					<FormField label="Skip TLS verification" horizontal tag={tag(P.skipTlsVerify)} help="Accepts self signed relay certificates">
						<Switch bind:checked={skipTlsVerify} disabled={!canEdit || locked(P.skipTlsVerify) || relayLocked} />
					</FormField>
					{#if !parentScope}
						<FormField label="Rate limit" id="mail-rate" tag={tag(P.maxPerMinute)} help="Messages per minute, 0 means unlimited">
							<UnitInput id="mail-rate" unit="per min" bind:value={maxPerMinute} min={0} class="w-full sm:w-40" disabled={!canEdit || locked(P.maxPerMinute)} />
						</FormField>
					{/if}
				</div>

				<div class="space-y-3 pt-4 border-t border-border/40">
					<p class="text-xs font-medium uppercase tracking-wide text-muted-foreground">Test</p>
					<FormField label="Send a test email" id="mail-test-to" help="Uses the saved settings, blank sends to you" tag={testAct.tag} error={testAct.error}>
						<div class="flex flex-col sm:flex-row gap-2">
							<Input id="mail-test-to" type="email" bind:value={testTo} placeholder="you@example.com" class="w-full sm:max-w-xs" />
							<Button variant="outline" size="sm" onclick={sendTest} disabled={testAct.busy || !enabled} class="h-9 gap-1.5 shrink-0">
								<Send class="h-3.5 w-3.5" />
								{testAct.busy ? 'Sending...' : 'Send Test'}
							</Button>
						</div>
					</FormField>
				</div>
			</div>
		</FormCard>

		{#if !parentScope}
		<FormCard title="Incoming mail" description="Replies to notification emails become comments through an IMAP mailbox." icon={Inbox} footer={actions}>
			<div class="space-y-3">
				<FormField label="Enabled" horizontal tag={tag(P.inEnabled)}>
					<Switch bind:checked={inEnabled} disabled={!canEdit || locked(P.inEnabled)} />
				</FormField>
				{#if inEnabled}
					<div class="space-y-3 pl-3 border-l-2 border-border/40">
						<div class="grid gap-3 sm:grid-cols-[1fr_8rem]">
							<FormField label="IMAP host" id="imap-host" tag={tag(P.imapHost)}>
								<Input id="imap-host" bind:value={imapHost} placeholder="imap.example.com" disabled={!canEdit || locked(P.imapHost)} />
							</FormField>
							<FormField label="Port" id="imap-port" tag={tag(P.imapPort)}>
								<Input id="imap-port" type="number" min={1} max={65535} bind:value={imapPort} disabled={!canEdit || locked(P.imapPort)} />
							</FormField>
						</div>
						<FormField label="TLS" horizontal tag={tag(P.imapTls)}>
							<Switch bind:checked={imapTls} disabled={!canEdit || locked(P.imapTls)} />
						</FormField>
						<div class="grid gap-3 sm:grid-cols-2">
							<FormField label="Username" id="imap-user" tag={tag(P.inUsername)}>
								<Input id="imap-user" bind:value={inUsername} autocomplete="off" disabled={!canEdit || locked(P.inUsername)} />
							</FormField>
							<FormField label="Password" id="imap-pass" tag={tag(P.inPassword)} help={inPasswordSet ? 'A password is stored, leave blank to keep it' : undefined}>
								<PasswordInput id="imap-pass" bind:value={inPassword} placeholder={inPasswordSet ? 'Unchanged' : 'Password'} autocomplete="new-password" disabled={!canEdit || locked(P.inPassword)} />
							</FormField>
						</div>
						<div class="grid gap-3 sm:grid-cols-2">
							<FormField label="Mailbox" id="imap-mailbox" tag={tag(P.mailbox)} help="Defaults to INBOX">
								<Input id="imap-mailbox" bind:value={mailbox} placeholder="INBOX" disabled={!canEdit || locked(P.mailbox)} />
							</FormField>
							<FormField label="Reply address" id="imap-reply" tag={tag(P.replyAddress)} help="Plus extension carries the reply token">
								<Input id="imap-reply" type="email" bind:value={replyAddress} placeholder="reply@example.com" disabled={!canEdit || locked(P.replyAddress)} />
							</FormField>
						</div>
					</div>
				{/if}
			</div>
		</FormCard>
		{/if}
	</div>
{/if}

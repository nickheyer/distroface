<script lang="ts">
	import { onMount } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import {
		Table, TableBody, TableCell, TableHead, TableHeader, TableRow
	} from '$lib/components/ui/table';
	import {
		Select, SelectContent, SelectItem, SelectTrigger
	} from '$lib/components/ui/select';
	import FormPanel from '$lib/components/form-panel.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import CopyButton from '$lib/components/copy-button.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import { KeyRound, Fingerprint, Plus, Trash2, ShieldCheck, Terminal, CircleAlert, Info } from '@lucide/svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { configStore } from '$lib/stores/config.svelte';
	import { toast } from 'svelte-sonner';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime } from '$lib/utils';
	import { Pager } from '$lib/pager.svelte';
	import { errText } from '$lib/act.svelte';
	import { ConnectError } from '@connectrpc/connect';
	import { SSHKeyUsage, type SSHKey, type GPGKey } from '$lib/proto/distroface/v1/keys_pb';

	let { ssh = true, gpg = true }: { ssh?: boolean; gpg?: boolean } = $props();

	const sshTransport = $derived(configStore.sshEnabled);

	const usageOptions = [
		{ value: SSHKeyUsage.SSH_KEY_USAGE_AUTH, label: 'Authentication' },
		{ value: SSHKeyUsage.SSH_KEY_USAGE_SIGNING, label: 'Signing' },
		{ value: SSHKeyUsage.SSH_KEY_USAGE_BOTH, label: 'Authentication and signing' }
	];
	const expiryOptions = [
		{ value: 'never', label: 'No expiration' },
		{ value: '30', label: '30 days' },
		{ value: '90', label: '90 days' },
		{ value: '365', label: '1 year' }
	];

	let sshKeys = $state<SSHKey[]>([]);
	const sshPager = new Pager(20);
	let sshLoaded = $state(false);
	let sshOpen = $state(false);
	let sshTitle = $state('');
	let sshText = $state('');
	let sshUsage = $state<SSHKeyUsage>(SSHKeyUsage.SSH_KEY_USAGE_AUTH);
	let sshExpiry = $state('never');
	let addingSsh = $state(false);
	let deleteSshOpen = $state(false);
	let deleteSsh = $state<SSHKey | null>(null);
	let deletingSsh = $state(false);

	let gpgKeys = $state<GPGKey[]>([]);
	const gpgPager = new Pager(20);
	let gpgLoaded = $state(false);
	let gpgOpen = $state(false);
	let gpgArmored = $state('');
	let addingGpg = $state(false);
	let verifyOpen = $state(false);
	let verifyKey = $state<GPGKey | null>(null);
	let challenge = $state('');
	let signature = $state('');
	let verifying = $state(false);
	let verifyError = $state('');
	let deleteGpgOpen = $state(false);
	let deleteGpg = $state<GPGKey | null>(null);
	let deletingGpg = $state(false);

	const sshValid = $derived(/^(ssh-(rsa|ed25519|dss)|ecdsa-sha2-nistp\d+|sk-(ssh-ed25519|ecdsa-sha2-nistp256)@openssh\.com)\s+[A-Za-z0-9+/=]+/.test(sshText.trim()));
	const gpgValid = $derived(gpgArmored.includes('-----BEGIN PGP PUBLIC KEY BLOCK-----'));
	const signCommand = $derived(
		`printf '%s' '${challenge}' | gpg --armor --detach-sign --default-key ${verifyKey?.keyId ?? 'KEYID'}`
	);

	function usageLabel(u: SSHKeyUsage): string {
		return usageOptions.find((o) => o.value === u)?.label ?? 'Authentication';
	}

	// Time left before a future date, coarse
	function untilText(d: Date): string {
		const hours = Math.floor((d.getTime() - Date.now()) / 3600000);
		if (hours < 1) return 'under 1h';
		if (hours < 48) return `${hours}h`;
		return `${Math.floor(hours / 24)}d`;
	}

	async function loadSsh() {
		try {
			const resp = await rpcClient.key.listSSHKeys({ page: sshPager.request() });
			sshKeys = resp.keys;
			sshPager.apply(resp.page);
		} catch {
			sshKeys = [];
		} finally {
			sshLoaded = true;
		}
	}

	async function loadGpg() {
		try {
			const resp = await rpcClient.key.listGPGKeys({ page: gpgPager.request() });
			gpgKeys = resp.keys;
			gpgPager.apply(resp.page);
		} catch {
			gpgKeys = [];
		} finally {
			gpgLoaded = true;
		}
	}

	function openSsh() {
		sshTitle = '';
		sshText = '';
		sshUsage = SSHKeyUsage.SSH_KEY_USAGE_AUTH;
		sshExpiry = 'never';
		sshOpen = true;
	}

	// Comment field of the pasted key seeds the title
	function sshPasted() {
		if (sshTitle.trim()) return;
		const parts = sshText.trim().split(/\s+/);
		if (parts.length >= 3) sshTitle = parts.slice(2).join(' ');
	}

	async function addSsh() {
		if (!sshValid) return;
		addingSsh = true;
		try {
			await rpcClient.key.addSSHKey({
				title: sshTitle.trim() || 'SSH key',
				publicKey: sshText.trim(),
				usage: sshUsage,
				expiresInDays: sshExpiry !== 'never' ? Number(sshExpiry) : undefined
			});
			toast.success('SSH key added');
			sshOpen = false;
			await loadSsh();
		} catch {
			// error interceptor
		} finally {
			addingSsh = false;
		}
	}

	async function confirmDeleteSsh() {
		if (!deleteSsh) return;
		deletingSsh = true;
		try {
			await rpcClient.key.deleteSSHKey({ id: deleteSsh.id });
			toast.success('SSH key removed');
			deleteSshOpen = false;
			await loadSsh();
		} catch {
			// error interceptor
		} finally {
			deletingSsh = false;
		}
	}

	function openGpg() {
		gpgArmored = '';
		gpgOpen = true;
	}

	async function addGpg() {
		if (!gpgValid) return;
		addingGpg = true;
		try {
			const resp = await rpcClient.key.addGPGKey({ armoredPublicKey: gpgArmored.trim() });
			toast.success('GPG key added');
			gpgOpen = false;
			await loadGpg();
			if (resp.key && resp.challenge) openVerify(resp.key, resp.challenge);
		} catch {
			// error interceptor
		} finally {
			addingGpg = false;
		}
	}

	function openVerify(key: GPGKey, ch: string) {
		verifyKey = key;
		challenge = ch;
		signature = '';
		verifyError = '';
		verifyOpen = true;
	}

	// An empty signature makes the server issue a fresh challenge
	async function startVerify(key: GPGKey) {
		try {
			const resp = await rpcClient.key.verifyGPGKey({ id: key.id, armoredSignature: '' }, silentCallOptions);
			if (resp.key?.verified) {
				toast.success('Key already verified');
				await loadGpg();
			}
		} catch (err) {
			const msg = err instanceof ConnectError ? err.rawMessage : '';
			const idx = msg.lastIndexOf(': ');
			if (idx > -1 && msg.includes('challenge')) {
				openVerify(key, msg.slice(idx + 2).trim());
			} else {
				toast.error(errText(err));
			}
		}
	}

	async function submitVerify() {
		if (!verifyKey || !signature.trim()) return;
		verifying = true;
		verifyError = '';
		try {
			await rpcClient.key.verifyGPGKey({ id: verifyKey.id, armoredSignature: signature.trim() }, silentCallOptions);
			toast.success('GPG key verified');
			verifyOpen = false;
			await loadGpg();
		} catch (err) {
			verifyError = errText(err);
		} finally {
			verifying = false;
		}
	}

	async function confirmDeleteGpg() {
		if (!deleteGpg) return;
		deletingGpg = true;
		try {
			await rpcClient.key.deleteGPGKey({ id: deleteGpg.id });
			toast.success('GPG key removed');
			deleteGpgOpen = false;
			await loadGpg();
		} catch {
			// error interceptor
		} finally {
			deletingGpg = false;
		}
	}

	onMount(() => {
		if (ssh) loadSsh();
		if (gpg) loadGpg();
	});
</script>

<div class="space-y-8">
	{#if ssh}
		<div class="space-y-4">
			<div class="section-header">
				<div class="min-w-0">
					<h2 class="section-title">SSH Keys</h2>
					<p class="section-subtitle">{sshTransport ? 'Keys that authenticate git over SSH or sign commits.' : 'Keys that sign commits and tags.'}</p>
				</div>
				<Button size="sm" class="shrink-0" onclick={openSsh}>
					<Plus class="h-4 w-4 mr-1.5" />
					Add SSH Key
				</Button>
			</div>

			{#if !sshTransport}
				<Alert>
					<Info class="h-4 w-4" />
					<AlertDescription>Git over SSH is disabled on this instance. Keys still verify signed commits and can be used once an admin enables it.</AlertDescription>
				</Alert>
			{/if}

			{#if !sshLoaded}
				<Skeleton class="h-24 w-full rounded-lg" />
			{:else if sshKeys.length === 0}
				<EmptyState icon={KeyRound} message="No SSH keys" description={sshTransport ? 'Add a public key to clone and push over SSH.' : 'Add a public key to mark your signed commits as verified.'}>
					{#snippet actions()}
						<Button variant="outline" size="sm" onclick={openSsh}>
							<Plus class="h-4 w-4 mr-1.5" />
							Add SSH Key
						</Button>
					{/snippet}
				</EmptyState>
			{:else}
				<div class="data-table">
					<Table>
						<TableHeader>
							<TableRow class="bg-muted/30 hover:bg-muted/30">
								<TableHead class="th">Key</TableHead>
								<TableHead class="th w-40">Usage</TableHead>
								<TableHead class="th w-28 hidden md:table-cell">Last used</TableHead>
								<TableHead class="th w-28 hidden md:table-cell">Expires</TableHead>
								<TableHead class="th w-12"></TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{#each sshKeys as key (key.id)}
								<TableRow>
									<TableCell class="py-3 px-3">
										<p class="font-medium">{key.title}</p>
										<p class="text-[12px] text-muted-foreground font-mono mt-0.5 break-all">
											{key.keyType}{key.keyType ? ' ' : ''}{key.fingerprint}
										</p>
									</TableCell>
									<TableCell class="py-3 px-3">
										<Badge variant="outline" class="text-[11px]">{usageLabel(key.usage)}</Badge>
									</TableCell>
									<TableCell class="text-sm text-muted-foreground py-3 px-3 hidden md:table-cell">
										{key.lastUsedAt ? relativeTime(timestampDate(key.lastUsedAt)) : 'Never'}
									</TableCell>
									<TableCell class="text-sm py-3 px-3 hidden md:table-cell">
										{#if key.expiresAt}
											{@const expires = timestampDate(key.expiresAt)}
											<Badge variant={expires < new Date() ? 'destructive' : 'outline'} class="text-[11px]">
												{expires < new Date() ? 'Expired' : `in ${untilText(expires)}`}
											</Badge>
										{:else}
											<Badge variant="secondary" class="text-[11px]">Never</Badge>
										{/if}
									</TableCell>
									<TableCell class="text-right py-3 px-3">
										<Button
											variant="ghost" size="icon"
											class="h-7 w-7 text-destructive hover:text-destructive"
											onclick={() => { deleteSsh = key; deleteSshOpen = true; }}
											aria-label="Delete"
										>
											<Trash2 class="h-3.5 w-3.5" />
										</Button>
									</TableCell>
								</TableRow>
							{/each}
						</TableBody>
					</Table>
					<DataPagination attached pager={sshPager} onChange={loadSsh} />
				</div>
			{/if}
		</div>
	{/if}

	{#if gpg}
		<div class="space-y-4">
			<div class="section-header">
				<div class="min-w-0">
					<h2 class="section-title">GPG Keys</h2>
					<p class="section-subtitle">Public keys that mark your signed commits and tags as verified.</p>
				</div>
				<Button size="sm" variant="outline" class="shrink-0" onclick={openGpg}>
					<Plus class="h-4 w-4 mr-1.5" />
					Add GPG Key
				</Button>
			</div>

			{#if !gpgLoaded}
				<Skeleton class="h-24 w-full rounded-lg" />
			{:else if gpgKeys.length === 0}
				<EmptyState icon={Fingerprint} message="No GPG keys" description="Add an armored public key and sign a challenge to verify it." />
			{:else}
				<div class="data-table">
					<Table>
						<TableHeader>
							<TableRow class="bg-muted/30 hover:bg-muted/30">
								<TableHead class="th">Key</TableHead>
								<TableHead class="th hidden md:table-cell">Emails</TableHead>
								<TableHead class="th w-28">Status</TableHead>
								<TableHead class="th w-28 hidden md:table-cell">Expires</TableHead>
								<TableHead class="th w-32"></TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{#each gpgKeys as key (key.id)}
								<TableRow>
									<TableCell class="py-3 px-3">
										<p class="font-mono text-sm">{key.keyId}</p>
										<p class="text-[12px] text-muted-foreground font-mono mt-0.5 break-all">{key.fingerprint}</p>
									</TableCell>
									<TableCell class="text-sm text-muted-foreground py-3 px-3 hidden md:table-cell">
										{key.emails.join(', ') || '-'}
									</TableCell>
									<TableCell class="py-3 px-3">
										{#if key.verified}
											<Badge class="text-[11px] gap-1">
												<ShieldCheck class="h-3 w-3" />
												Verified
											</Badge>
										{:else}
											<Badge variant="outline" class="text-[11px] gap-1">
												<CircleAlert class="h-3 w-3" />
												Unverified
											</Badge>
										{/if}
									</TableCell>
									<TableCell class="text-sm py-3 px-3 hidden md:table-cell">
										{#if key.expiresAt}
											{@const expires = timestampDate(key.expiresAt)}
											<Badge variant={expires < new Date() ? 'destructive' : 'outline'} class="text-[11px]">
												{expires < new Date() ? 'Expired' : `in ${untilText(expires)}`}
											</Badge>
										{:else}
											<Badge variant="secondary" class="text-[11px]">Never</Badge>
										{/if}
									</TableCell>
									<TableCell class="text-right py-3 px-3">
										<div class="flex justify-end gap-1">
											{#if !key.verified}
												<Button variant="ghost" size="sm" class="h-7" onclick={() => startVerify(key)}>Verify</Button>
											{/if}
											<Button
												variant="ghost" size="icon"
												class="h-7 w-7 text-destructive hover:text-destructive"
												onclick={() => { deleteGpg = key; deleteGpgOpen = true; }}
												aria-label="Delete"
											>
												<Trash2 class="h-3.5 w-3.5" />
											</Button>
										</div>
									</TableCell>
								</TableRow>
							{/each}
						</TableBody>
					</Table>
					<DataPagination attached pager={gpgPager} onChange={loadGpg} />
				</div>
			{/if}
		</div>
	{/if}
</div>

<FormPanel bind:open={sshOpen} title="Add SSH Key" description="Paste the contents of your public key file, usually id_ed25519.pub." icon={KeyRound}>
	<FormSection title="Key">
		<div class="space-y-3">
			<FormField label="Public key" id="ssh-key" required error={sshText && !sshValid ? 'Expected an OpenSSH public key' : undefined}>
				<Textarea id="ssh-key" bind:value={sshText} oninput={sshPasted} rows={4} class="font-mono text-xs" placeholder="ssh-ed25519 AAAA... you@host" spellcheck={false} />
			</FormField>
			<FormField label="Title" id="ssh-title" help="Defaults to the key comment">
				<Input id="ssh-title" bind:value={sshTitle} placeholder="Work laptop" />
			</FormField>
			<div class="grid gap-3 sm:grid-cols-2">
				<FormField label="Usage" id="ssh-usage">
					<Select type="single" value={String(sshUsage)} onValueChange={(v) => { if (v) sshUsage = Number(v) as SSHKeyUsage; }}>
						<SelectTrigger id="ssh-usage" class="w-full">{usageLabel(sshUsage)}</SelectTrigger>
						<SelectContent>
							{#each usageOptions as o (o.value)}
								<SelectItem value={String(o.value)}>{o.label}</SelectItem>
							{/each}
						</SelectContent>
					</Select>
				</FormField>
				<FormField label="Expiration" id="ssh-expiry">
					<Select type="single" value={sshExpiry} onValueChange={(v) => { if (v) sshExpiry = v; }}>
						<SelectTrigger id="ssh-expiry" class="w-full">{expiryOptions.find((o) => o.value === sshExpiry)?.label}</SelectTrigger>
						<SelectContent>
							{#each expiryOptions as o (o.value)}
								<SelectItem value={o.value}>{o.label}</SelectItem>
							{/each}
						</SelectContent>
					</Select>
				</FormField>
			</div>
		</div>
	</FormSection>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (sshOpen = false)}>Cancel</Button>
		<Button onclick={addSsh} disabled={addingSsh || !sshValid}>
			{addingSsh ? 'Adding...' : 'Add Key'}
		</Button>
	{/snippet}
</FormPanel>

<FormPanel bind:open={gpgOpen} title="Add GPG Key" description="Export with gpg --armor --export KEYID and paste the block." icon={Fingerprint}>
	<FormSection title="Armored public key">
		<FormField label="Key" id="gpg-key" required error={gpgArmored && !gpgValid ? 'Expected a PGP PUBLIC KEY BLOCK' : undefined}>
			<Textarea id="gpg-key" bind:value={gpgArmored} rows={10} class="font-mono text-xs" placeholder="-----BEGIN PGP PUBLIC KEY BLOCK-----" spellcheck={false} />
		</FormField>
	</FormSection>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (gpgOpen = false)}>Cancel</Button>
		<Button onclick={addGpg} disabled={addingGpg || !gpgValid}>
			{addingGpg ? 'Adding...' : 'Add Key'}
		</Button>
	{/snippet}
</FormPanel>

<FormPanel bind:open={verifyOpen} title="Verify GPG Key" description="Sign the challenge with the key to prove you hold its private half." icon={ShieldCheck}>
	<div class="space-y-6">
		<Alert>
			<CircleAlert class="h-4 w-4" />
			<AlertDescription>Until verified, commits signed with {verifyKey?.keyId} show as unverified.</AlertDescription>
		</Alert>
		<div class="rounded-xl border border-border/50 bg-muted/20 overflow-hidden">
			<div class="flex items-center gap-2 px-4 py-2.5 border-b border-border/40 bg-muted/30">
				<Terminal class="h-3.5 w-3.5 text-muted-foreground" />
				<span class="text-xs font-medium text-muted-foreground">Sign the challenge</span>
			</div>
			<div class="p-4 space-y-2">
				<pre class="code-inline block text-xs whitespace-pre-wrap break-all">{signCommand}</pre>
				<CopyButton text={signCommand} label="Command copied!" />
			</div>
		</div>
		<FormField label="Armored signature" id="gpg-sig" required error={verifyError || undefined}>
			<Textarea id="gpg-sig" bind:value={signature} rows={8} class="font-mono text-xs" placeholder="-----BEGIN PGP SIGNATURE-----" spellcheck={false} />
		</FormField>
	</div>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (verifyOpen = false)}>Later</Button>
		<Button onclick={submitVerify} disabled={verifying || !signature.trim()}>
			{verifying ? 'Verifying...' : 'Verify'}
		</Button>
	{/snippet}
</FormPanel>

<ConfirmDialog bind:open={deleteSshOpen} title="Remove SSH Key" confirmLabel="Remove" onConfirm={confirmDeleteSsh} loading={deletingSsh} icon={Trash2}>
	{#snippet description()}
		Remove <strong>{deleteSsh?.title}</strong>? Clients using it stop authenticating immediately.
	{/snippet}
</ConfirmDialog>

<ConfirmDialog bind:open={deleteGpgOpen} title="Remove GPG Key" confirmLabel="Remove" onConfirm={confirmDeleteGpg} loading={deletingGpg} icon={Trash2}>
	{#snippet description()}
		Remove <strong class="font-mono">{deleteGpg?.keyId}</strong>? Commits it signed show as unverified.
	{/snippet}
</ConfirmDialog>

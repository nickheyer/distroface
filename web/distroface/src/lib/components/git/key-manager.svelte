<script lang="ts">
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import {
		Table,
		TableBody,
		TableCell,
		TableHead,
		TableHeader,
		TableRow
	} from '$lib/components/ui/table';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import {
		Key as KeyRound,
		Fingerprint,
		Plus,
		Trash,
		ShieldCheck,
		AlertCircle,
		Info
	} from '$lib/icons';
	import { rpcClient } from '$lib/api/rpc-client';
	import { configStore } from '$lib/stores/config.svelte';
	import { toast } from 'svelte-sonner';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime } from '$lib/utils';
	import { Pager } from '$lib/pager.svelte';
	import { SSHKeyUsage, type SSHKey, type GPGKey } from '$lib/proto/distroface/v1/keys_pb';

	let { ssh = true, gpg = true }: { ssh?: boolean; gpg?: boolean } = $props();

	const sshTransport = $derived(configStore.sshEnabled);
	const newSshHref = resolve('/settings/keys/new');
	const newGpgHref = `${resolve('/settings/keys/new')}?type=gpg`;

	const usageOptions = [
		{ value: SSHKeyUsage.SSH_KEY_USAGE_AUTH, label: 'Authentication' },
		{ value: SSHKeyUsage.SSH_KEY_USAGE_SIGNING, label: 'Signing' },
		{ value: SSHKeyUsage.SSH_KEY_USAGE_BOTH, label: 'Authentication and signing' }
	];

	let sshKeys = $state<SSHKey[]>([]);
	const sshPager = new Pager(20);
	let sshLoaded = $state(false);
	let deleteSshOpen = $state(false);
	let deleteSsh = $state<SSHKey | null>(null);
	let deletingSsh = $state(false);

	let gpgKeys = $state<GPGKey[]>([]);
	const gpgPager = new Pager(20);
	let gpgLoaded = $state(false);
	let deleteGpgOpen = $state(false);
	let deleteGpg = $state<GPGKey | null>(null);
	let deletingGpg = $state(false);

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

	const verifyHref = (key: GPGKey) => resolve('/settings/keys/[id]/verify', { id: key.id });

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

<div class="space-y-6">
	{#if ssh}
		<div class="space-y-4">
			<div class="section-header">
				<div class="min-w-0">
					<h2 class="section-title">SSH keys</h2>
					<p class="section-subtitle">
						{sshTransport
							? 'Keys that authenticate git over SSH or sign commits.'
							: 'Keys that sign commits and tags.'}
					</p>
				</div>
				<Button class="shrink-0" href={newSshHref}>
					<Plus />
					Add SSH key
				</Button>
			</div>

			{#if !sshTransport}
				<Alert variant="info">
					<Info />
					<AlertDescription
						>Git over SSH is disabled on this instance. Keys still verify signed commits and can be
						used once an admin enables it.</AlertDescription
					>
				</Alert>
			{/if}

			{#if !sshLoaded}
				<div class="box divide-y">
					{#each { length: 2 }, i (i)}
						<div class="flex items-center gap-3 px-4 py-3">
							<div class="flex-1 space-y-2">
								<Skeleton class="h-4 w-32" />
								<Skeleton class="h-3 w-72" />
							</div>
							<Skeleton class="h-5 w-24 rounded-full" />
						</div>
					{/each}
				</div>
			{:else if sshKeys.length === 0}
				<EmptyState
					icon={KeyRound}
					message="No SSH keys yet"
					description={sshTransport
						? 'Add a public key to clone and push over SSH.'
						: 'Add a public key to mark your signed commits as verified.'}
				>
					{#snippet actions()}
						<Button variant="outline" href={newSshHref}>
							<Plus />
							Add SSH key
						</Button>
					{/snippet}
				</EmptyState>
			{:else}
				<div class="data-table">
					<Table>
						<TableHeader>
							<TableRow>
								<TableHead>Key</TableHead>
								<TableHead class="w-40">Usage</TableHead>
								<TableHead class="hidden w-28 md:table-cell">Last used</TableHead>
								<TableHead class="hidden w-28 md:table-cell">Expires</TableHead>
								<TableHead class="w-12"></TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{#each sshKeys as key (key.id)}
								<TableRow>
									<TableCell>
										<p class="font-medium">{key.title}</p>
										<p class="mt-0.5 font-mono text-xs break-all text-muted-foreground">
											{key.keyType}{key.keyType ? ' ' : ''}{key.fingerprint}
										</p>
									</TableCell>
									<TableCell>
										<Badge variant="outline">{usageLabel(key.usage)}</Badge>
									</TableCell>
									<TableCell class="hidden text-muted-foreground md:table-cell">
										{key.lastUsedAt ? relativeTime(timestampDate(key.lastUsedAt)) : 'Never'}
									</TableCell>
									<TableCell class="hidden md:table-cell">
										{#if key.expiresAt}
											{@const expires = timestampDate(key.expiresAt)}
											<Badge variant={expires < new Date() ? 'destructive' : 'outline'}>
												{expires < new Date() ? 'Expired' : `in ${untilText(expires)}`}
											</Badge>
										{:else}
											<Badge variant="secondary">Never</Badge>
										{/if}
									</TableCell>
									<TableCell class="text-right">
										<Button
											variant="ghost-muted"
											size="icon-sm"
											class="hover:text-destructive"
											onclick={() => {
												deleteSsh = key;
												deleteSshOpen = true;
											}}
											aria-label="Delete"
										>
											<Trash />
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
					<h2 class="section-title">GPG keys</h2>
					<p class="section-subtitle">
						Public keys that mark your signed commits and tags as verified.
					</p>
				</div>
				<Button variant="outline" class="shrink-0" href={newGpgHref}>
					<Plus />
					Add GPG key
				</Button>
			</div>

			{#if !gpgLoaded}
				<div class="box divide-y">
					{#each { length: 2 }, i (i)}
						<div class="flex items-center gap-3 px-4 py-3">
							<div class="flex-1 space-y-2">
								<Skeleton class="h-4 w-32" />
								<Skeleton class="h-3 w-72" />
							</div>
							<Skeleton class="h-5 w-24 rounded-full" />
						</div>
					{/each}
				</div>
			{:else if gpgKeys.length === 0}
				<EmptyState
					icon={Fingerprint}
					message="No GPG keys yet"
					description="Add an armored public key and sign a challenge to verify it."
				/>
			{:else}
				<div class="data-table">
					<Table>
						<TableHeader>
							<TableRow>
								<TableHead>Key</TableHead>
								<TableHead class="hidden md:table-cell">Emails</TableHead>
								<TableHead class="w-28">Status</TableHead>
								<TableHead class="hidden w-28 md:table-cell">Expires</TableHead>
								<TableHead class="w-32"></TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{#each gpgKeys as key (key.id)}
								<TableRow>
									<TableCell>
										<p class="font-mono">{key.keyId}</p>
										<p class="mt-0.5 font-mono text-xs break-all text-muted-foreground">
											{key.fingerprint}
										</p>
									</TableCell>
									<TableCell class="hidden text-muted-foreground md:table-cell">
										{key.emails.join(', ') || '-'}
									</TableCell>
									<TableCell>
										{#if key.verified}
											<Badge variant="success">
												<ShieldCheck />
												Verified
											</Badge>
										{:else}
											<Badge variant="warning">
												<AlertCircle />
												Unverified
											</Badge>
										{/if}
									</TableCell>
									<TableCell class="hidden md:table-cell">
										{#if key.expiresAt}
											{@const expires = timestampDate(key.expiresAt)}
											<Badge variant={expires < new Date() ? 'destructive' : 'outline'}>
												{expires < new Date() ? 'Expired' : `in ${untilText(expires)}`}
											</Badge>
										{:else}
											<Badge variant="secondary">Never</Badge>
										{/if}
									</TableCell>
									<TableCell class="text-right">
										<div class="flex justify-end gap-1">
											{#if !key.verified}
												<Button variant="outline" size="sm" href={verifyHref(key)}>Verify</Button>
											{/if}
											<Button
												variant="ghost-muted"
												size="icon-sm"
												class="hover:text-destructive"
												onclick={() => {
													deleteGpg = key;
													deleteGpgOpen = true;
												}}
												aria-label="Delete"
											>
												<Trash />
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

<ConfirmDialog
	bind:open={deleteSshOpen}
	title="Remove SSH key"
	confirmLabel="Remove"
	onConfirm={confirmDeleteSsh}
	loading={deletingSsh}
	icon={Trash}
>
	{#snippet description()}
		Remove <strong>{deleteSsh?.title}</strong>? Clients using it stop authenticating immediately.
	{/snippet}
</ConfirmDialog>

<ConfirmDialog
	bind:open={deleteGpgOpen}
	title="Remove GPG key"
	confirmLabel="Remove"
	onConfirm={confirmDeleteGpg}
	loading={deletingGpg}
	icon={Trash}
>
	{#snippet description()}
		Remove <strong class="font-mono">{deleteGpg?.keyId}</strong>? Commits it signed show as
		unverified.
	{/snippet}
</ConfirmDialog>

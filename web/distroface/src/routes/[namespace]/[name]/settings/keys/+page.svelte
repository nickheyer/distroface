<script lang="ts">
	import { page } from '$app/state';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { toast } from 'svelte-sonner';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime } from '$lib/utils';
	import { Pager } from '$lib/pager.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Switch } from '$lib/components/ui/switch';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '$lib/components/ui/table';
	import FormPanel from '$lib/components/form-panel.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import { KeyRound, Plus, Trash2, Lock, PenLine, Info } from '@lucide/svelte';
	import { SSHKeyUsage, type SSHKey } from '$lib/proto/distroface/v1/keys_pb';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);

	let keys = $state<SSHKey[]>([]);
	let loading = $state(false);
	let loaded = $state(false);
	let loadedFor = $state('');
	const pager = new Pager(20);

	let addOpen = $state(false);
	let title = $state('');
	let publicKey = $state('');
	let readOnly = $state(true);
	let expiry = $state('never');
	let adding = $state(false);

	let deleteOpen = $state(false);
	let deleteTarget = $state<SSHKey | null>(null);
	let deleting = $state(false);

	const expiryOptions = [
		{ value: '30', label: '30 days' },
		{ value: '90', label: '90 days' },
		{ value: '365', label: '1 year' },
		{ value: 'never', label: 'No expiration' }
	];

	const keyLooksValid = $derived(/^(ssh-(rsa|ed25519|dss)|ecdsa-sha2-nistp\d+|sk-)\S*\s+\S+/.test(publicKey.trim()));

	$effect(() => {
		if (repo && repo.fullName !== loadedFor) {
			loadedFor = repo.fullName;
			pager.reset();
			void load();
		}
	});

	async function load() {
		loading = true;
		try {
			const resp = await rpcClient.key.listSSHKeys({ namespace, name, page: pager.request() }, silentCallOptions);
			keys = resp.keys;
			pager.apply(resp.page);
		} catch {
			keys = [];
		} finally {
			loading = false;
			loaded = true;
		}
	}

	function openAdd() {
		title = '';
		publicKey = '';
		readOnly = true;
		expiry = 'never';
		addOpen = true;
	}

	async function add() {
		if (!title.trim() || !keyLooksValid) return;
		adding = true;
		try {
			await rpcClient.key.addSSHKey({
				namespace, name,
				title: title.trim(),
				publicKey: publicKey.trim(),
				usage: SSHKeyUsage.SSH_KEY_USAGE_AUTH,
				readOnly,
				expiresInDays: expiry === 'never' ? undefined : Number(expiry)
			});
			toast.success('Deploy key added');
			addOpen = false;
			await load();
		} catch {
			// Error interceptor already toasted
		} finally {
			adding = false;
		}
	}

	async function remove() {
		if (!deleteTarget) return;
		deleting = true;
		try {
			await rpcClient.key.deleteSSHKey({ namespace, name, id: deleteTarget.id });
			toast.success('Deploy key removed');
			deleteOpen = false;
			await load();
		} catch {
			// Error interceptor already toasted
		} finally {
			deleting = false;
		}
	}
</script>

<div class="space-y-4">
	<div class="section-header">
		<div class="min-w-0">
			<h2 class="section-title">Deploy keys</h2>
			<p class="section-subtitle">SSH keys that reach only this repository, read only unless you allow pushes.</p>
		</div>
		<Button size="sm" class="shrink-0" onclick={openAdd} disabled={!repo}>
			<Plus class="h-4 w-4 mr-1.5" />Add Key
		</Button>
	</div>

	{#if !configStore.sshEnabled}
		<Alert>
			<Info class="h-4 w-4" />
			<AlertDescription>SSH is disabled on this instance, deploy keys cannot be used until an admin enables it.</AlertDescription>
		</Alert>
	{/if}

	{#if !loaded}
		<Skeleton class="h-40 w-full rounded-xl" />
	{:else if keys.length === 0}
		<EmptyState icon={KeyRound} message="No deploy keys" description="Give a server or CI job clone access without a user account.">
			{#snippet actions()}
				<Button variant="outline" size="sm" onclick={openAdd}>
					<Plus class="h-4 w-4 mr-1.5" />Add Key
				</Button>
			{/snippet}
		</EmptyState>
	{:else}
		<div class="data-table transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
			<Table>
				<TableHeader>
					<TableRow>
						<TableHead class="th">Key</TableHead>
						<TableHead class="th">Access</TableHead>
						<TableHead class="th hidden md:table-cell">Last used</TableHead>
						<TableHead class="th hidden md:table-cell">Expires</TableHead>
						<TableHead class="th w-12"></TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{#each keys as key (key.id)}
						<TableRow>
							<TableCell class="py-3 px-3">
								<div class="min-w-0">
									<p class="text-sm font-medium truncate">{key.title}</p>
									<p class="font-mono text-xs text-muted-foreground truncate">{key.keyType} {key.fingerprint}</p>
								</div>
							</TableCell>
							<TableCell class="py-3 px-3">
								{#if key.readOnly}
									<Badge variant="outline" class="text-xs gap-1"><Lock class="h-2.5 w-2.5" />Read only</Badge>
								{:else}
									<Badge variant="outline" class="text-xs gap-1 border-amber-500/30 text-amber-600 dark:text-amber-400"><PenLine class="h-2.5 w-2.5" />Read and write</Badge>
								{/if}
							</TableCell>
							<TableCell class="py-3 px-3 text-sm text-muted-foreground hidden md:table-cell">
								{key.lastUsedAt ? relativeTime(timestampDate(key.lastUsedAt)) : 'Never'}
							</TableCell>
							<TableCell class="py-3 px-3 text-sm hidden md:table-cell">
								{#if key.expiresAt}
									{@const d = timestampDate(key.expiresAt)}
									<Badge variant={d < new Date() ? 'destructive' : 'outline'} class="text-xs">
										{d < new Date() ? 'Expired' : d.toLocaleDateString()}
									</Badge>
								{:else}
									<Badge variant="secondary" class="text-xs">Never</Badge>
								{/if}
							</TableCell>
							<TableCell class="py-3 px-3 text-right">
								<Button
									variant="ghost" size="icon" class="h-7 w-7 text-destructive hover:text-destructive"
									aria-label="Remove {key.title}"
									onclick={() => { deleteTarget = key; deleteOpen = true; }}
								>
									<Trash2 class="h-3.5 w-3.5" />
								</Button>
							</TableCell>
						</TableRow>
					{/each}
				</TableBody>
			</Table>
			<DataPagination attached {pager} onChange={load} />
		</div>
	{/if}
</div>

<FormPanel bind:open={addOpen} title="Add Deploy Key" description="Paste the public half of an SSH key." icon={KeyRound}>
	<div class="space-y-3">
		<FormField label="Title" id="key-title" required>
			<Input id="key-title" bind:value={title} placeholder="Production deploy" />
		</FormField>
		<FormField label="Public key" id="key-public" required help="Starts with ssh-ed25519, ssh-rsa, or ecdsa-sha2">
			<Textarea id="key-public" bind:value={publicKey} rows={4} class="font-mono text-xs" placeholder="ssh-ed25519 AAAA..." spellcheck={false} />
		</FormField>
		<FormField label="Read only" horizontal help="Off allows this key to push">
			<Switch bind:checked={readOnly} />
		</FormField>
		<FormField label="Expiration" id="key-expiry">
			<Select type="single" value={expiry} onValueChange={(v) => { if (v) expiry = v; }}>
				<SelectTrigger id="key-expiry" class="w-full">{expiryOptions.find((o) => o.value === expiry)?.label}</SelectTrigger>
				<SelectContent>
					{#each expiryOptions as o (o.value)}
						<SelectItem value={o.value}>{o.label}</SelectItem>
					{/each}
				</SelectContent>
			</Select>
		</FormField>
	</div>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (addOpen = false)}>Cancel</Button>
		<Button onclick={add} disabled={adding || !title.trim() || !keyLooksValid}>{adding ? 'Adding...' : 'Add Key'}</Button>
	{/snippet}
</FormPanel>

<ConfirmDialog bind:open={deleteOpen} title="Remove Deploy Key" confirmLabel="Remove" onConfirm={remove} loading={deleting} icon={Trash2}>
	{#snippet description()}
		<strong>{deleteTarget?.title}</strong> stops authenticating immediately.
	{/snippet}
</ConfirmDialog>

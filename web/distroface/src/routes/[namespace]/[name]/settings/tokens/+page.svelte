<script lang="ts">
	import { page } from '$app/state';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { authStore } from '$lib/stores/auth.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { portalStore } from '$lib/stores/portal.svelte';
	import { toast } from 'svelte-sonner';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime } from '$lib/utils';
	import { Pager } from '$lib/pager.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Input } from '$lib/components/ui/input';
	import { Switch } from '$lib/components/ui/switch';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '$lib/components/ui/table';
	import FormPanel from '$lib/components/form-panel.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import CopyButton from '$lib/components/copy-button.svelte';
	import { Key, Plus, Trash2, AlertTriangle, CheckCircle, Terminal, Lock, PenLine } from '@lucide/svelte';
	import { CredentialScopeType, type APIToken } from '$lib/proto/distroface/v1/types_pb';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const fullName = $derived(`${namespace}/${name}`);
	const repo = $derived(gitRepoStore.repo);

	let tokens = $state<APIToken[]>([]);
	let loading = $state(false);
	let loaded = $state(false);
	let loadedFor = $state('');
	const pager = new Pager(20);

	let createOpen = $state(false);
	let tokenName = $state('');
	let readOnly = $state(true);
	let expiry = $state('never');
	let creating = $state(false);
	let plaintext = $state<string | null>(null);

	let revokeOpen = $state(false);
	let revokeTarget = $state<APIToken | null>(null);
	let revoking = $state(false);

	const expiryOptions = [
		{ value: '7', label: '7 days' },
		{ value: '30', label: '30 days' },
		{ value: '90', label: '90 days' },
		{ value: '365', label: '1 year' },
		{ value: 'never', label: 'No expiration' }
	];

	const host = $derived(portalStore.host(configStore.publicHostname));
	const scheme = $derived(portalStore.scheme());
	const username = $derived(authStore.user?.username ?? 'USER');
	const cloneExample = $derived(
		`git clone ${scheme}://${username}:${plaintext ?? 'YOUR_TOKEN'}@${host}/${fullName}.git`
	);
	const helperExample = $derived(
		`dfcli --server ${scheme}://${host} login --token ${plaintext ?? 'YOUR_TOKEN'}\ngit config --global credential.helper "dfcli git credential"`
	);
	const dockerExample = $derived(
		`docker login ${host} -u ${username} -p ${plaintext ?? 'YOUR_TOKEN'}`
	);

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
			const resp = await rpcClient.token.listAPITokens(
				{ page: pager.request(), gitRepo: fullName },
				silentCallOptions
			);
			tokens = resp.tokens;
			pager.apply(resp.page);
		} catch {
			tokens = [];
		} finally {
			loading = false;
			loaded = true;
		}
	}

	function openCreate() {
		tokenName = '';
		readOnly = true;
		expiry = 'never';
		plaintext = null;
		createOpen = true;
	}

	function closeCreate() {
		createOpen = false;
		plaintext = null;
	}

	async function create() {
		if (!tokenName.trim()) return;
		creating = true;
		try {
			const resp = await rpcClient.token.createAPIToken({
				name: tokenName.trim(),
				expiresInDays: expiry === 'never' ? undefined : Number(expiry),
				scope: { type: CredentialScopeType.GIT_REPO, id: fullName, readOnly }
			});
			plaintext = resp.plaintextToken;
			toast.success('Deploy token created');
			await load();
		} catch {
			// Error interceptor already toasted
		} finally {
			creating = false;
		}
	}

	async function revoke() {
		if (!revokeTarget) return;
		revoking = true;
		try {
			await rpcClient.token.deleteAPIToken({ id: revokeTarget.id });
			toast.success('Token revoked');
			revokeOpen = false;
			await load();
		} catch {
			// Error interceptor already toasted
		} finally {
			revoking = false;
		}
	}
</script>

<div class="space-y-4">
	<div class="section-header">
		<div class="min-w-0">
			<h2 class="section-title">Deploy tokens</h2>
			<p class="section-subtitle">Tokens limited to this repository and its linked packages, owned by you.</p>
		</div>
		<Button size="sm" class="shrink-0" onclick={openCreate} disabled={!repo}>
			<Plus class="h-4 w-4 mr-1.5" />Create Token
		</Button>
	</div>

	{#if !loaded}
		<Skeleton class="h-40 w-full rounded-xl" />
	{:else if tokens.length === 0}
		<EmptyState icon={Key} message="No deploy tokens" description="External CI uses one to clone, push, and publish images or artifacts for this repository.">
			{#snippet actions()}
				<Button variant="outline" size="sm" onclick={openCreate}>
					<Plus class="h-4 w-4 mr-1.5" />Create Token
				</Button>
			{/snippet}
		</EmptyState>
	{:else}
		<div class="data-table transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
			<Table>
				<TableHeader>
					<TableRow>
						<TableHead class="th">Name</TableHead>
						<TableHead class="th">Access</TableHead>
						<TableHead class="th hidden md:table-cell">Expires</TableHead>
						<TableHead class="th hidden md:table-cell">Last used</TableHead>
						<TableHead class="th w-12"></TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{#each tokens as token (token.id)}
						<TableRow>
							<TableCell class="py-3 px-3 text-sm font-medium">{token.name}</TableCell>
							<TableCell class="py-3 px-3">
								{#if token.scope?.readOnly}
									<Badge variant="outline" class="text-xs gap-1"><Lock class="h-2.5 w-2.5" />Read only</Badge>
								{:else}
									<Badge variant="outline" class="text-xs gap-1 border-amber-500/30 text-amber-600 dark:text-amber-400"><PenLine class="h-2.5 w-2.5" />Read and write</Badge>
								{/if}
							</TableCell>
							<TableCell class="py-3 px-3 text-sm hidden md:table-cell">
								{#if token.expiresAt}
									{@const d = timestampDate(token.expiresAt)}
									<Badge variant={d < new Date() ? 'destructive' : 'outline'} class="text-xs">
										{d < new Date() ? 'Expired' : d.toLocaleDateString()}
									</Badge>
								{:else}
									<Badge variant="secondary" class="text-xs">Never</Badge>
								{/if}
							</TableCell>
							<TableCell class="py-3 px-3 text-sm text-muted-foreground hidden md:table-cell">
								{token.lastUsedAt ? relativeTime(timestampDate(token.lastUsedAt)) : 'Never'}
							</TableCell>
							<TableCell class="py-3 px-3 text-right">
								<Button
									variant="ghost" size="icon" class="h-7 w-7 text-destructive hover:text-destructive"
									aria-label="Revoke {token.name}"
									onclick={() => { revokeTarget = token; revokeOpen = true; }}
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

<FormPanel
	open={createOpen}
	onOpenChange={(v) => { if (!v) closeCreate(); }}
	title={plaintext ? 'Token Created' : 'Create Deploy Token'}
	description={plaintext ? 'Copy it now, it will not be shown again.' : `Scoped to ${fullName} and its linked packages.`}
	icon={plaintext ? CheckCircle : Key}
>
	{#if plaintext}
		<div class="space-y-5">
			<Alert variant="destructive">
				<AlertTriangle class="h-4 w-4" />
				<AlertDescription>Copy your token now. You won't be able to see it again.</AlertDescription>
			</Alert>
			<div class="flex items-center gap-2">
				<code class="code-inline flex-1 min-w-0">{plaintext}</code>
				<CopyButton text={plaintext} label="Token copied!" />
			</div>
			{#each [
				{ title: 'Clone over https', text: cloneExample },
				{ title: 'Log in with dfcli and the git credential helper', text: helperExample },
				{ title: 'Push images to linked packages', text: dockerExample }
			] as block (block.title)}
				<div class="rounded-lg border border-border/50 bg-muted/30 overflow-hidden">
					<div class="flex items-center gap-2 px-3 py-1.5 border-b border-border/40">
						<Terminal class="h-3.5 w-3.5 text-muted-foreground" />
						<span class="flex-1 text-xs font-medium text-muted-foreground">{block.title}</span>
						<CopyButton text={block.text} label="Command copied!" />
					</div>
					<pre class="px-4 py-3 font-mono text-xs leading-relaxed whitespace-pre-wrap break-all select-all">{block.text}</pre>
				</div>
			{/each}
		</div>
	{:else}
		<div class="space-y-6">
			<FormSection title="Token">
				<div class="space-y-3">
					<FormField label="Name" id="deploy-token-name" required>
						<Input id="deploy-token-name" bind:value={tokenName} placeholder="CI publish" />
					</FormField>
					<FormField label="Read only" horizontal help="Off allows pushes to the repository and its packages">
						<Switch bind:checked={readOnly} />
					</FormField>
					<FormField label="Expiration" id="deploy-token-expiry" help="No expiration means valid until revoked">
						<Select type="single" value={expiry} onValueChange={(v) => { if (v) expiry = v; }}>
							<SelectTrigger id="deploy-token-expiry" class="w-full">{expiryOptions.find((o) => o.value === expiry)?.label}</SelectTrigger>
							<SelectContent>
								{#each expiryOptions as o (o.value)}
									<SelectItem value={o.value}>{o.label}</SelectItem>
								{/each}
							</SelectContent>
						</Select>
					</FormField>
				</div>
			</FormSection>
		</div>
	{/if}
	{#snippet footer()}
		{#if plaintext}
			<Button onclick={closeCreate}>Done</Button>
		{:else}
			<Button variant="outline" onclick={closeCreate}>Cancel</Button>
			<Button onclick={create} disabled={creating || !tokenName.trim()}>{creating ? 'Creating...' : 'Create Token'}</Button>
		{/if}
	{/snippet}
</FormPanel>

<ConfirmDialog bind:open={revokeOpen} title="Revoke Token" confirmLabel="Revoke" onConfirm={revoke} loading={revoking} icon={Trash2}>
	{#snippet description()}
		Anything using <strong>{revokeTarget?.name}</strong> loses access immediately.
	{/snippet}
</ConfirmDialog>

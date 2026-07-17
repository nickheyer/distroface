<script lang="ts">
	import { onMount } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Input } from '$lib/components/ui/input';
	import {
		Table, TableBody, TableCell, TableHead, TableHeader, TableRow
	} from '$lib/components/ui/table';
	import {
		Select, SelectContent, SelectItem, SelectTrigger
	} from '$lib/components/ui/select';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import FormPanel from '$lib/components/form-panel.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import CopyButton from '$lib/components/copy-button.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import { Key, Plus, AlertTriangle, Trash2, CheckCircle, Terminal, ShieldCheck } from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { configStore } from '$lib/stores/config.svelte';
	import { portalStore } from '$lib/stores/portal.svelte';
	import { authStore } from '$lib/stores/auth.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import { toast } from 'svelte-sonner';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime, pageToToken } from '$lib/utils';
	import type { APIToken } from '$lib/proto/distroface/v1/types_pb';

	let tokens = $state<APIToken[]>([]);
	let loading = $state(true);
	let totalCount = $state(0);
	let currentPage = $state(1);
	const pageSize = 20;

	let createPanelOpen = $state(false);
	let tokenName = $state('');
	let tokenExpiryOption = $state('never');
	let creating = $state(false);
	let newPlaintextToken = $state<string | null>(null);

	let revokeDialogOpen = $state(false);
	let revokeTarget = $state<APIToken | null>(null);
	let revoking = $state(false);

	const expiryOptions = [
		{ value: '7', label: '7 days' },
		{ value: '30', label: '30 days' },
		{ value: '90', label: '90 days' },
		{ value: '365', label: '1 year' },
		{ value: 'never', label: 'No expiration' }
	];

	const registryHost = $derived(
		portalStore.host(configStore.get('server.hostname', 'localhost:8080') as string)
	);
	const dockerLoginExample = $derived(
		`docker login ${registryHost} \\\n  -u ${authStore.user?.username} \\\n  -p ${newPlaintextToken ?? 'YOUR_TOKEN'}`
	);
	const apiCurlExample = $derived(
		`curl -X POST \\\n  ${window.location.protocol}//${registryHost}/distroface.v1.RepositoryService/ListRepositories \\\n  -H "authorization: Bearer ${newPlaintextToken ?? 'YOUR_TOKEN'}" \\\n  -H "content-type: application/json" \\\n  -d '{}'`
	);

	async function loadTokens() {
		loading = true;
		try {
			const resp = await rpcClient.token.listAPITokens({
				pageSize,
				pageToken: pageToToken(currentPage, pageSize)
			});
			tokens = resp.tokens;
			totalCount = resp.totalCount;
		} catch {
			// error interceptor
		} finally {
			loading = false;
		}
	}

	async function createToken() {
		if (!tokenName.trim()) return;
		creating = true;
		try {
			const expiryDays = tokenExpiryOption !== 'never' ? Number(tokenExpiryOption) : undefined;
			const resp = await rpcClient.token.createAPIToken({
				name: tokenName.trim(),
				expiresInDays: expiryDays
			});
			newPlaintextToken = resp.plaintextToken;
			toast.success('Token created');
			tokenName = '';
			tokenExpiryOption = 'never';
			await loadTokens();
		} catch {
			// error interceptor
		} finally {
			creating = false;
		}
	}

	function openRevoke(token: APIToken) {
		revokeTarget = token;
		revokeDialogOpen = true;
	}

	async function confirmRevoke() {
		if (!revokeTarget) return;
		revoking = true;
		try {
			await rpcClient.token.deleteAPIToken({ id: revokeTarget.id });
			toast.success('Token revoked');
			revokeDialogOpen = false;
			await loadTokens();
		} catch {
			// error interceptor
		} finally {
			revoking = false;
		}
	}

	function closeCreatePanel() {
		createPanelOpen = false;
		newPlaintextToken = null;
		tokenName = '';
		tokenExpiryOption = 'never';
	}

	onMount(loadTokens);
</script>

<div class="space-y-6">
	<div class="section-header">
		<div>
			<h2 class="section-title">API Tokens</h2>
			<p class="section-subtitle">Personal access tokens for API and Docker registry authentication.</p>
		</div>
		<PermissionGate resource="tokens" action="create">
			<Button size="sm" onclick={() => (createPanelOpen = true)}>
				<Plus class="h-4 w-4 mr-1.5" />
				Create Token
			</Button>
		</PermissionGate>
	</div>

	{#if loading}
		<div class="space-y-2">
			{#each Array(3)}
				<Skeleton class="h-14 w-full rounded-xl" />
			{/each}
		</div>
	{:else if tokens.length === 0}
		<EmptyState
			icon={Key}
			message="No API tokens"
			description="Create a token to authenticate with the API and Docker registry."
		>
			{#snippet actions()}
				<PermissionGate resource="tokens" action="create">
					<Button variant="outline" size="sm" onclick={() => (createPanelOpen = true)}>
						<Plus class="h-4 w-4 mr-1.5" />
						Create Token
					</Button>
				</PermissionGate>
			{/snippet}
		</EmptyState>
	{:else}
		<div class="data-table">
			<Table>
				<TableHeader>
					<TableRow class="bg-muted/30 hover:bg-muted/30">
						<TableHead class="th">Name</TableHead>
						<TableHead class="th">Created</TableHead>
						<TableHead class="th">Expires</TableHead>
						<TableHead class="th">Last Used</TableHead>
						<TableHead class="th w-16"></TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{#each tokens as token (token.id)}
						<TableRow>
							<TableCell class="font-medium py-3 px-3">
								<div class="flex items-center gap-2">
									{token.name}
									{#if token.createdBy && token.createdBy !== authStore.user?.id}
										<span class="flex items-center gap-1 text-xs text-muted-foreground/60">
											<ShieldCheck class="h-3 w-3" />Managed
										</span>
									{/if}
								</div>
							</TableCell>
							<TableCell class="text-muted-foreground text-sm py-3 px-3">
								{token.createdAt ? relativeTime(timestampDate(token.createdAt)) : '-'}
							</TableCell>
							<TableCell class="text-sm py-3 px-3">
								{#if token.expiresAt}
									{@const expires = timestampDate(token.expiresAt)}
									{@const isExpired = expires < new Date()}
									<Badge variant={isExpired ? 'destructive' : 'outline'} class="text-xs">
										{isExpired ? 'Expired' : relativeTime(expires).replace(' ago', '')}
									</Badge>
								{:else}
									<Badge variant="secondary" class="text-xs">Never</Badge>
								{/if}
							</TableCell>
							<TableCell class="text-muted-foreground text-sm py-3 px-3">
								{token.lastUsedAt ? relativeTime(timestampDate(token.lastUsedAt)) : 'Never'}
							</TableCell>
							<PermissionGate resource="tokens" action="delete">
								<TableCell class="text-right py-3 px-3">
									<Button
										variant="ghost" size="icon"
										class="h-7 w-7 text-destructive hover:text-destructive"
										onclick={() => openRevoke(token)}
									>
										<Trash2 class="h-3.5 w-3.5" />
									</Button>
								</TableCell>
							</PermissionGate>
						</TableRow>
					{/each}
				</TableBody>
			</Table>
		</div>

		<DataPagination
			page={currentPage} {pageSize} totalCount={totalCount}
			onPrev={() => { currentPage--; loadTokens(); }}
			onNext={() => { currentPage++; loadTokens(); }}
		/>
	{/if}
</div>

<!-- Create Token Panel -->
<FormPanel
	open={createPanelOpen}
	onOpenChange={(v) => { if (!v) closeCreatePanel(); }}
	title={newPlaintextToken ? 'Token Created' : 'Create API Token'}
	description={newPlaintextToken
		? 'Your new token has been generated. Copy it now — it will not be shown again.'
		: 'Create a personal access token for authenticating with the API and Docker registry.'}
	icon={newPlaintextToken ? CheckCircle : Key}
>
	{#if newPlaintextToken}
		<div class="space-y-6">
			<Alert variant="destructive">
				<AlertTriangle class="h-4 w-4" />
				<AlertDescription>
					Copy your token now. You won't be able to see it again.
				</AlertDescription>
			</Alert>

			<div class="flex items-center gap-2">
				<code class="flex-1 text-sm bg-muted px-3 py-2.5 rounded-lg font-mono break-all border border-border/50 select-all">
					{newPlaintextToken}
				</code>
				<CopyButton text={newPlaintextToken} label="Token copied!" />
			</div>

			<div class="rounded-xl border border-border/50 bg-muted/20 overflow-hidden">
				<div class="flex items-center gap-2 px-4 py-2.5 border-b border-border/40 bg-muted/30">
					<Terminal class="h-3.5 w-3.5 text-muted-foreground" />
					<span class="text-xs font-medium text-muted-foreground">Docker registry login</span>
				</div>
				<div class="p-4 space-y-2">
					<pre class="code-inline block text-xs whitespace-pre-wrap wrap-break-word">{dockerLoginExample}</pre>
					<CopyButton text={dockerLoginExample} label="Command copied!" />
				</div>
			</div>

			<div class="rounded-xl border border-border/50 bg-muted/20 overflow-hidden">
				<div class="flex items-center gap-2 px-4 py-2.5 border-b border-border/40 bg-muted/30">
					<Terminal class="h-3.5 w-3.5 text-muted-foreground" />
					<span class="text-xs font-medium text-muted-foreground">API request</span>
				</div>
				<div class="p-4 space-y-2">
					<pre class="code-inline block text-xs whitespace-pre-wrap wrap-break-word">{apiCurlExample}</pre>
					<CopyButton text={apiCurlExample} label="Command copied!" />
				</div>
			</div>
		</div>
	{:else}
		<div class="space-y-6">
			<FormSection title="Token Details">
				<div class="space-y-3">
					<FormField label="Token Name" id="token-name" required help="A descriptive name to identify this token (e.g., 'CI/CD Pipeline', 'Local Dev').">
						<Input id="token-name" bind:value={tokenName} placeholder="e.g., CI/CD Pipeline" />
					</FormField>

					<FormField label="Expiration" help="Tokens without expiration remain valid until manually revoked.">
						<Select
							type="single"
							value={tokenExpiryOption}
							onValueChange={(v) => { if (v) tokenExpiryOption = v; }}
						>
							<SelectTrigger class="w-full">
								{expiryOptions.find((o) => o.value === tokenExpiryOption)?.label ?? 'Select expiry'}
							</SelectTrigger>
							<SelectContent>
								{#each expiryOptions as option (option.label)}
									<SelectItem value={option.value}>{option.label}</SelectItem>
								{/each}
							</SelectContent>
						</Select>
					</FormField>
				</div>
			</FormSection>
		</div>
	{/if}

	{#snippet footer()}
		{#if newPlaintextToken}
			<Button onclick={closeCreatePanel}>Done</Button>
		{:else}
			<Button variant="outline" onclick={closeCreatePanel}>Cancel</Button>
			<Button onclick={createToken} disabled={creating || !tokenName.trim()}>
				{creating ? 'Creating...' : 'Create Token'}
			</Button>
		{/if}
	{/snippet}
</FormPanel>

<ConfirmDialog bind:open={revokeDialogOpen} title="Revoke Token" confirmLabel="Revoke" onConfirm={confirmRevoke} loading={revoking}>
	{#snippet description()}
		Are you sure you want to revoke <strong>{revokeTarget?.name}</strong>? Any applications
		using this token will lose access immediately.
	{/snippet}
</ConfirmDialog>

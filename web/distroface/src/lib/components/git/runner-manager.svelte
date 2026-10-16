<script lang="ts">
	import { onMount } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Input } from '$lib/components/ui/input';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import {
		Table, TableBody, TableCell, TableHead, TableHeader, TableRow
	} from '$lib/components/ui/table';
	import FormPanel from '$lib/components/form-panel.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import CopyButton from '$lib/components/copy-button.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import { Cpu, Plus, Pencil, Trash2, KeyRound, AlertTriangle, Terminal, CheckCircle } from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { toast } from 'svelte-sonner';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime } from '$lib/utils';
	import { Pager } from '$lib/pager.svelte';
	import type { Runner } from '$lib/proto/distroface/v1/actions_pb';

	let {
		scope,
		cliScope,
		canManage = true,
		description = 'Executors that pick up workflow jobs for this scope.'
	}: {
		scope: { kind: string; id: string };
		cliScope: string;
		canManage?: boolean;
		description?: string;
	} = $props();

	let runners = $state<Runner[]>([]);
	const pager = new Pager(20);
	let loading = $state(true);
	let loaded = $state(false);

	let tokenOpen = $state(false);
	let token = $state<string | null>(null);
	let tokenExpires = $state<Date | null>(null);
	let creatingToken = $state(false);

	let editOpen = $state(false);
	let editTarget = $state<Runner | null>(null);
	let editName = $state('');
	let editLabels = $state('');
	let savingEdit = $state(false);

	let deleteOpen = $state(false);
	let deleteTarget = $state<Runner | null>(null);
	let deleting = $state(false);

	const registerLine = $derived(
		`dfcli runner register --scope ${cliScope} --token ${token ?? 'YOUR_TOKEN'}`
	);

	async function loadRunners() {
		loading = true;
		try {
			const resp = await rpcClient.actions.listRunners({ page: pager.request(), scope });
			runners = resp.runners;
			pager.apply(resp.page);
		} catch {
			runners = [];
		} finally {
			loading = false;
			loaded = true;
		}
	}

	async function newToken() {
		creatingToken = true;
		try {
			const resp = await rpcClient.actions.createRunnerToken({ scope });
			token = resp.token;
			tokenExpires = resp.expiresAt ? timestampDate(resp.expiresAt) : null;
			tokenOpen = true;
		} catch {
			// error interceptor
		} finally {
			creatingToken = false;
		}
	}

	function closeToken() {
		tokenOpen = false;
		token = null;
		tokenExpires = null;
		loadRunners();
	}

	function openEdit(runner: Runner) {
		editTarget = runner;
		editName = runner.name;
		editLabels = runner.labels.join(', ');
		editOpen = true;
	}

	async function saveEdit() {
		if (!editTarget) return;
		savingEdit = true;
		try {
			const labels = editLabels.split(',').map((l) => l.trim()).filter(Boolean);
			await rpcClient.actions.updateRunner({
				scope,
				id: editTarget.id,
				runnerName: editName.trim() || undefined,
				setLabels: true,
				labels
			});
			toast.success('Runner updated');
			editOpen = false;
			await loadRunners();
		} catch {
			// error interceptor
		} finally {
			savingEdit = false;
		}
	}

	async function confirmDelete() {
		if (!deleteTarget) return;
		deleting = true;
		try {
			await rpcClient.actions.deleteRunner({ scope, id: deleteTarget.id });
			toast.success('Runner removed');
			deleteOpen = false;
			await loadRunners();
		} catch {
			// error interceptor
		} finally {
			deleting = false;
		}
	}

	function statusVariant(status: string): 'default' | 'secondary' | 'outline' {
		if (status === 'online') return 'default';
		if (status === 'busy') return 'secondary';
		return 'outline';
	}

	onMount(loadRunners);
</script>

<div class="space-y-4">
	<div class="section-header">
		<div class="min-w-0">
			<h2 class="section-title">Runners</h2>
			<p class="section-subtitle">{description}</p>
		</div>
		{#if canManage}
			<Button size="sm" class="shrink-0" onclick={newToken} disabled={creatingToken}>
				<Plus class="h-4 w-4 mr-1.5" />
				{creatingToken ? 'Creating...' : 'New Registration Token'}
			</Button>
		{/if}
	</div>

	{#if !loaded}
		<div class="space-y-2">
			{#each { length: 3 }, i (i)}
				<Skeleton class="h-14 w-full rounded-lg" />
			{/each}
		</div>
	{:else if runners.length === 0}
		<EmptyState
			icon={Cpu}
			message="No runners"
			description="Create a registration token and run dfcli runner register on the host."
		>
			{#snippet actions()}
				{#if canManage}
					<Button variant="outline" size="sm" onclick={newToken} disabled={creatingToken}>
						<KeyRound class="h-4 w-4 mr-1.5" />
						New Registration Token
					</Button>
				{/if}
			{/snippet}
		</EmptyState>
	{:else}
		<div class="data-table transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
			<Table>
				<TableHeader>
					<TableRow class="bg-muted/30 hover:bg-muted/30">
						<TableHead class="th">Runner</TableHead>
						<TableHead class="th w-24">Status</TableHead>
						<TableHead class="th hidden sm:table-cell">Labels</TableHead>
						<TableHead class="th w-28 hidden md:table-cell">Last seen</TableHead>
						{#if canManage}
							<TableHead class="th w-20"></TableHead>
						{/if}
					</TableRow>
				</TableHeader>
				<TableBody>
					{#each runners as runner (runner.id)}
						<TableRow>
							<TableCell class="py-3 px-3">
								<p class="font-medium">{runner.name}</p>
								<p class="text-[12px] text-muted-foreground mt-0.5 font-mono">
									{runner.version || 'unknown version'}
								</p>
							</TableCell>
							<TableCell class="py-3 px-3">
								<Badge variant={statusVariant(runner.status)} class="text-[11px] capitalize">
									{runner.status || 'offline'}
								</Badge>
							</TableCell>
							<TableCell class="py-3 px-3 hidden sm:table-cell">
								<div class="flex flex-wrap gap-1">
									{#each runner.labels as label (label)}
										<Badge variant="outline" class="text-[11px] font-mono">{label}</Badge>
									{:else}
										<span class="text-[13px] text-muted-foreground">-</span>
									{/each}
								</div>
							</TableCell>
							<TableCell class="text-sm text-muted-foreground py-3 px-3 hidden md:table-cell">
								{runner.lastSeen ? relativeTime(timestampDate(runner.lastSeen)) : 'Never'}
							</TableCell>
							{#if canManage}
								<TableCell class="text-right py-3 px-3">
									<div class="flex justify-end gap-1">
										<Button variant="ghost" size="icon" class="h-7 w-7" onclick={() => openEdit(runner)} aria-label="Edit">
											<Pencil class="h-3.5 w-3.5" />
										</Button>
										<Button
											variant="ghost" size="icon"
											class="h-7 w-7 text-destructive hover:text-destructive"
											onclick={() => { deleteTarget = runner; deleteOpen = true; }}
											aria-label="Delete"
										>
											<Trash2 class="h-3.5 w-3.5" />
										</Button>
									</div>
								</TableCell>
							{/if}
						</TableRow>
					{/each}
				</TableBody>
			</Table>
			<DataPagination attached {pager} onChange={loadRunners} />
		</div>
	{/if}
</div>

<FormPanel
	open={tokenOpen}
	onOpenChange={(v) => { if (!v) closeToken(); }}
	title="Registration Token"
	description="Copy it now, it will not be shown again."
	icon={CheckCircle}
>
	{#if token}
		<div class="space-y-6">
			<Alert variant="destructive">
				<AlertTriangle class="h-4 w-4" />
				<AlertDescription>
					One time token{tokenExpires ? `, expires ${tokenExpires.toLocaleString()}` : ''}. Register the runner before it expires.
				</AlertDescription>
			</Alert>

			<div class="flex items-center gap-2">
				<code class="code-inline flex-1 min-w-0">{token}</code>
				<CopyButton text={token} label="Token copied!" />
			</div>

			<div class="rounded-xl border border-border/50 bg-muted/20 overflow-hidden">
				<div class="flex items-center gap-2 px-4 py-2.5 border-b border-border/40 bg-muted/30">
					<Terminal class="h-3.5 w-3.5 text-muted-foreground" />
					<span class="text-xs font-medium text-muted-foreground">Register on the runner host</span>
				</div>
				<div class="p-4 space-y-2">
					<pre class="code-inline block text-xs whitespace-pre-wrap break-all">{registerLine}</pre>
					<CopyButton text={registerLine} label="Command copied!" />
				</div>
			</div>
		</div>
	{/if}
	{#snippet footer()}
		<Button onclick={closeToken}>Done</Button>
	{/snippet}
</FormPanel>

<FormPanel bind:open={editOpen} title="Edit Runner" description="Labels decide which jobs the runner picks up." icon={Cpu}>
	<FormSection title="Runner">
		<div class="space-y-3">
			<FormField label="Name" id="runner-name">
				<Input id="runner-name" bind:value={editName} />
			</FormField>
			<FormField label="Labels" id="runner-labels" help="Comma separated, matched against runs-on">
				<Input id="runner-labels" bind:value={editLabels} placeholder="ubuntu-latest, docker" class="font-mono" />
			</FormField>
		</div>
	</FormSection>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (editOpen = false)}>Cancel</Button>
		<Button onclick={saveEdit} disabled={savingEdit}>
			{savingEdit ? 'Saving...' : 'Save Changes'}
		</Button>
	{/snippet}
</FormPanel>

<ConfirmDialog bind:open={deleteOpen} title="Remove Runner" confirmLabel="Remove" onConfirm={confirmDelete} loading={deleting} icon={Trash2}>
	{#snippet description()}
		Remove <strong>{deleteTarget?.name}</strong>? Its token stops working and running jobs fail.
	{/snippet}
</ConfirmDialog>

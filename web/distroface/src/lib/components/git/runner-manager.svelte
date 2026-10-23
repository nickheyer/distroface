<script lang="ts">
	import { onMount } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge, type BadgeVariant } from '$lib/components/ui/badge';
	import { Input } from '$lib/components/ui/input';
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
	import FormField from '$lib/components/form-field.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import CopyButton from '$lib/components/copy-button.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import {
		Cpu,
		Plus,
		Pencil,
		Trash2,
		KeyRound,
		AlertTriangle,
		Terminal,
		CheckCircle,
		Loader2
	} from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { configStore } from '$lib/stores/config.svelte';
	import { portalStore } from '$lib/stores/portal.svelte';
	import { toast } from 'svelte-sonner';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime } from '$lib/utils';
	import { Pager } from '$lib/pager.svelte';
	import { runnerRegisterCommand, scopeLabel } from '$lib/components/git/actions-state';
	import type { Runner } from '$lib/proto/distroface/v1/actions_pb';

	let {
		scope,
		canManage = true,
		includeInherited = false,
		description = 'Executors that pick up workflow jobs for this scope.'
	}: {
		scope: { kind: string; id: string };
		canManage?: boolean;
		includeInherited?: boolean;
		description?: string;
	} = $props();

	let runners = $state<Runner[]>([]);
	const pager = new Pager(20);
	let loading = $state(true);
	let loaded = $state(false);

	let token = $state<string | null>(null);
	let tokenExpires = $state<Date | null>(null);
	let creatingToken = $state(false);

	let editTarget = $state<Runner | null>(null);
	let editName = $state('');
	let editLabels = $state('');
	let savingEdit = $state(false);

	let deleteOpen = $state(false);
	let deleteTarget = $state<Runner | null>(null);
	let deleting = $state(false);

	const serverUrl = $derived(
		`${portalStore.scheme()}://${portalStore.host(configStore.publicHostname)}`
	);
	const registerLine = $derived(runnerRegisterCommand(serverUrl, token));
	const columns = $derived(4 + (includeInherited ? 1 : 0) + (canManage ? 1 : 0));

	// Inherited rows are managed where they were registered
	function own(runner: Runner): boolean {
		return !includeInherited || (runner.scope?.kind ?? scope.kind) === scope.kind;
	}

	async function loadRunners() {
		loading = true;
		try {
			const resp = await rpcClient.actions.listRunners({
				page: pager.request(),
				scope,
				includeInherited
			});
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
		} catch {
			// error interceptor
		} finally {
			creatingToken = false;
		}
	}

	function closeToken() {
		token = null;
		tokenExpires = null;
		loadRunners();
	}

	function openEdit(runner: Runner) {
		editTarget = runner;
		editName = runner.name;
		editLabels = runner.labels.join(', ');
	}

	function closeEdit() {
		editTarget = null;
	}

	async function saveEdit() {
		if (!editTarget || savingEdit) return;
		savingEdit = true;
		const labels = editLabels
			.split(',')
			.map((l) => l.trim())
			.filter(Boolean);
		const ok = await rpcClient.actions
			.updateRunner({
				scope,
				id: editTarget.id,
				runnerName: editName.trim() || undefined,
				setLabels: true,
				labels
			})
			.then(
				() => true,
				() => false
			);
		savingEdit = false;
		if (!ok) return;
		toast.success('Runner updated');
		closeEdit();
		await loadRunners();
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

	function statusVariant(status: string): BadgeVariant {
		if (status === 'online') return 'success';
		if (status === 'busy') return 'default';
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
			<Button class="shrink-0" onclick={newToken} disabled={creatingToken}>
				{#if creatingToken}
					<Loader2 class="animate-spin" />
				{:else}
					<Plus />
				{/if}
				{creatingToken ? 'Creating' : 'New registration token'}
			</Button>
		{/if}
	</div>

	{#if token}
		<div class="box border-primary/40">
			<div class="box-header">
				<span class="flex items-center gap-2">
					<CheckCircle class="size-4 text-success" />
					Registration token
				</span>
				<Button variant="ghost-muted" size="sm" onclick={closeToken}>Done</Button>
			</div>
			<div class="space-y-4 p-4">
				<Alert variant="destructive">
					<AlertTriangle />
					<AlertDescription>
						One time token{tokenExpires ? `, expires ${tokenExpires.toLocaleString()}` : ''}. Copy
						it now, it is not shown again.
					</AlertDescription>
				</Alert>
				<div class="flex items-center gap-2">
					<code class="code-inline min-w-0 flex-1">{token}</code>
					<CopyButton text={token} label="Token copied" />
				</div>
				<div class="space-y-2">
					<p class="eyebrow flex items-center gap-1.5">
						<Terminal class="size-3.5" />
						Register on the runner host
					</p>
					<div class="flex items-start gap-2">
						<pre
							class="code-inline min-w-0 flex-1 break-all whitespace-pre-wrap">{registerLine}</pre>
						<CopyButton text={registerLine} label="Command copied" />
					</div>
					<p class="text-xs text-muted-foreground">
						Then run <code class="font-mono">dfcli runner start</code>. The token already knows its
						scope. dfcli binaries are on the
						<a
							class="link"
							href="https://github.com/nickheyer/distroface/releases"
							target="_blank"
							rel="noreferrer">releases page</a
						>.
					</p>
				</div>
			</div>
		</div>
	{/if}

	{#if !loaded}
		<div class="box divide-y">
			{#each { length: 3 }, i (i)}
				<div class="flex items-center gap-3 px-4 py-3">
					<div class="flex-1 space-y-2">
						<Skeleton class="h-4 w-40" />
						<Skeleton class="h-3 w-24" />
					</div>
					<Skeleton class="h-5 w-16 rounded-full" />
					<Skeleton class="hidden h-3 w-20 md:block" />
				</div>
			{/each}
		</div>
	{:else if runners.length === 0}
		<EmptyState
			icon={Cpu}
			message="No runners"
			description={includeInherited
				? 'Nothing can pick up jobs for this repository yet. Register a runner here, or at the organization or instance level.'
				: 'Create a registration token and run dfcli runner register on the host.'}
		>
			{#snippet actions()}
				{#if canManage}
					<Button variant="outline" onclick={newToken} disabled={creatingToken}>
						<KeyRound />
						New registration token
					</Button>
				{/if}
			{/snippet}
		</EmptyState>
	{:else}
		<div class="data-table transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
			<Table>
				<TableHeader>
					<TableRow>
						<TableHead>Runner</TableHead>
						<TableHead class="w-24">Status</TableHead>
						{#if includeInherited}
							<TableHead class="w-32">Scope</TableHead>
						{/if}
						<TableHead class="hidden sm:table-cell">Labels</TableHead>
						<TableHead class="hidden w-28 md:table-cell">Last seen</TableHead>
						{#if canManage}
							<TableHead class="w-20"></TableHead>
						{/if}
					</TableRow>
				</TableHeader>
				<TableBody>
					{#each runners as runner (runner.id)}
						{#if editTarget?.id === runner.id}
							<TableRow class="bg-muted/30 hover:[&>td]:bg-transparent">
								<TableCell colspan={columns} class="px-4 py-4 whitespace-normal">
									<div class="space-y-4">
										<div class="grid gap-4 sm:grid-cols-2">
											<FormField label="Name" id="runner-name">
												<Input id="runner-name" bind:value={editName} autocomplete="off" />
											</FormField>
											<FormField
												label="Labels"
												id="runner-labels"
												help="Comma separated, matched against runs-on"
											>
												<Input
													id="runner-labels"
													bind:value={editLabels}
													placeholder="ubuntu-latest, docker"
													class="font-mono"
													autocomplete="off"
												/>
											</FormField>
										</div>
										<div class="flex items-center justify-end gap-2 border-t pt-4">
											<Button variant="outline" onclick={closeEdit}>Cancel</Button>
											<Button onclick={saveEdit} disabled={savingEdit}>
												{#if savingEdit}<Loader2 class="animate-spin" />{/if}
												{savingEdit ? 'Saving' : 'Save changes'}
											</Button>
										</div>
									</div>
								</TableCell>
							</TableRow>
						{:else}
							<TableRow>
								<TableCell>
									<p class="flex items-center gap-2 font-medium">
										{runner.name}
										{#if runner.builtin}<Badge
												variant="secondary"
												title="Runs inside the server process">Built in</Badge
											>{/if}
									</p>
									<p class="mt-0.5 font-mono text-xs text-muted-foreground">
										{runner.version || 'unknown version'}
									</p>
								</TableCell>
								<TableCell>
									<Badge variant={statusVariant(runner.status)} class="capitalize">
										{runner.status || 'offline'}
									</Badge>
								</TableCell>
								{#if includeInherited}
									<TableCell>
										<Badge variant={own(runner) ? 'secondary' : 'outline'}>
											{scopeLabel(runner.scope?.kind)}
										</Badge>
									</TableCell>
								{/if}
								<TableCell class="hidden sm:table-cell">
									<div class="flex flex-wrap gap-1">
										{#each runner.labels as label (label)}
											<Badge variant="outline" class="font-mono">{label}</Badge>
										{:else}
											<span class="text-sm text-muted-foreground">-</span>
										{/each}
									</div>
								</TableCell>
								<TableCell class="hidden text-sm text-muted-foreground md:table-cell">
									{runner.lastSeen ? relativeTime(timestampDate(runner.lastSeen)) : 'Never'}
								</TableCell>
								{#if canManage}
									<TableCell class="text-right">
										{#if runner.builtin}
											<span
												class="text-xs whitespace-nowrap text-muted-foreground"
												title="Configured under the instance actions settings">Server managed</span
											>
										{:else if own(runner)}
											<div class="flex justify-end gap-1">
												<Button
													variant="ghost-muted"
													size="icon-sm"
													onclick={() => openEdit(runner)}
													aria-label="Edit {runner.name}"
												>
													<Pencil />
												</Button>
												<Button
													variant="ghost-muted"
													size="icon-sm"
													class="hover:text-destructive"
													onclick={() => {
														deleteTarget = runner;
														deleteOpen = true;
													}}
													aria-label="Remove {runner.name}"
												>
													<Trash2 />
												</Button>
											</div>
										{:else}
											<span
												class="text-xs whitespace-nowrap text-muted-foreground"
												title="Managed in the {scopeLabel(
													runner.scope?.kind
												).toLowerCase()} settings">Inherited</span
											>
										{/if}
									</TableCell>
								{/if}
							</TableRow>
						{/if}
					{/each}
				</TableBody>
			</Table>
			<DataPagination attached {pager} onChange={loadRunners} />
		</div>
	{/if}
</div>

<ConfirmDialog
	bind:open={deleteOpen}
	title="Remove runner"
	confirmLabel="Remove"
	onConfirm={confirmDelete}
	loading={deleting}
	icon={Trash2}
>
	{#snippet description()}
		Remove <strong>{deleteTarget?.name}</strong>? Its token stops working and running jobs fail.
	{/snippet}
</ConfirmDialog>

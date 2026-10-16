<script lang="ts">
	import { page } from '$app/state';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { portalStore } from '$lib/stores/portal.svelte';
	import { toast } from 'svelte-sonner';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime } from '$lib/utils';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '$lib/components/ui/table';
	import FormCard from '$lib/components/form-card.svelte';
	import FormPanel from '$lib/components/form-panel.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import CopyButton from '$lib/components/copy-button.svelte';
	import { KeyRound, Variable as VariableIcon, Cpu, Plus, Trash2, Info, Terminal, AlertTriangle } from '@lucide/svelte';
	import type { Secret, Variable, Runner } from '$lib/proto/distroface/v1/actions_pb';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);
	const scope = $derived({ kind: 'repo', id: `${namespace}/${name}` });

	let secrets = $state<Secret[]>([]);
	let variables = $state<Variable[]>([]);
	let runners = $state<Runner[]>([]);
	let loaded = $state(false);
	let loadedFor = $state('');

	type Kind = 'secret' | 'variable';
	let editOpen = $state(false);
	let editKind = $state<Kind>('secret');
	let editName = $state('');
	let editValue = $state('');
	let editExisting = $state(false);
	let saving = $state(false);

	let deleteOpen = $state(false);
	let deleteKind = $state<Kind | 'runner'>('secret');
	let deleteTarget = $state('');
	let deleteLabel = $state('');
	let deleting = $state(false);

	let runnerToken = $state<string | null>(null);
	let runnerTokenExpires = $state<Date | null>(null);
	let tokenOpen = $state(false);
	let issuing = $state(false);

	const nameValid = $derived(/^[A-Za-z_][A-Za-z0-9_]*$/.test(editName.trim()));
	const serverUrl = $derived(`${portalStore.scheme()}://${portalStore.host(configStore.publicHostname)}`);
	const registerExample = $derived(
		`dfcli runner register --server ${serverUrl} \\\n  --token ${runnerToken ?? 'REGISTRATION_TOKEN'} \\\n  --name my-runner --labels linux,docker`
	);

	$effect(() => {
		if (repo && repo.fullName !== loadedFor) {
			loadedFor = repo.fullName;
			void load();
		}
	});

	async function load() {
		try {
			const [s, v, r] = await Promise.all([
				rpcClient.actions.listSecrets({ scope, page: { pageSize: 100 } }, silentCallOptions),
				rpcClient.actions.listVariables({ scope, page: { pageSize: 100 } }, silentCallOptions),
				rpcClient.actions.listRunners({ scope, page: { pageSize: 100 } }, silentCallOptions)
			]);
			secrets = s.secrets;
			variables = v.variables;
			runners = r.runners;
		} catch {
			// Error interceptor stays quiet, lists stay empty
		} finally {
			loaded = true;
		}
	}

	function openEdit(kind: Kind, existing?: { name: string; value?: string }) {
		editKind = kind;
		editName = existing?.name ?? '';
		editValue = existing?.value ?? '';
		editExisting = !!existing;
		editOpen = true;
	}

	async function save() {
		if (!nameValid) return;
		saving = true;
		try {
			if (editKind === 'secret') {
				await rpcClient.actions.setSecret({ scope, secretName: editName.trim(), value: editValue });
			} else {
				await rpcClient.actions.setVariable({ scope, variableName: editName.trim(), value: editValue });
			}
			toast.success(`${editKind === 'secret' ? 'Secret' : 'Variable'} saved`);
			editOpen = false;
			await load();
		} catch {
			// Error interceptor already toasted
		} finally {
			saving = false;
		}
	}

	function openDelete(kind: Kind | 'runner', id: string, label: string) {
		deleteKind = kind;
		deleteTarget = id;
		deleteLabel = label;
		deleteOpen = true;
	}

	async function remove() {
		deleting = true;
		try {
			if (deleteKind === 'secret') await rpcClient.actions.deleteSecret({ scope, secretName: deleteTarget });
			else if (deleteKind === 'variable') await rpcClient.actions.deleteVariable({ scope, variableName: deleteTarget });
			else await rpcClient.actions.deleteRunner({ scope, id: deleteTarget });
			toast.success('Deleted');
			deleteOpen = false;
			await load();
		} catch {
			// Error interceptor already toasted
		} finally {
			deleting = false;
		}
	}

	async function issueRunnerToken() {
		issuing = true;
		try {
			const resp = await rpcClient.actions.createRunnerToken({ scope });
			runnerToken = resp.token;
			runnerTokenExpires = resp.expiresAt ? timestampDate(resp.expiresAt) : null;
			tokenOpen = true;
		} catch {
			// Error interceptor already toasted
		} finally {
			issuing = false;
		}
	}

	const statusClass = (s: string) =>
		s === 'online' ? 'status-dot-active' : s === 'busy' ? 'bg-amber-500' : 'status-dot-inactive';
</script>

{#if !repo || !loaded}
	<Skeleton class="h-72 w-full rounded-xl" />
{:else}
	<div class="space-y-4">
		<h2 class="section-title">Actions</h2>

		{#if !configStore.actionsEnabled}
			<Alert>
				<Info class="h-4 w-4" />
				<AlertDescription>Workflows are turned off for this instance. Values saved here apply once an administrator enables Actions.</AlertDescription>
			</Alert>
		{:else if !repo.units?.actions}
			<Alert>
				<Info class="h-4 w-4" />
				<AlertDescription>The Actions unit is disabled for this repository. Turn it on under General, Features.</AlertDescription>
			</Alert>
		{/if}

		<FormCard title="Secrets" description="Encrypted values exposed to workflows as secrets.NAME, never shown again." icon={KeyRound}>
			{#if secrets.length === 0}
				<p class="text-sm text-muted-foreground">No repository secrets.</p>
			{:else}
				<div class="data-table">
					<Table>
						<TableHeader>
							<TableRow>
								<TableHead class="th">Name</TableHead>
								<TableHead class="th hidden sm:table-cell">Updated</TableHead>
								<TableHead class="th w-24"></TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{#each secrets as s (s.name)}
								<TableRow>
									<TableCell class="py-2.5 px-3 font-mono text-sm">{s.name}</TableCell>
									<TableCell class="py-2.5 px-3 text-sm text-muted-foreground hidden sm:table-cell">{s.updatedAt ? relativeTime(timestampDate(s.updatedAt)) : '-'}</TableCell>
									<TableCell class="py-2.5 px-3 text-right">
										<Button variant="ghost" size="sm" class="h-7 text-xs" onclick={() => openEdit('secret', { name: s.name })}>Replace</Button>
										<Button variant="ghost" size="icon" class="h-7 w-7 text-destructive hover:text-destructive" aria-label="Delete {s.name}" onclick={() => openDelete('secret', s.name, s.name)}>
											<Trash2 class="h-3.5 w-3.5" />
										</Button>
									</TableCell>
								</TableRow>
							{/each}
						</TableBody>
					</Table>
				</div>
			{/if}
			{#snippet footer()}
				<Button size="sm" onclick={() => openEdit('secret')}><Plus class="h-4 w-4 mr-1.5" />New Secret</Button>
			{/snippet}
		</FormCard>

		<FormCard title="Variables" description="Plain values exposed as vars.NAME." icon={VariableIcon}>
			{#if variables.length === 0}
				<p class="text-sm text-muted-foreground">No repository variables.</p>
			{:else}
				<div class="data-table">
					<Table>
						<TableHeader>
							<TableRow>
								<TableHead class="th">Name</TableHead>
								<TableHead class="th">Value</TableHead>
								<TableHead class="th hidden sm:table-cell">Updated</TableHead>
								<TableHead class="th w-24"></TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{#each variables as v (v.name)}
								<TableRow>
									<TableCell class="py-2.5 px-3 font-mono text-sm">{v.name}</TableCell>
									<TableCell class="py-2.5 px-3 font-mono text-xs text-muted-foreground max-w-xs truncate">{v.value}</TableCell>
									<TableCell class="py-2.5 px-3 text-sm text-muted-foreground hidden sm:table-cell">{v.updatedAt ? relativeTime(timestampDate(v.updatedAt)) : '-'}</TableCell>
									<TableCell class="py-2.5 px-3 text-right">
										<Button variant="ghost" size="sm" class="h-7 text-xs" onclick={() => openEdit('variable', { name: v.name, value: v.value })}>Edit</Button>
										<Button variant="ghost" size="icon" class="h-7 w-7 text-destructive hover:text-destructive" aria-label="Delete {v.name}" onclick={() => openDelete('variable', v.name, v.name)}>
											<Trash2 class="h-3.5 w-3.5" />
										</Button>
									</TableCell>
								</TableRow>
							{/each}
						</TableBody>
					</Table>
				</div>
			{/if}
			{#snippet footer()}
				<Button size="sm" onclick={() => openEdit('variable')}><Plus class="h-4 w-4 mr-1.5" />New Variable</Button>
			{/snippet}
		</FormCard>

		<FormCard title="Runners" description="Executors registered to this repository, org and instance runners also pick up jobs." icon={Cpu}>
			{#if runners.length === 0}
				<p class="text-sm text-muted-foreground">No repository runners.</p>
			{:else}
				<div class="data-table">
					<Table>
						<TableHeader>
							<TableRow>
								<TableHead class="th">Runner</TableHead>
								<TableHead class="th">Labels</TableHead>
								<TableHead class="th hidden sm:table-cell">Last seen</TableHead>
								<TableHead class="th w-12"></TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{#each runners as r (r.id)}
								<TableRow>
									<TableCell class="py-2.5 px-3">
										<div class="flex items-center gap-2">
											<span class="status-dot {statusClass(r.status)}" title={r.status}></span>
											<div class="min-w-0">
												<p class="text-sm font-medium truncate">{r.name}</p>
												<p class="text-xs text-muted-foreground">{r.status}{r.version ? `, ${r.version}` : ''}</p>
											</div>
										</div>
									</TableCell>
									<TableCell class="py-2.5 px-3">
										<div class="flex flex-wrap gap-1">
											{#each r.labels as l (l)}
												<Badge variant="secondary" class="text-[10px] font-normal">{l}</Badge>
											{/each}
										</div>
									</TableCell>
									<TableCell class="py-2.5 px-3 text-sm text-muted-foreground hidden sm:table-cell">{r.lastSeen ? relativeTime(timestampDate(r.lastSeen)) : 'Never'}</TableCell>
									<TableCell class="py-2.5 px-3 text-right">
										<Button variant="ghost" size="icon" class="h-7 w-7 text-destructive hover:text-destructive" aria-label="Remove {r.name}" onclick={() => openDelete('runner', r.id, r.name)}>
											<Trash2 class="h-3.5 w-3.5" />
										</Button>
									</TableCell>
								</TableRow>
							{/each}
						</TableBody>
					</Table>
				</div>
			{/if}
			{#snippet footer()}
				<Button size="sm" variant="outline" onclick={issueRunnerToken} disabled={issuing}>
					<KeyRound class="h-4 w-4 mr-1.5" />{issuing ? 'Issuing...' : 'New Registration Token'}
				</Button>
			{/snippet}
		</FormCard>
	</div>
{/if}

<FormPanel
	bind:open={editOpen}
	title={editExisting ? (editKind === 'secret' ? 'Replace Secret' : 'Edit Variable') : editKind === 'secret' ? 'New Secret' : 'New Variable'}
	description={editKind === 'secret' ? 'The value is sealed on save and cannot be read back.' : 'Visible to anyone who can read workflow runs.'}
	icon={editKind === 'secret' ? KeyRound : VariableIcon}
>
	<div class="space-y-3">
		<FormField label="Name" id="act-name" required help="Letters, digits, and underscores, referenced as {editKind === 'secret' ? 'secrets' : 'vars'}.NAME">
			<Input id="act-name" bind:value={editName} placeholder="DEPLOY_TOKEN" class="font-mono text-sm uppercase" disabled={editExisting} />
		</FormField>
		<FormField label="Value" id="act-value" required>
			<Textarea id="act-value" bind:value={editValue} rows={4} class="font-mono text-xs" spellcheck={false} />
		</FormField>
	</div>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (editOpen = false)}>Cancel</Button>
		<Button onclick={save} disabled={saving || !nameValid || !editValue}>{saving ? 'Saving...' : 'Save'}</Button>
	{/snippet}
</FormPanel>

<FormPanel bind:open={tokenOpen} title="Registration Token" description="Pair a runner with this repository." icon={Cpu}>
	{#if runnerToken}
		<div class="space-y-4">
			<Alert variant="destructive">
				<AlertTriangle class="h-4 w-4" />
				<AlertDescription>
					Single use{runnerTokenExpires ? `, expires ${runnerTokenExpires.toLocaleString()}` : ''}. Copy it now.
				</AlertDescription>
			</Alert>
			<div class="flex items-center gap-2">
				<code class="code-inline flex-1 min-w-0">{runnerToken}</code>
				<CopyButton text={runnerToken} label="Token copied!" />
			</div>
			<div class="rounded-lg border border-border/50 bg-muted/30 overflow-hidden">
				<div class="flex items-center gap-2 px-3 py-1.5 border-b border-border/40">
					<Terminal class="h-3.5 w-3.5 text-muted-foreground" />
					<span class="flex-1 text-xs font-medium text-muted-foreground">Register a runner</span>
					<CopyButton text={registerExample} label="Command copied!" />
				</div>
				<pre class="px-4 py-3 font-mono text-xs leading-relaxed whitespace-pre-wrap break-all select-all">{registerExample}</pre>
			</div>
		</div>
	{/if}
	{#snippet footer()}
		<Button onclick={() => { tokenOpen = false; runnerToken = null; }}>Done</Button>
	{/snippet}
</FormPanel>

<ConfirmDialog bind:open={deleteOpen} title={deleteKind === 'secret' ? 'Delete Secret' : deleteKind === 'variable' ? 'Delete Variable' : 'Remove Runner'} confirmLabel={deleteKind === 'runner' ? 'Remove' : 'Delete'} onConfirm={remove} loading={deleting} icon={Trash2}>
	{#snippet description()}
		<strong class="font-mono">{deleteLabel}</strong> is removed{deleteKind === 'runner' ? ' and stops receiving jobs' : ' from every workflow'}.
	{/snippet}
</ConfirmDialog>

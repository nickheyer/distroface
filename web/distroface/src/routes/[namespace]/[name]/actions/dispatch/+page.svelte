<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { Play, GitBranch, Loader2 } from '@lucide/svelte';
	import { toast } from 'svelte-sonner';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Switch } from '$lib/components/ui/switch';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import EmptyState from '$lib/components/empty-state.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import { GitUnit } from '$lib/proto/distroface/v1/git_pb';
	import type { Workflow } from '$lib/proto/distroface/v1/actions_pb';

	interface DispatchInput {
		name: string;
		type: string;
		description?: string;
		default?: string;
		required?: boolean;
		options?: string[];
	}

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);
	const workflowPath = page.url.searchParams.get('workflow') ?? '';
	const canWrite = $derived(!!repo && gitRepoStore.canWrite(GitUnit.ACTIONS) && !repo.isArchived);
	const actionsHref = $derived(
		resolve(
			`${resolve('/[namespace]/[name]/actions', { namespace, name })}?workflow=${encodeURIComponent(workflowPath)}` as `/${string}`
		)
	);

	let workflow = $state<Workflow | undefined>();
	let loading = $state(true);
	let values = $state<Record<string, string>>({});
	let ref = $state(page.url.searchParams.get('ref') ?? '');
	let branches = $state<string[]>([]);
	let submitting = $state(false);

	const inputs = $derived.by((): DispatchInput[] => {
		if (!workflow?.dispatchInputs) return [];
		try {
			const v = JSON.parse(workflow.dispatchInputs);
			if (!Array.isArray(v)) return [];
			return v
				.filter((i) => i && typeof i.name === 'string')
				.map((i) => ({
					name: i.name,
					type: String(i.type || 'string'),
					description: i.description || '',
					default: i.default == null ? '' : String(i.default),
					required: !!i.required,
					options: Array.isArray(i.options) ? i.options.map(String) : []
				}));
		} catch {
			return [];
		}
	});

	const dispatchable = $derived(
		!!workflow &&
			workflow.enabled &&
			!workflow.error &&
			workflow.triggers.includes('workflow_dispatch')
	);
	const refOptions = $derived(ref && !branches.includes(ref) ? [ref, ...branches] : branches);
	const missing = $derived(
		inputs
			.filter((i) => i.required && i.type !== 'boolean' && !(values[i.name] ?? '').trim())
			.map((i) => i.name)
	);

	function seedValues() {
		const next: Record<string, string> = {};
		for (const i of inputs) {
			next[i.name] =
				i.type === 'boolean' ? (i.default === 'true' ? 'true' : 'false') : (i.default ?? '');
		}
		values = next;
	}

	onMount(async () => {
		try {
			const [flows, refs] = await Promise.all([
				rpcClient.actions.listWorkflows({ namespace, name }, silentCallOptions),
				rpcClient.gitRefs.listBranches(
					{ namespace, name, page: { pageSize: 200, pageToken: '', orderBy: 'updated desc' } },
					silentCallOptions
				)
			]);
			workflow = flows.workflows.find((w) => w.path === workflowPath);
			branches = refs.branches.map((b) => b.name);
			if (!ref) ref = repo?.defaultBranch ?? '';
			seedValues();
		} catch {
			workflow = undefined;
		} finally {
			loading = false;
		}
	});

	async function submit() {
		if (!workflow || submitting || missing.length > 0 || !ref.trim()) return;
		submitting = true;
		try {
			const payload: Record<string, string> = {};
			for (const i of inputs) payload[i.name] = values[i.name] ?? '';
			const resp = await rpcClient.actions.dispatchWorkflow({
				namespace,
				name,
				path: workflow.path,
				ref: ref.trim(),
				inputs: payload
			});
			toast.success(`Run #${resp.run?.number ?? ''} queued`);
			await goto(resolve(actionsHref as `/${string}`));
		} catch {
			// Error interceptor already toasted
		} finally {
			submitting = false;
		}
	}

	function inputId(n: string): string {
		return `dispatch-input-${n.replace(/[^a-zA-Z0-9_-]/g, '-')}`;
	}
</script>

<svelte:head>
	<title>Run workflow - {namespace}/{name}</title>
</svelte:head>

{#if !repo || loading}
	<div class="mx-auto w-full max-w-5xl space-y-4">
		<Skeleton class="h-7 w-72" />
		<Skeleton class="h-64 w-full rounded-lg" />
	</div>
{:else if !workflow}
	<EmptyState
		icon={Play}
		message="Workflow not found"
		description="{workflowPath || 'The workflow'} is not in this repository."
	>
		{#snippet actions()}
			<Button variant="outline" href={actionsHref}>Back to actions</Button>
		{/snippet}
	</EmptyState>
{:else if !canWrite || !dispatchable || !configStore.actionsEnabled}
	<EmptyState
		icon={Play}
		message="This workflow cannot be run by hand"
		description={!configStore.actionsEnabled
			? 'Workflows are turned off for this instance.'
			: !canWrite
				? 'Write access to the actions unit is required.'
				: !workflow.enabled
					? 'The workflow is disabled.'
					: workflow.error
						? workflow.error
						: 'It has no workflow_dispatch trigger.'}
	>
		{#snippet actions()}
			<Button variant="outline" href={actionsHref}>Back to actions</Button>
		{/snippet}
	</EmptyState>
{:else}
	<FormPage
		title="Run {workflow.name || workflow.path}"
		description="Queues a run of {workflow.path} with the inputs below."
		backHref={actionsHref}
		backLabel="Actions"
	>
		<FormSection title="Branch" description="Workflow file and code are read from this ref.">
			<FormField label="Branch" id="dispatch-ref" required span="half">
				{#if refOptions.length > 0}
					<Select
						type="single"
						value={ref}
						onValueChange={(v) => {
							if (v) ref = v;
						}}
					>
						<SelectTrigger class="w-full font-mono" id="dispatch-ref">
							<span class="inline-flex items-center gap-1.5 truncate"
								><GitBranch class="size-3.5 text-muted-foreground" />{ref || 'Select branch'}</span
							>
						</SelectTrigger>
						<SelectContent>
							{#each refOptions as b (b)}
								<SelectItem value={b} class="font-mono">{b}</SelectItem>
							{/each}
						</SelectContent>
					</Select>
				{:else}
					<Input id="dispatch-ref" bind:value={ref} placeholder="main" class="font-mono" />
				{/if}
			</FormField>
		</FormSection>

		<FormSection
			title="Inputs"
			description={inputs.length === 0
				? 'This workflow takes no inputs.'
				: 'Values the workflow reads from github.event.inputs.'}
		>
			{#each inputs as input (input.name)}
				{@const id = inputId(input.name)}
				{#if input.type === 'boolean'}
					<FormField label={input.name} {id} layout="toggle" help={input.description}>
						<Switch
							{id}
							checked={values[input.name] === 'true'}
							onCheckedChange={(v) => (values[input.name] = v ? 'true' : 'false')}
						/>
					</FormField>
				{:else}
					<FormField
						label={input.name}
						{id}
						required={input.required}
						help={input.description}
						span={input.type === 'number' ? 'third' : 'half'}
					>
						{#if input.type === 'choice'}
							<Select
								type="single"
								value={values[input.name] ?? ''}
								onValueChange={(v) => (values[input.name] = v ?? '')}
							>
								<SelectTrigger class="w-full" {id}>
									{values[input.name] || 'Select an option'}
								</SelectTrigger>
								<SelectContent>
									{#each input.options ?? [] as opt (opt)}
										<SelectItem value={opt}>{opt}</SelectItem>
									{/each}
								</SelectContent>
							</Select>
						{:else if input.type === 'number'}
							<Input {id} type="number" bind:value={values[input.name]} class="tabular-nums" />
						{:else if input.type === 'environment'}
							<Input {id} bind:value={values[input.name]} placeholder="Environment name" />
						{:else}
							<Input {id} bind:value={values[input.name]} />
						{/if}
					</FormField>
				{/if}
			{:else}
				<p class="text-sm text-muted-foreground">Nothing to fill in, the run starts as is.</p>
			{/each}
		</FormSection>

		{#snippet footer()}
			<Button onclick={submit} disabled={submitting || missing.length > 0 || !ref.trim()}>
				{#if submitting}
					<Loader2 class="animate-spin" />
				{:else}
					<Play />
				{/if}
				{submitting ? 'Starting' : 'Run workflow'}
			</Button>
			<Button variant="ghost-muted" href={actionsHref}>Cancel</Button>
		{/snippet}
	</FormPage>
{/if}

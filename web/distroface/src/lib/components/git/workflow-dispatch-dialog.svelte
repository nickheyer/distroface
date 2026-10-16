<script lang="ts">
	import { Play, GitBranch } from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Switch } from '$lib/components/ui/switch';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import FormPanel from '$lib/components/form-panel.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import type { Workflow, WorkflowRun } from '$lib/proto/distroface/v1/actions_pb';
	import { toast } from 'svelte-sonner';

	interface DispatchInput {
		name: string;
		type: string;
		description?: string;
		default?: string;
		required?: boolean;
		options?: string[];
	}

	let {
		open = $bindable(false),
		namespace,
		name,
		workflow,
		defaultRef = '',
		onDispatched
	}: {
		open?: boolean;
		namespace: string;
		name: string;
		workflow: Workflow | undefined;
		defaultRef?: string;
		onDispatched?: (run: WorkflowRun) => void;
	} = $props();

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

	let values = $state<Record<string, string>>({});
	let ref = $state('');
	let branches = $state<string[]>([]);
	let branchesLoading = $state(false);
	let submitting = $state(false);

	function resetForm() {
		const next: Record<string, string> = {};
		for (const i of inputs) {
			next[i.name] = i.type === 'boolean' ? (i.default === 'true' ? 'true' : 'false') : (i.default ?? '');
		}
		values = next;
		ref = defaultRef;
	}

	async function loadBranches() {
		branchesLoading = true;
		try {
			const resp = await rpcClient.gitRefs.listBranches(
				{ namespace, name, page: { pageSize: 200, pageToken: '', orderBy: 'updated desc' } },
				silentCallOptions
			);
			branches = resp.branches.map((b) => b.name);
		} catch {
			branches = [];
		} finally {
			branchesLoading = false;
		}
	}

	$effect(() => {
		if (!open) return;
		resetForm();
		loadBranches();
	});

	const refOptions = $derived(ref && !branches.includes(ref) ? [ref, ...branches] : branches);

	const missing = $derived(inputs.filter((i) => i.required && i.type !== 'boolean' && !(values[i.name] ?? '').trim()).map((i) => i.name));

	async function submit() {
		if (!workflow || submitting || missing.length > 0 || !ref.trim()) return;
		submitting = true;
		try {
			const payload: Record<string, string> = {};
			for (const i of inputs) payload[i.name] = values[i.name] ?? '';
			const resp = await rpcClient.actions.dispatchWorkflow({
				namespace, name, path: workflow.path, ref: ref.trim(), inputs: payload
			});
			toast.success(`Run #${resp.run?.number ?? ''} queued`);
			open = false;
			if (resp.run) onDispatched?.(resp.run);
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

<FormPanel {open} onOpenChange={(v) => (open = v)} title="Run workflow" description={workflow?.name || workflow?.path || ''} icon={Play}>
	<div class="space-y-3">
		<FormField label="Branch" id="dispatch-ref" required help="Workflow file and code are read from this ref">
			{#if refOptions.length > 0}
				<Select type="single" value={ref} onValueChange={(v) => { if (v) ref = v; }}>
					<SelectTrigger class="w-full font-mono text-[13px]" id="dispatch-ref">
						<span class="inline-flex items-center gap-1.5 truncate"><GitBranch class="h-3.5 w-3.5 text-muted-foreground" />{ref || 'Select branch'}</span>
					</SelectTrigger>
					<SelectContent>
						{#each refOptions as b (b)}
							<SelectItem value={b} class="font-mono text-[13px]">{b}</SelectItem>
						{/each}
					</SelectContent>
				</Select>
			{:else}
				<Input id="dispatch-ref" bind:value={ref} placeholder={branchesLoading ? 'Loading branches...' : 'main'} class="font-mono text-sm" />
			{/if}
		</FormField>

		{#each inputs as input (input.name)}
			{@const id = inputId(input.name)}
			<FormField label={input.name} {id} required={input.required} help={input.description}>
				{#if input.type === 'boolean'}
					<div class="flex items-center gap-2 h-9">
						<Switch {id} checked={values[input.name] === 'true'} onCheckedChange={(v) => (values[input.name] = v ? 'true' : 'false')} />
						<span class="text-[12.5px] text-muted-foreground">{values[input.name] === 'true' ? 'true' : 'false'}</span>
					</div>
				{:else if input.type === 'choice'}
					<Select type="single" value={values[input.name] ?? ''} onValueChange={(v) => (values[input.name] = v ?? '')}>
						<SelectTrigger class="w-full text-[13px]" {id}>
							{values[input.name] || 'Select an option'}
						</SelectTrigger>
						<SelectContent>
							{#each input.options ?? [] as opt (opt)}
								<SelectItem value={opt}>{opt}</SelectItem>
							{/each}
						</SelectContent>
					</Select>
				{:else if input.type === 'number'}
					<Input {id} type="number" bind:value={values[input.name]} class="text-sm tabular-nums" />
				{:else if input.type === 'environment'}
					<Input {id} bind:value={values[input.name]} placeholder="Environment name" class="text-sm" />
				{:else}
					<Input {id} bind:value={values[input.name]} class="text-sm" />
				{/if}
			</FormField>
		{/each}

		{#if inputs.length === 0}
			<p class="text-[12.5px] text-muted-foreground">This workflow takes no inputs.</p>
		{/if}
	</div>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (open = false)}>Cancel</Button>
		<Button onclick={submit} disabled={submitting || !workflow || missing.length > 0 || !ref.trim()}>
			<Play class="h-3.5 w-3.5 mr-1.5" />{submitting ? 'Starting...' : 'Run Workflow'}
		</Button>
	{/snippet}
</FormPanel>

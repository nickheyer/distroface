<script lang="ts">
	import { untrack } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Switch } from '$lib/components/ui/switch';
	import { Badge } from '$lib/components/ui/badge';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import FormActions from '$lib/components/form-actions.svelte';
	import LimitInput from '$lib/components/limit-input.svelte';
	import PasswordInput from '$lib/components/password-input.svelte';
	import { Loader, PlugZap, Plus, Trash, CheckCircle, XCircle } from '$lib/icons';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import {
		AgentProviderKind,
		type AgentModel,
		type AgentProvider,
		type AgentProviderSpec
	} from '$lib/proto/distroface/v1/agents_pb';
	import { providerKindOptions, providerKinds } from './agents-state';
	import { errText } from '$lib/act.svelte';
	import { toast } from 'svelte-sonner';

	let {
		scope,
		provider,
		onSaved,
		cancelHref
	}: {
		scope: { kind: string; id: string };
		provider?: AgentProvider;
		onSaved: (p: AgentProvider) => void;
		cancelHref: string;
	} = $props();

	const initial = untrack(() => provider);
	const editing = !!initial;

	const positive = (v: number | bigint | undefined) => (v && Number(v) > 0 ? Number(v) : undefined);
	const whole = (v: number | null | undefined) => Math.max(0, Math.round(Number(v) || 0));

	let name = $state(initial?.name ?? '');
	let kind = $state<AgentProviderKind>(initial?.kind ?? AgentProviderKind.OPENAI);
	let baseUrl = $state(initial?.baseUrl ?? '');
	let apiKey = $state('');
	let clearKey = $state(false);
	let apiVersion = $state(initial?.apiVersion ?? '');
	let defaultModel = $state(initial?.defaultModel ?? '');
	let models = $state((initial?.models ?? []).join(', '));
	let maxTokens = $state<number | undefined>(positive(initial?.maxTokens));
	let contextWindow = $state<number | undefined>(positive(initial?.contextWindow));
	let timeoutSeconds = $state<number | undefined>(positive(initial?.timeoutSeconds));
	let requestsPerMinute = $state<number | undefined>(positive(initial?.requestsPerMinute));
	let tokensPerDay = $state<number | undefined>(positive(initial?.tokensPerDay));
	let toolsSupported = $state(initial?.toolsSupported ?? true);
	let skipTlsVerify = $state(initial?.skipTlsVerify ?? false);
	let enabled = $state(initial?.enabled ?? true);
	let isDefault = $state(initial?.isDefault ?? false);
	let headers = $state<{ key: string; value: string }[]>([]);
	let editHeaders = $state(!initial?.headersSet);

	let saving = $state(false);
	let testing = $state(false);
	let testResult = $state<{
		ok: boolean;
		message: string;
		sample: string;
		toolsOk: boolean;
	} | null>(null);
	let discovered = $state<AgentModel[]>([]);

	const info = $derived(providerKinds[kind]);
	const keyStored = $derived(editing && !!initial?.apiKeySet);
	const nameValid = $derived(name.trim().length > 0);
	const urlValid = $derived.by(() => {
		const v = baseUrl.trim();
		if (!v) return kind !== AgentProviderKind.AZURE_OPENAI;
		return /^https?:\/\/[^\s]+$/.test(v);
	});

	function spec(): AgentProviderSpec {
		const out: AgentProviderSpec = {
			$typeName: 'distroface.v1.AgentProviderSpec',
			name: name.trim(),
			kind,
			baseUrl: baseUrl.trim(),
			headers: {},
			setHeaders: editHeaders,
			apiVersion: apiVersion.trim(),
			defaultModel: defaultModel.trim(),
			models: models
				.split(',')
				.map((m) => m.trim())
				.filter(Boolean),
			maxTokens: whole(maxTokens),
			timeoutSeconds: whole(timeoutSeconds),
			skipTlsVerify,
			toolsSupported,
			requestsPerMinute: whole(requestsPerMinute),
			tokensPerDay: BigInt(whole(tokensPerDay)),
			enabled,
			isDefault,
			contextWindow: whole(contextWindow)
		};
		if (editHeaders) {
			for (const h of headers) if (h.key.trim()) out.headers[h.key.trim()] = h.value;
		}
		if (apiKey.trim()) out.apiKey = apiKey.trim();
		else if (clearKey || !editing) out.apiKey = '';
		return out;
	}

	async function test() {
		testing = true;
		testResult = null;
		try {
			const resp = await rpcClient.agents.testAgentProvider(
				{ scope, id: initial?.id ?? '', spec: spec(), model: defaultModel.trim() },
				silentCallOptions
			);
			testResult = {
				ok: resp.ok,
				message: resp.message,
				sample: resp.sample,
				toolsOk: resp.toolsOk
			};
			discovered = resp.models;
		} catch (err) {
			testResult = { ok: false, message: errText(err), sample: '', toolsOk: false };
		} finally {
			testing = false;
		}
	}

	async function save() {
		if (!nameValid || !urlValid || saving) return;
		saving = true;
		try {
			const resp = editing
				? await rpcClient.agents.updateAgentProvider({ scope, id: initial!.id, spec: spec() })
				: await rpcClient.agents.createAgentProvider({ scope, spec: spec() });
			toast.success(editing ? 'Connector updated' : 'Connector added');
			if (resp.provider) onSaved(resp.provider);
		} catch {
			// Error interceptor already toasted
		} finally {
			saving = false;
		}
	}
</script>

<FormSection title="Endpoint" description="Where completions are requested.">
	<FormField label="Name" id="provider-name" required span="half">
		<Input id="provider-name" bind:value={name} placeholder="Local vLLM" autocomplete="off" />
	</FormField>
	<FormField label="Protocol" id="provider-kind" required span="half" help={info?.help}>
		<Select
			type="single"
			value={String(kind)}
			onValueChange={(v) => (kind = Number(v) as AgentProviderKind)}
		>
			<SelectTrigger id="provider-kind" class="w-full">
				{info?.label ?? 'Protocol'}
			</SelectTrigger>
			<SelectContent>
				{#each providerKindOptions as k (k)}
					<SelectItem value={String(k)}>{providerKinds[k].label}</SelectItem>
				{/each}
			</SelectContent>
		</Select>
	</FormField>
	<FormField
		label="Base URL"
		id="provider-url"
		required={kind === AgentProviderKind.AZURE_OPENAI}
		help={kind === AgentProviderKind.AZURE_OPENAI ? undefined : 'Blank uses the protocol default'}
		error={!urlValid ? 'Must be an http or https url' : undefined}
	>
		<Input
			id="provider-url"
			bind:value={baseUrl}
			placeholder={info?.baseUrl}
			class="font-mono"
			autocomplete="off"
			aria-invalid={!urlValid}
		/>
	</FormField>
	<FormField
		label="API key"
		id="provider-key"
		span="half"
		required={!keyStored && !!info?.needsKey}
		help={keyStored
			? 'Blank keeps the stored key'
			: info?.needsKey
				? undefined
				: 'Optional for local servers'}
	>
		<PasswordInput
			id="provider-key"
			bind:value={apiKey}
			placeholder={keyStored ? 'Stored' : 'sk-...'}
			autocomplete="new-password"
		/>
	</FormField>
	{#if info?.apiVersion}
		<FormField label="API version" id="provider-api-version" span="half">
			<Input
				id="provider-api-version"
				bind:value={apiVersion}
				placeholder="2024-10-21"
				class="font-mono"
				autocomplete="off"
			/>
		</FormField>
	{/if}
	{#if keyStored}
		<FormField label="Remove the stored key" layout="toggle" newRow>
			<Switch bind:checked={clearKey} />
		</FormField>
	{/if}
	<FormField
		label="Native tool calling"
		layout="toggle"
		help="Off wraps tools in a prompted protocol"
	>
		<Switch bind:checked={toolsSupported} />
	</FormField>
	<FormField label="Skip TLS verification" layout="toggle" help="Self signed endpoints only">
		<Switch bind:checked={skipTlsVerify} />
	</FormField>
</FormSection>

<FormSection title="Models" description="Defaults for agents that name no model of their own.">
	<FormField label="Default model" id="provider-model" span="half">
		<Input
			id="provider-model"
			bind:value={defaultModel}
			placeholder={info?.model}
			class="font-mono"
			autocomplete="off"
		/>
	</FormField>
	<FormField
		label="Allowed models"
		id="provider-models"
		span="half"
		help="Comma separated, blank allows any"
	>
		<Input
			id="provider-models"
			bind:value={models}
			placeholder="gpt-4.1, gpt-4.1-mini"
			class="font-mono"
			autocomplete="off"
		/>
	</FormField>
	{#if discovered.length > 0}
		<div class="space-y-1.5">
			<p class="eyebrow">Served by the endpoint</p>
			<div class="flex max-h-28 flex-wrap gap-1 overflow-auto">
				{#each discovered.slice(0, 60) as m (m.id)}
					<button
						type="button"
						class="rounded-full border px-2 py-0.5 font-mono text-2xs hover:bg-muted"
						onclick={() => (defaultModel = m.id)}>{m.id}</button
					>
				{/each}
			</div>
		</div>
	{/if}
	<FormField
		label="Max output tokens"
		id="provider-max-tokens"
		layout="inline"
		help="Per completion"
	>
		<LimitInput
			id="provider-max-tokens"
			label="output tokens"
			unit="tokens"
			bind:value={maxTokens}
			seed={8192}
		/>
	</FormField>
	<FormField
		label="Context window"
		id="provider-context"
		layout="inline"
		help="Transcripts compact near this size"
	>
		<LimitInput
			id="provider-context"
			label="context window"
			unit="tokens"
			bind:value={contextWindow}
			seed={128000}
		/>
	</FormField>
</FormSection>

<FormSection title="Limits" description="Caps across every agent using this connector.">
	<FormField label="Request timeout" id="provider-timeout" layout="inline">
		<LimitInput
			id="provider-timeout"
			label="request timeout"
			unit="seconds"
			bind:value={timeoutSeconds}
			seed={120}
		/>
	</FormField>
	<FormField label="Requests per minute" id="provider-rpm" layout="inline">
		<LimitInput
			id="provider-rpm"
			label="requests per minute"
			unit="rpm"
			bind:value={requestsPerMinute}
			seed={60}
		/>
	</FormField>
	<FormField label="Tokens per day" id="provider-tpd" layout="inline">
		<LimitInput
			id="provider-tpd"
			label="tokens per day"
			unit="tokens"
			bind:value={tokensPerDay}
			seed={1000000}
		/>
	</FormField>
</FormSection>

<FormSection title="Headers" description="Extra request headers, stored sealed.">
	{#if editing && initial?.headersSet && !editHeaders}
		<div class="flex items-center justify-between gap-3">
			<p class="text-sm text-muted-foreground">Stored headers are kept as they are.</p>
			<Button variant="outline" size="sm" onclick={() => (editHeaders = true)}>Replace</Button>
		</div>
	{:else}
		{#each headers as h, i (i)}
			<div class="flex items-center gap-2">
				<Input bind:value={h.key} placeholder="X-Header" class="font-mono" autocomplete="off" />
				<Input bind:value={h.value} placeholder="value" class="font-mono" autocomplete="off" />
				<Button
					variant="ghost-muted"
					size="icon-sm"
					aria-label="Remove header"
					onclick={() => (headers = headers.filter((_, j) => j !== i))}
				>
					<Trash />
				</Button>
			</div>
		{:else}
			<p class="text-sm text-muted-foreground">None</p>
		{/each}
		<div>
			<Button
				variant="outline"
				size="sm"
				onclick={() => (headers = [...headers, { key: '', value: '' }])}
			>
				<Plus />Add header
			</Button>
		</div>
	{/if}
</FormSection>

<FormSection title="Availability" description="Whether agents can use it.">
	<FormField label="Enabled" layout="toggle">
		<Switch bind:checked={enabled} />
	</FormField>
	<FormField
		label="Default for this scope"
		layout="toggle"
		help="Agents without a connector use it"
	>
		<Switch bind:checked={isDefault} />
	</FormField>
</FormSection>

{#if testResult}
	<Alert variant={testResult.ok ? 'default' : 'destructive'}>
		{#if testResult.ok}<CheckCircle />{:else}<XCircle />{/if}
		<AlertDescription>
			<div class="space-y-1">
				<p>{testResult.message}</p>
				{#if testResult.sample}
					<p class="font-mono text-xs text-muted-foreground">Reply: {testResult.sample}</p>
				{/if}
				{#if testResult.ok && toolsSupported}
					<Badge variant={testResult.toolsOk ? 'success' : 'warning'}>
						{testResult.toolsOk ? 'Tool calling works' : 'Tool call not returned'}
					</Badge>
				{/if}
			</div>
		</AlertDescription>
	</Alert>
{/if}

<FormActions>
	<Button onclick={save} disabled={saving || !nameValid || !urlValid}>
		{#if saving}<Loader class="animate-spin" />{/if}
		{saving ? 'Saving' : editing ? 'Save changes' : 'Add connector'}
	</Button>
	<Button variant="ghost-muted" href={cancelHref}>Cancel</Button>
	<Button variant="outline" onclick={test} disabled={testing || !urlValid}>
		{#if testing}<Loader class="animate-spin" />{:else}<PlugZap />{/if}
		{testing ? 'Testing' : 'Test connection'}
	</Button>
</FormActions>

<script lang="ts">
	import { untrack } from 'svelte';
	import { toast } from 'svelte-sonner';
	import { rpcClient } from '$lib/api/rpc-client';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Switch } from '$lib/components/ui/switch';
	import { Checkbox } from '$lib/components/ui/checkbox';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import FormActions from '$lib/components/form-actions.svelte';
	import { webhookEventLabels } from '$lib/utils';
	import { WebhookEvent, WebhookScope } from '$lib/proto/distroface/v1/types_pb';
	import type { Webhook as WebhookType } from '$lib/proto/distroface/v1/types_pb';
	import isURL from 'validator/lib/isURL';
	import { Loader2, Zap } from '@lucide/svelte';
	import { mode } from 'mode-watcher';
	import CodeMirror from 'svelte-codemirror-editor';
	import { json } from '@codemirror/lang-json';
	import { oneDark } from '@codemirror/theme-one-dark';

	let {
		scope,
		scopeId,
		webhook,
		onSaved,
		cancelHref
	}: {
		scope: WebhookScope;
		scopeId: string;
		webhook?: WebhookType;
		onSaved: () => void;
		cancelHref: string;
	} = $props();

	const initial = untrack(() => webhook);
	const editing = !!initial;
	let url = $state(initial?.url ?? '');
	let secret = $state('');
	let events = $state<WebhookEvent[]>(initial ? [...initial.events] : [WebhookEvent.PUSH]);
	let payloadTemplate = $state(initial?.payloadTemplate ?? '');
	let active = $state(initial?.active ?? true);
	let templatePanelOpen = $state((initial?.payloadTemplate ?? '').length > 0);
	let saving = $state(false);

	const urlValid = $derived(
		isURL(url, { protocols: ['http', 'https'], require_protocol: true, require_tld: false })
	);
	const urlError = $derived(url.length > 0 && !urlValid ? 'Must be a valid HTTP or HTTPS URL' : '');
	const canSubmit = $derived(urlValid && events.length > 0);

	const defaultTemplate = `{
  "event": "{{ .Event }}",
  "repository": "{{ .Repository.FullName }}",
  "tag": "{{ .Tag }}",
  "digest": "{{ .Digest }}",
  "timestamp": "{{ .Timestamp }}"
}`;

	const allEvents = [WebhookEvent.PUSH, WebhookEvent.PULL, WebhookEvent.DELETE];

	function toggleEvent(event: WebhookEvent) {
		events = events.includes(event) ? events.filter((e) => e !== event) : [...events, event];
	}

	function handleTemplatePanelToggle(checked: boolean) {
		templatePanelOpen = checked;
		if (checked && payloadTemplate.length === 0) payloadTemplate = defaultTemplate;
		if (!checked) payloadTemplate = '';
	}

	interface Preset {
		name: string;
		label: string;
		template: string;
	}

	const presets: Preset[] = [
		{
			name: 'discord',
			label: 'Discord',
			template: `{
  "embeds": [{
    "title": "{{ .Repository.FullName }}",
    "description": "**{{ .Event }}** {{ if .Tag }}tag \`{{ .Tag }}\`{{ end }}",
    "color": 5814783,
    "fields": [
      { "name": "Digest", "value": "\`{{ .Digest }}\`", "inline": true }
    ],
    "timestamp": "{{ .Timestamp }}"
  }]
}`
		},
		{
			name: 'slack',
			label: 'Slack',
			template: `{
  "blocks": [
    {
      "type": "section",
      "text": {
        "type": "mrkdwn",
        "text": "*{{ .Event }}* on <{{ .Repository.FullName }}>{{ if .Tag }} - tag \`{{ .Tag }}\`{{ end }}"
      }
    },
    {
      "type": "context",
      "elements": [
        { "type": "mrkdwn", "text": "Digest: \`{{ .Digest }}\` | {{ .Timestamp }}" }
      ]
    }
  ]
}`
		},
		{
			name: 'teams',
			label: 'Teams',
			template: `{
  "type": "message",
  "attachments": [{
    "contentType": "application/vnd.microsoft.card.adaptive",
    "content": {
      "type": "AdaptiveCard",
      "$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
      "version": "1.4",
      "body": [
        { "type": "TextBlock", "size": "medium", "weight": "bolder", "text": "{{ .Repository.FullName }}" },
        { "type": "TextBlock", "text": "**{{ .Event }}**{{ if .Tag }} - tag {{ .Tag }}{{ end }}", "wrap": true },
        { "type": "FactSet", "facts": [
          { "title": "Digest", "value": "{{ .Digest }}" },
          { "title": "Time", "value": "{{ .Timestamp }}" }
        ]}
      ]
    }
  }]
}`
		},
		{
			name: 'ntfy',
			label: 'ntfy',
			template: `{
  "topic": "distroface",
  "title": "{{ .Event }} - {{ .Repository.FullName }}",
  "message": "{{ if .Tag }}Tag: {{ .Tag }}\\n{{ end }}Digest: {{ .Digest }}",
  "tags": ["package"]
}`
		}
	];

	const templateFields = [
		{ name: '.Event', desc: 'Event type (push, pull, delete)' },
		{ name: '.Timestamp', desc: 'ISO 8601 timestamp' },
		{ name: '.Repository.Namespace', desc: 'Repository namespace' },
		{ name: '.Repository.Name', desc: 'Repository name' },
		{ name: '.Repository.FullName', desc: 'namespace/name' },
		{ name: '.Tag', desc: 'Tag name (if applicable)' },
		{ name: '.Digest', desc: 'Image digest' }
	];

	const templateFunctions = [
		{ name: 'toJSON', desc: 'JSON-encode a value', example: '{{ toJSON .Repository }}' },
		{ name: 'toUpper', desc: 'Uppercase string', example: '{{ toUpper .Event }}' },
		{ name: 'toLower', desc: 'Lowercase string', example: '{{ toLower .Event }}' },
		{ name: 'replace', desc: 'Replace all occurrences', example: '{{ replace .Tag "-" "_" }}' }
	];

	function applyPreset(preset: Preset) {
		payloadTemplate = preset.template;
	}

	let cmTheme = $derived(mode.current === 'dark' ? oneDark : undefined);

	async function submit() {
		if (!canSubmit || saving) return;
		saving = true;
		const call = webhook
			? rpcClient.webhook.updateWebhook({
					id: webhook.id,
					url,
					secret: secret || undefined,
					events,
					active,
					contentType: 'application/json',
					payloadTemplate
				})
			: rpcClient.webhook.createWebhook({
					scope,
					repoId: scope === WebhookScope.ORGANIZATION ? '' : scopeId,
					orgId: scope === WebhookScope.ORGANIZATION ? scopeId : '',
					url,
					secret,
					events,
					active,
					contentType: 'application/json',
					payloadTemplate
				});
		const ok = await call.then(
			() => true,
			() => false
		);
		saving = false;
		if (!ok) return;
		toast.success(webhook ? 'Webhook updated' : 'Webhook created');
		onSaved();
	}
</script>

<FormSection title="Endpoint" description="Where events are delivered and which ones fire.">
	<FormField label="Payload URL" id="wh-url" required error={urlError}>
		<Input
			id="wh-url"
			bind:value={url}
			type="url"
			autocomplete="off"
			placeholder="https://example.com/webhook"
			aria-invalid={!!urlError}
		/>
	</FormField>

	<FormField
		label="Secret"
		id="wh-secret"
		help={editing ? 'Blank keeps the current secret' : 'Signs payloads with HMAC-SHA256'}
		span="half"
	>
		<Input
			id="wh-secret"
			bind:value={secret}
			type="text"
			autocomplete="new-password"
			data-1p-ignore
			data-lpignore="true"
			data-bwignore
			class="font-mono"
			placeholder={editing ? 'Leave blank to keep current' : 'Optional secret'}
		/>
	</FormField>

	<FormField label="Events" error={events.length === 0 ? 'Pick at least one event' : ''}>
		<div class="flex flex-wrap gap-4 pt-1">
			{#each allEvents as ev (ev.toString())}
				<label class="flex cursor-pointer items-center gap-2 text-sm">
					<Checkbox checked={events.includes(ev)} onCheckedChange={() => toggleEvent(ev)} />
					{webhookEventLabels[ev]}
				</label>
			{/each}
		</div>
	</FormField>

	<FormField
		label="Active"
		layout="toggle"
		help="Inactive webhooks keep their settings but send nothing"
	>
		<Switch bind:checked={active} />
	</FormField>
</FormSection>

<FormSection
	title="Payload"
	description="Sent as JSON, or shaped with a Go text/template for chat and notification services."
>
	<FormField label="Custom template" layout="toggle">
		<Switch checked={templatePanelOpen} onCheckedChange={handleTemplatePanelToggle} />
	</FormField>

	{#if templatePanelOpen}
		<div class="form-subfields">
			<div class="space-y-2">
				<p class="eyebrow flex items-center gap-1">
					<Zap class="size-3" />Presets
				</p>
				<div class="flex flex-wrap items-center gap-2">
					{#each presets as preset (preset.name)}
						<Button variant="outline" size="xs" onclick={() => applyPreset(preset)}>
							{preset.label}
						</Button>
					{/each}
				</div>
			</div>

			<FormField label="Template" id="wh-template" help="Go text/template for the request body">
				<div class="overflow-hidden rounded-md border">
					<CodeMirror
						bind:value={payloadTemplate}
						lang={json()}
						theme={cmTheme}
						lineWrapping
						styles={{
							'&': {
								height: '320px',
								fontSize: '12px'
							}
						}}
					/>
				</div>
			</FormField>

			<div class="box divide-y text-xs">
				<div class="px-3 py-2.5">
					<p class="eyebrow">Fields</p>
					<div class="mt-1.5 grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5">
						{#each templateFields as field (field.name)}
							<code class="font-mono whitespace-nowrap text-primary"
								>{'{{ ' + field.name + ' }}'}</code
							>
							<span class="text-muted-foreground">{field.desc}</span>
						{/each}
					</div>
				</div>
				<div class="px-3 py-2.5">
					<p class="eyebrow">Functions</p>
					<div class="mt-1.5 grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5">
						{#each templateFunctions as fn (fn.name)}
							<code class="font-mono whitespace-nowrap text-primary">{fn.example}</code>
							<span class="text-muted-foreground">{fn.desc}</span>
						{/each}
					</div>
				</div>
			</div>
		</div>
	{/if}
</FormSection>

<FormActions>
	<Button onclick={submit} disabled={saving || !canSubmit}>
		{#if saving}<Loader2 class="animate-spin" />{/if}
		{saving ? 'Saving' : editing ? 'Save changes' : 'Create webhook'}
	</Button>
	<Button variant="ghost-muted" href={cancelHref}>Cancel</Button>
</FormActions>

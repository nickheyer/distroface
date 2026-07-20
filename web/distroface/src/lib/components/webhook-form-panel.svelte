<script lang="ts">
	import type { Snippet } from 'svelte';
	import { tick } from 'svelte';
	import {
		Sheet,
		SheetContent,
		SheetTitle,
		SheetDescription
	} from '$lib/components/ui/sheet';
	import { Input } from '$lib/components/ui/input';
	import { Switch } from '$lib/components/ui/switch';
	import { Checkbox } from '$lib/components/ui/checkbox';
	import FormField from '$lib/components/form-field.svelte';
	import { webhookEventLabels } from '$lib/utils';
	import { WebhookEvent } from '$lib/proto/distroface/v1/types_pb';
	import isURL from 'validator/lib/isURL';
	import { Webhook, Zap } from '@lucide/svelte';
	import { mode } from 'mode-watcher';
	import CodeMirror from 'svelte-codemirror-editor';
	import { json } from '@codemirror/lang-json';
	import { oneDark } from '@codemirror/theme-one-dark';

	let {
		open = $bindable(false),
		title,
		description = '',
		formMode = 'create',
		idPrefix = 'wh',
		url = $bindable(''),
		secret = $bindable(''),
		events = $bindable<WebhookEvent[]>([WebhookEvent.PUSH]),
		payloadTemplate = $bindable(''),
		active = $bindable(true),
		footer,
		onOpenChange
	}: {
		open: boolean;
		title: string;
		description?: string;
		formMode?: 'create' | 'edit';
		idPrefix?: string;
		url: string;
		secret: string;
		events: WebhookEvent[];
		payloadTemplate: string;
		active: boolean;
		footer?: Snippet;
		onOpenChange?: (open: boolean) => void;
	} = $props();

	let templatePanelOpen = $state(false);
	let panelScroll = $state<HTMLElement | null>(null);
	let urlError = $derived(
		url.length > 0 &&
			!isURL(url, { protocols: ['http', 'https'], require_protocol: true, require_tld: false })
			? 'Must be a valid HTTP or HTTPS URL'
			: ''
	);

	const defaultTemplate = `{
  "event": "{{ .Event }}",
  "repository": "{{ .Repository.FullName }}",
  "tag": "{{ .Tag }}",
  "digest": "{{ .Digest }}",
  "timestamp": "{{ .Timestamp }}"
}`;

	const allEvents = [WebhookEvent.PUSH, WebhookEvent.PULL, WebhookEvent.DELETE];

	function toggleEvent(event: WebhookEvent) {
		events = events.includes(event)
			? events.filter((e) => e !== event)
			: [...events, event];
	}

	function handleTemplatePanelToggle(checked: boolean) {
		templatePanelOpen = checked;
		if (checked && payloadTemplate.length === 0) {
			payloadTemplate = defaultTemplate;
		}
		if (!checked) {
			payloadTemplate = '';
		}
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

	function handleOpenChange(value: boolean) {
		open = value;
		onOpenChange?.(value);
	}

	// Auto-expand template panel when editing a webhook that has a template
	$effect(() => {
		if (open && payloadTemplate.length > 0) {
			templatePanelOpen = true;
		}
	});

	// Smooth scroll to template panel on expand
	$effect(() => {
		if (templatePanelOpen && panelScroll) {
			tick().then(() => {
				panelScroll?.scrollTo({ left: panelScroll.scrollWidth, behavior: 'smooth' });
			});
		}
	});

	// Reset template panel when sheet closes
	$effect(() => {
		if (!open) {
			templatePanelOpen = false;
		}
	});

	let cmTheme = $derived(mode.current === 'dark' ? oneDark : undefined);
</script>

<Sheet bind:open onOpenChange={handleOpenChange}>
	<SheetContent
		side="right"
		class="w-full overflow-hidden p-0 flex flex-col"
		style="max-width: min({templatePanelOpen ? 80 : 32}rem, 85vw); transition: max-width 200ms ease-in-out;"
	>
		<!-- Header -->
		<div class="flex items-start gap-3 px-6 py-5 border-b border-border/40 bg-muted/20 shrink-0">
			<div
				class="h-10 w-10 rounded-xl bg-primary/10 flex items-center justify-center shrink-0 mt-0.5"
			>
				<Webhook class="h-5 w-5 text-primary" />
			</div>
			<div class="flex-1 min-w-0">
				<SheetTitle class="text-lg font-semibold tracking-tight">{title}</SheetTitle>
				{#if description}
					<SheetDescription class="text-[13px] text-muted-foreground mt-1"
						>{description}</SheetDescription
					>
				{:else}
					<SheetDescription class="sr-only">{title}</SheetDescription>
				{/if}
			</div>
		</div>

		<!-- Body: two-panel layout -->
		<div bind:this={panelScroll} class="flex-1 flex overflow-x-hidden overflow-y-auto min-h-0">
			<!-- Left panel: main form -->
			<div
				class="{templatePanelOpen
					? 'w-lg shrink-0'
					: 'flex-1'} overflow-y-auto {templatePanelOpen
					? 'border-r border-border/30'
					: ''}"
			>
				<div class="px-6 py-6 space-y-4">
					<FormField label="Payload URL" id="{idPrefix}-url" required error={urlError}>
						<Input
							id="{idPrefix}-url"
							bind:value={url}
							type="url"
							autocomplete="off"
							placeholder="https://example.com/webhook"
						/>
					</FormField>

					<FormField
						label="Secret"
						id="{idPrefix}-secret"
						help={formMode === 'create'
							? 'Signs payloads with HMAC-SHA256'
							: undefined}
					>
						<Input
							id="{idPrefix}-secret"
							bind:value={secret}
							type="text"
							autocomplete="new-password"
							data-1p-ignore
							data-lpignore="true"
							data-bwignore
							class="font-mono"
							placeholder={formMode === 'create'
								? 'Optional secret'
								: 'Leave blank to keep current'}
						/>
					</FormField>

					<FormField label="Events">
						<div class="flex flex-wrap gap-3 pt-1">
							{#each allEvents as ev (ev.toString())}
								<label class="flex items-center gap-2 text-sm cursor-pointer">
									<Checkbox
										checked={events.includes(ev)}
										onCheckedChange={() => toggleEvent(ev)}
									/>
									{webhookEventLabels[ev]}
								</label>
							{/each}
						</div>
					</FormField>

					<FormField
						label="Custom template"
						horizontal
					>
						<Switch checked={templatePanelOpen} onCheckedChange={handleTemplatePanelToggle} />
					</FormField>

					<FormField label="Active" horizontal>
						<Switch bind:checked={active} />
					</FormField>
				</div>
			</div>

			<!-- Right panel: template editor -->
			{#if templatePanelOpen}
				<div class="flex-1 overflow-y-auto">
					<div class="px-6 py-6 space-y-4">
						<!-- Presets -->
						<div class="space-y-2">
							<span
								class="text-[11px] text-muted-foreground/60 font-medium uppercase tracking-wider flex items-center gap-1"
							>
								<Zap class="h-3 w-3" />Presets
							</span>
							<div class="flex items-center gap-2 flex-wrap">
								{#each presets as preset (preset.name)}
									<button
										type="button"
										class="inline-flex items-center gap-1 rounded-md border border-border/60 bg-background px-2 py-0.5 text-[11px] font-medium text-muted-foreground hover:bg-muted hover:text-foreground hover:border-border transition-colors cursor-pointer"
										onclick={() => applyPreset(preset)}
									>
										{preset.label}
									</button>
								{/each}
							</div>
						</div>

						<!-- CodeMirror Editor -->
						<FormField
							label="Template"
							id="{idPrefix}-template"
							help="Go text/template for the request body"
						>
							<div class="rounded-md border border-border/60 overflow-hidden">
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

						<!-- Template Reference -->
						<div
							class="rounded-lg border border-border/50 bg-muted/20 overflow-hidden text-[11px]"
						>
							<div class="px-3 py-2 border-b border-border/30">
								<span
									class="font-semibold text-muted-foreground uppercase tracking-wider text-[10px]"
									>Fields</span
								>
								<div
									class="mt-1.5 grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5"
								>
									{#each templateFields as field (field.name)}
										<code class="font-mono text-primary/80 whitespace-nowrap"
											>{'{{ ' + field.name + ' }}'}</code
										>
										<span class="text-muted-foreground/70">{field.desc}</span>
									{/each}
								</div>
							</div>
							<div class="px-3 py-2">
								<span
									class="font-semibold text-muted-foreground uppercase tracking-wider text-[10px]"
									>Functions</span
								>
								<div
									class="mt-1.5 grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5"
								>
									{#each templateFunctions as fn (fn.name)}
										<code class="font-mono text-primary/80 whitespace-nowrap"
											>{fn.example}</code
										>
										<span class="text-muted-foreground/70">{fn.desc}</span>
									{/each}
								</div>
							</div>
						</div>
					</div>
				</div>
			{/if}
		</div>

		<!-- Footer -->
		{#if footer}
			<div
				class="flex items-center justify-end gap-3 px-6 py-4 border-t border-border/40 bg-muted/20"
			>
				{@render footer()}
			</div>
		{/if}
	</SheetContent>
</Sheet>

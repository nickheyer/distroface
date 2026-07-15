<script lang="ts">
	import type { Snippet } from 'svelte';
	import FormPanel from '$lib/components/form-panel.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import { Input } from '$lib/components/ui/input';
	import { Switch } from '$lib/components/ui/switch';
	import { Button } from '$lib/components/ui/button';
	import { Globe, Plus, X } from '@lucide/svelte';
	import { parseEndpoint } from '$lib/portal-endpoint';

	type RuleDraft = { pattern: string; replace: string };

	let {
		open = $bindable(false),
		title,
		description = '',
		formMode = 'create',
		idPrefix = 'portal',
		orgName,
		name = $bindable(''),
		endpoint = $bindable(''),
		mapUnqualified = $bindable(true),
		allowPush = $bindable(true),
		requireAuth = $bindable(false),
		enabled = $bindable(true),
		rules = $bindable<RuleDraft[]>([]),
		footer
	}: {
		open: boolean;
		title: string;
		description?: string;
		formMode?: 'create' | 'edit';
		idPrefix?: string;
		orgName: string;
		name: string;
		endpoint: string;
		mapUnqualified: boolean;
		allowPush: boolean;
		requireAuth: boolean;
		enabled?: boolean;
		rules: RuleDraft[];
		footer?: Snippet;
	} = $props();

	const endpointError = $derived(parseEndpoint(endpoint).error);

	function addRule() {
		rules = [...rules, { pattern: '', replace: '' }];
	}

	function removeRule(index: number) {
		rules = rules.filter((_, i) => i !== index);
	}
</script>

<FormPanel bind:open {title} {description} icon={Globe} {footer}>
	<div class="space-y-6">
		<FormSection title="Portal">
			<div class="space-y-3">
				<FormField label="Name" id="{idPrefix}-name" required help="A short label for this portal.">
					<Input id="{idPrefix}-name" bind:value={name} placeholder="e.g. mirror" />
				</FormField>

				<FormField
					label="Endpoint"
					id="{idPrefix}-endpoint"
					required
					error={endpointError}
					help="Where this portal answers - host, host:port, or :port. A port opens a dedicated proxy listener (shareable between portals), :port alone catches any hostname on it. Point DNS for hostnames at this server."
				>
					<Input
						id="{idPrefix}-endpoint"
						bind:value={endpoint}
						class="font-mono"
						placeholder="registry.example.com, registry.example.com:5001, or :5001"
					/>
				</FormField>
			</div>
		</FormSection>

		<FormSection title="Options">
			<div class="space-y-3">
				<FormField
					label="Map unqualified names into org namespace"
					horizontal
					help="e.g. myimage resolves to {orgName}/myimage."
				>
					<Switch bind:checked={mapUnqualified} />
				</FormField>

				<FormField label="Allow push" horizontal help="Off makes this a read-only portal.">
					<Switch bind:checked={allowPush} />
				</FormField>

				<FormField
					label="Require authentication"
					horizontal
					help="Require auth for all access, including pulls of public repositories."
				>
					<Switch bind:checked={requireAuth} />
				</FormField>

				{#if formMode === 'edit'}
					<FormField label="Enabled" horizontal help="Disabled portals reject all requests.">
						<Switch bind:checked={enabled} />
					</FormField>
				{/if}
			</div>
		</FormSection>

		<FormSection
			title="Custom rules"
			description="Advanced: regex rewrites applied to requested repository names, in order — first match wins."
		>
			{#snippet actions()}
				<Button variant="outline" size="sm" onclick={addRule}>
					<Plus class="h-3.5 w-3.5 mr-1.5" />Add Rule
				</Button>
			{/snippet}

			{#if rules.length === 0}
				<p class="text-[13px] text-muted-foreground/70">
					No custom rules. Most portals only need the options above.
				</p>
			{:else}
				<div class="space-y-2">
					{#each rules as rule, i (i)}
						<div class="flex items-center gap-2">
							<Input
								bind:value={rule.pattern}
								class="font-mono text-xs"
								placeholder="pattern (regex)"
								aria-label="Rule pattern"
							/>
							<span class="text-xs text-muted-foreground shrink-0">→</span>
							<Input
								bind:value={rule.replace}
								class="font-mono text-xs"
								placeholder="replace ($1, ...)"
								aria-label="Rule replacement"
							/>
							<Button
								variant="ghost"
								size="icon"
								class="h-8 w-8 shrink-0 text-destructive hover:text-destructive"
								onclick={() => removeRule(i)}
							>
								<X class="h-3.5 w-3.5" />
							</Button>
						</div>
					{/each}
				</div>
			{/if}
		</FormSection>
	</div>
</FormPanel>

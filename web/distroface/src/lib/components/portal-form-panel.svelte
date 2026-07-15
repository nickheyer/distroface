<script lang="ts">
	import type { Snippet } from 'svelte';
	import FormPanel from '$lib/components/form-panel.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import { Input } from '$lib/components/ui/input';
	import { Switch } from '$lib/components/ui/switch';
	import { Button } from '$lib/components/ui/button';
	import { Globe, Plus, X } from '@lucide/svelte';
	import { parseAddress } from '$lib/portal-address';

	type RuleDraft = { pattern: string; replace: string };

	let {
		open = $bindable(false),
		title,
		description = '',
		idPrefix = 'portal',
		orgName,
		name = $bindable(''),
		address = $bindable(''),
		mapUnqualified = $bindable(true),
		allowPush = $bindable(true),
		requireAuth = $bindable(false),
		rules = $bindable<RuleDraft[]>([]),
		footer
	}: {
		open: boolean;
		title: string;
		description?: string;
		idPrefix?: string;
		orgName: string;
		name: string;
		address: string;
		mapUnqualified: boolean;
		allowPush: boolean;
		requireAuth: boolean;
		rules: RuleDraft[];
		footer?: Snippet;
	} = $props();

	const addressError = $derived(address.trim() === '' ? '' : parseAddress(address).error);

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
				<FormField label="Name" id="{idPrefix}-name" required help="Short label for this portal.">
					<Input id="{idPrefix}-name" bind:value={name} placeholder="e.g. mirror" />
				</FormField>

				<FormField
					label="Address"
					id="{idPrefix}-address"
					required
					error={addressError}
					help="A hostname (inherit port), hostname:port, or :port (inherit hostname). Point hostname DNS at this server."
				>
					<Input
						id="{idPrefix}-address"
						bind:value={address}
						class="font-mono"
						placeholder="registry.example.com"
					/>
				</FormField>
			</div>
		</FormSection>

		<FormSection title="Options">
			<div class="space-y-3">
				<FormField label="Map bare image names" horizontal help="myimage → {orgName}/myimage">
					<Switch bind:checked={mapUnqualified} />
				</FormField>

				<FormField label="Allow push" horizontal help="Off makes this portal pull-only.">
					<Switch bind:checked={allowPush} />
				</FormField>

				<FormField label="Require authentication" horizontal help="Require login even for public pulls.">
					<Switch bind:checked={requireAuth} />
				</FormField>
			</div>
		</FormSection>

		<FormSection
			title="Rewrite rules"
			description="Optional regex rewrites for requested image names. First match wins. Results must be under {orgName}/."
		>
			{#snippet actions()}
				<Button variant="outline" size="sm" onclick={addRule}>
					<Plus class="h-3.5 w-3.5 mr-1.5" />Add Rule
				</Button>
			{/snippet}

			{#if rules.length === 0}
				<p class="text-[13px] text-muted-foreground/70">No rules. Most portals don't need any.</p>
			{:else}
				<div class="space-y-2">
					{#each rules as rule, i (i)}
						<div class="flex items-center gap-2">
							<Input
								bind:value={rule.pattern}
								class="font-mono text-xs"
								placeholder="legacy/(.+)"
								aria-label="Rule pattern"
							/>
							<span class="text-xs text-muted-foreground shrink-0">→</span>
							<Input
								bind:value={rule.replace}
								class="font-mono text-xs"
								placeholder="{orgName}/$1"
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

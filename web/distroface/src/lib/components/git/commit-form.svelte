<script lang="ts">
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import { RadioGroup, RadioGroupItem } from '$lib/components/ui/radio-group';
	import FormCard from '$lib/components/form-card.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import { authStore } from '$lib/stores/auth.svelte';
	import { GitCommit, GitBranchPlus, GitBranch, Loader2 } from '@lucide/svelte';

	let {
		summary = $bindable(''),
		description = $bindable(''),
		mode = $bindable('direct'),
		newBranch = $bindable(''),
		branch,
		placeholder = 'Update files',
		submitLabel = 'Commit changes',
		submitting = false,
		disabled = false,
		onSubmit,
		onCancel
	}: {
		summary: string;
		description: string;
		mode: 'direct' | 'branch';
		newBranch: string;
		branch: string;
		placeholder?: string;
		submitLabel?: string;
		submitting?: boolean;
		disabled?: boolean;
		onSubmit: () => void;
		onCancel: () => void;
	} = $props();

	const user = $derived(authStore.user);
	const branchValid = $derived(mode === 'direct' || /^[^\s~^:?*[\\]+$/.test(newBranch.trim()));
	const ready = $derived(
		!disabled && !submitting && branchValid && (summary.trim() !== '' || placeholder !== '')
	);
	const author = $derived(
		user
			? `Committed as ${user.displayName || user.username}${user.email ? ` <${user.email}>` : ''}`
			: undefined
	);

	// Suggests a branch name for the PR path
	function pickMode(v: string) {
		mode = v === 'branch' ? 'branch' : 'direct';
		if (mode === 'branch' && !newBranch.trim() && user) {
			newBranch = `${user.username}-patch-${Math.floor(Date.now() / 1000).toString(36)}`;
		}
	}
</script>

<FormCard title="Commit changes" description={author} icon={GitCommit}>
	<div class="space-y-4">
		<FormField label="Summary" id="commit-summary" required>
			<Input id="commit-summary" bind:value={summary} {placeholder} maxlength={200} />
		</FormField>
		<FormField label="Description" id="commit-description" help="Optional extended description">
			<Textarea
				id="commit-description"
				bind:value={description}
				rows={3}
				placeholder="Explain the change"
			/>
		</FormField>
		<FormField label="Target" id="commit-mode">
			<RadioGroup id="commit-mode" value={mode} onValueChange={pickMode} class="gap-2">
				<label
					class="flex cursor-pointer items-start gap-3 rounded-lg border px-4 py-3 has-[[data-state=checked]]:border-primary/40 has-[[data-state=checked]]:bg-primary/5"
				>
					<RadioGroupItem value="direct" class="mt-0.5" />
					<span class="flex min-w-0 flex-wrap items-center gap-1.5 text-sm font-medium">
						<GitBranch class="size-3.5 shrink-0 text-muted-foreground" />
						Commit directly to
						<code class="rounded bg-muted px-1.5 py-0.5 font-mono text-xs break-all">{branch}</code>
					</span>
				</label>
				<label
					class="flex cursor-pointer items-start gap-3 rounded-lg border px-4 py-3 has-[[data-state=checked]]:border-primary/40 has-[[data-state=checked]]:bg-primary/5"
				>
					<RadioGroupItem value="branch" class="mt-0.5" />
					<span class="min-w-0 flex-1 space-y-2">
						<span class="flex items-center gap-1.5 text-sm font-medium">
							<GitBranchPlus class="size-3.5 shrink-0 text-muted-foreground" />
							Create a new branch and start a pull request
						</span>
						{#if mode === 'branch'}
							<Input
								bind:value={newBranch}
								placeholder="feature/my-change"
								class="font-mono"
								aria-label="New branch name"
								aria-invalid={!branchValid}
							/>
						{/if}
					</span>
				</label>
			</RadioGroup>
		</FormField>
	</div>
	{#snippet footer()}
		<Button variant="outline" onclick={onCancel} disabled={submitting}>Cancel</Button>
		<Button onclick={onSubmit} disabled={!ready}>
			{#if submitting}
				<Loader2 class="animate-spin" />
			{:else}
				<GitCommit />
			{/if}
			{submitting ? 'Committing' : mode === 'branch' ? 'Propose changes' : submitLabel}
		</Button>
	{/snippet}
</FormCard>

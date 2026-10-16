<script lang="ts">
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import { RadioGroup, RadioGroupItem } from '$lib/components/ui/radio-group';
	import FormCard from '$lib/components/form-card.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import { authStore } from '$lib/stores/auth.svelte';
	import { GitCommit, GitBranchPlus, GitBranch } from '@lucide/svelte';

	let {
		summary = $bindable(''),
		description = $bindable(''),
		mode = $bindable('direct'),
		newBranch = $bindable(''),
		branch,
		placeholder = 'Update files',
		submitLabel = 'Commit Changes',
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
	const ready = $derived(!disabled && !submitting && branchValid && (summary.trim() !== '' || placeholder !== ''));
	const author = $derived(
		user ? `Committed as ${user.displayName || user.username}${user.email ? ` <${user.email}>` : ''}` : undefined
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
	<div class="space-y-3">
		<FormField label="Summary" id="commit-summary" required>
			<Input id="commit-summary" bind:value={summary} {placeholder} maxlength={200} />
		</FormField>
		<FormField label="Description" id="commit-description" help="Optional extended description">
			<Textarea id="commit-description" bind:value={description} rows={3} placeholder="Explain the change" />
		</FormField>
		<FormField label="Target" id="commit-mode">
			<RadioGroup id="commit-mode" value={mode} onValueChange={pickMode} class="gap-2">
				<label class="flex items-start gap-3 rounded-lg border border-border/60 px-4 py-3 cursor-pointer has-[[data-state=checked]]:border-primary/40 has-[[data-state=checked]]:bg-primary/5">
					<RadioGroupItem value="direct" class="mt-0.5" />
					<span class="min-w-0 flex items-center gap-1.5 text-sm font-medium flex-wrap">
						<GitBranch class="h-3.5 w-3.5 text-muted-foreground shrink-0" />
						Commit directly to <code class="font-mono text-xs bg-muted px-1.5 py-0.5 rounded break-all">{branch}</code>
					</span>
				</label>
				<label class="flex items-start gap-3 rounded-lg border border-border/60 px-4 py-3 cursor-pointer has-[[data-state=checked]]:border-primary/40 has-[[data-state=checked]]:bg-primary/5">
					<RadioGroupItem value="branch" class="mt-0.5" />
					<span class="min-w-0 flex-1 space-y-2">
						<span class="flex items-center gap-1.5 text-sm font-medium">
							<GitBranchPlus class="h-3.5 w-3.5 text-muted-foreground shrink-0" />
							Create a new branch and start a pull request
						</span>
						{#if mode === 'branch'}
							<Input
								bind:value={newBranch}
								placeholder="feature/my-change"
								class="font-mono text-sm"
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
		<Button onclick={onSubmit} disabled={!ready} class="gap-2">
			<GitCommit class="h-4 w-4" />
			{submitting ? 'Committing...' : mode === 'branch' ? 'Propose Changes' : submitLabel}
		</Button>
	{/snippet}
</FormCard>

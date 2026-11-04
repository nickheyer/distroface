<script lang="ts" module>
	import type { GitBranchProtection } from '$lib/proto/distroface/v1/git_pb';

	export type ProtectionForm = {
		id: string;
		pattern: string;
		priority: number;
		blockForcePush: boolean;
		blockDeletion: boolean;
		requirePullRequest: boolean;
		requiredApprovals: number;
		dismissStaleApprovals: boolean;
		requireCodeOwnerReview: boolean;
		requireStatusChecks: boolean;
		statusCheckContexts: string[];
		requireSignedCommits: boolean;
		requireLinearHistory: boolean;
		requireUpToDate: boolean;
		allowedPushers: string[];
		allowedMergers: string[];
		bypassIds: string[];
	};

	export function emptyProtection(): ProtectionForm {
		return {
			id: '',
			pattern: '',
			priority: 0,
			blockForcePush: true,
			blockDeletion: true,
			requirePullRequest: false,
			requiredApprovals: 0,
			dismissStaleApprovals: false,
			requireCodeOwnerReview: false,
			requireStatusChecks: false,
			statusCheckContexts: [],
			requireSignedCommits: false,
			requireLinearHistory: false,
			requireUpToDate: false,
			allowedPushers: [],
			allowedMergers: [],
			bypassIds: []
		};
	}

	export function protectionFrom(p: GitBranchProtection): ProtectionForm {
		return {
			id: p.id,
			pattern: p.pattern,
			priority: p.priority,
			blockForcePush: p.blockForcePush,
			blockDeletion: p.blockDeletion,
			requirePullRequest: p.requirePullRequest,
			requiredApprovals: p.requiredApprovals,
			dismissStaleApprovals: p.dismissStaleApprovals,
			requireCodeOwnerReview: p.requireCodeOwnerReview,
			requireStatusChecks: p.requireStatusChecks,
			statusCheckContexts: [...p.statusCheckContexts],
			requireSignedCommits: p.requireSignedCommits,
			requireLinearHistory: p.requireLinearHistory,
			requireUpToDate: p.requireUpToDate,
			allowedPushers: [...p.allowedPushers],
			allowedMergers: [...p.allowedMergers],
			bypassIds: [...p.bypassIds]
		};
	}

	export function protectionInit(f: ProtectionForm) {
		return {
			...f,
			pattern: f.pattern.trim(),
			priority: Math.max(0, Math.trunc(Number(f.priority) || 0)),
			requiredApprovals: Math.max(0, Math.trunc(Number(f.requiredApprovals) || 0)),
			statusCheckContexts: f.statusCheckContexts.map((c) => c.trim()).filter(Boolean)
		};
	}
</script>

<script lang="ts">
	import { Input } from '$lib/components/ui/input';
	import { Switch } from '$lib/components/ui/switch';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import PrincipalPicker from '$lib/components/git/principal-picker.svelte';
	import { X, Plus } from '$lib/icons';

	let { form = $bindable(), orgId = '' }: { form: ProtectionForm; orgId?: string } = $props();

	let contextDraft = $state('');

	function addContext() {
		const c = contextDraft.trim();
		contextDraft = '';
		if (!c || form.statusCheckContexts.includes(c)) return;
		form.statusCheckContexts = [...form.statusCheckContexts, c];
	}
</script>

<FormSection
	title={form.id ? 'Rule' : 'New rule'}
	description="Which branches it covers and how it ranks against other rules."
>
	<FormField
		label="Branch pattern"
		id="prot-pattern"
		required
		help="Glob over branch names, main or release/*"
		span="two-thirds"
	>
		<Input id="prot-pattern" bind:value={form.pattern} placeholder="main" class="font-mono" />
	</FormField>
	<FormField label="Priority" id="prot-priority" help="Higher wins on overlap" span="third">
		<Input id="prot-priority" type="number" min={0} bind:value={form.priority} />
	</FormField>
</FormSection>

<FormSection title="Pushes" description="Checked on every push to matching branches.">
	<FormField
		label="Block force pushes"
		layout="toggle"
		help="The old tip must be an ancestor of the new one"
	>
		<Switch bind:checked={form.blockForcePush} />
	</FormField>
	<FormField label="Block deletion" layout="toggle">
		<Switch bind:checked={form.blockDeletion} />
	</FormField>
	<FormField
		label="Require signed commits"
		layout="toggle"
		help="Every new commit must verify against a stored key"
	>
		<Switch bind:checked={form.requireSignedCommits} />
	</FormField>
	<FormField label="Require linear history" layout="toggle" help="Merge commits are refused">
		<Switch bind:checked={form.requireLinearHistory} />
	</FormField>
	<FormField label="Allowed pushers" help="Empty allows every writer, users and teams otherwise">
		<PrincipalPicker bind:selected={form.allowedPushers} {orgId} />
	</FormField>
</FormSection>

<FormSection title="Pull requests" description="Enforced when a pull request merges.">
	<FormField
		label="Require pull request"
		layout="toggle"
		help="Direct pushes are refused, changes land through merges"
	>
		<Switch bind:checked={form.requirePullRequest} />
	</FormField>
	{#if form.requirePullRequest}
		<div class="form-subfields">
			<FormField label="Required approvals" id="prot-approvals" layout="inline">
				<Input
					id="prot-approvals"
					type="number"
					min={0}
					bind:value={form.requiredApprovals}
					class="w-20"
				/>
			</FormField>
			<FormField
				label="Dismiss stale approvals"
				layout="toggle"
				help="New commits reset existing approvals"
			>
				<Switch bind:checked={form.dismissStaleApprovals} />
			</FormField>
			<FormField
				label="Require code owner review"
				layout="toggle"
				help="Owners from CODEOWNERS must approve"
			>
				<Switch bind:checked={form.requireCodeOwnerReview} />
			</FormField>
			<FormField
				label="Require up to date branch"
				layout="toggle"
				help="The head must contain the base tip before merging"
			>
				<Switch bind:checked={form.requireUpToDate} />
			</FormField>
			<FormField label="Allowed mergers" help="Empty allows every writer">
				<PrincipalPicker bind:selected={form.allowedMergers} {orgId} />
			</FormField>
		</div>
	{/if}
	<FormField
		label="Require status checks"
		layout="toggle"
		help="Listed contexts must succeed on the head commit"
	>
		<Switch bind:checked={form.requireStatusChecks} />
	</FormField>
	{#if form.requireStatusChecks}
		<div class="form-subfields">
			<FormField
				label="Status check contexts"
				id="prot-context"
				help="Empty requires every reported context to pass"
			>
				<div class="space-y-2">
					{#if form.statusCheckContexts.length > 0}
						<div class="flex flex-wrap gap-1.5">
							{#each form.statusCheckContexts as ctx (ctx)}
								<Badge variant="secondary" class="pr-1 font-mono">
									{ctx}
									<button
										type="button"
										class="rounded-full p-0.5 hover:bg-foreground/10"
										aria-label="Remove {ctx}"
										onclick={() =>
											(form.statusCheckContexts = form.statusCheckContexts.filter(
												(c) => c !== ctx
											))}
									>
										<X class="size-3" />
									</button>
								</Badge>
							{/each}
						</div>
					{/if}
					<div class="flex gap-2">
						<Input
							id="prot-context"
							bind:value={contextDraft}
							placeholder="ci/build"
							class="font-mono"
							onkeydown={(e) => {
								if (e.key === 'Enter') {
									e.preventDefault();
									addContext();
								}
							}}
						/>
						<Button
							variant="outline"
							class="shrink-0"
							onclick={addContext}
							disabled={!contextDraft.trim()}
						>
							<Plus />Add
						</Button>
					</div>
				</div>
			</FormField>
		</div>
	{/if}
</FormSection>

<FormSection title="Bypass" description="Users and teams the rule never applies to.">
	<FormField label="Bypass list" help="Empty means nobody bypasses">
		<PrincipalPicker bind:selected={form.bypassIds} {orgId} placeholder="Nobody" />
	</FormField>
</FormSection>

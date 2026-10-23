<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { getContext } from 'svelte';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import AgentManager from '$lib/components/git/agent-manager.svelte';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import {
		REPO_AGENTS_CONTEXT_KEY,
		type RepoAgentsContext
	} from '$lib/components/git/repo-agents-context.svelte';
	import { FileCode2 } from '@lucide/svelte';

	const ctx = getContext<RepoAgentsContext>(REPO_AGENTS_CONTEXT_KEY);
	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);
	const overview = $derived(ctx.overview);
</script>

<div class="space-y-4">
	{#if overview && repo}
		<Alert>
			<FileCode2 />
			<AlertDescription>
				{#if overview.instructionFiles.length > 0}
					Repository instructions read by every session:
					{#each overview.instructionFiles as f, i (f)}
						<a
							href={resolve('/[namespace]/[name]/blob/[ref]/[...path]', {
								namespace,
								name,
								ref: encodeURIComponent(repo.defaultBranch),
								path: f
							})}
							class="link font-mono">{f}</a
						>{i < overview.instructionFiles.length - 1 ? ', ' : ''}
					{/each}
				{:else}
					An AGENTS.md at the repository root steers every session. Agent files under
					.distroface/agents define agents with yaml front matter.
				{/if}
			</AlertDescription>
		</Alert>
	{/if}

	<AgentManager
		scope={{ kind: 'repo', id: `${namespace}/${name}` }}
		newHref={resolve('/[namespace]/[name]/settings/agents/definitions/new', { namespace, name })}
		editHref={(id) =>
			resolve('/[namespace]/[name]/settings/agents/definitions/[id]', { namespace, name, id })}
		includeInherited
		canManage={gitRepoStore.canAdmin}
		description="Every agent available here, from this repository, its files, the organization, and the instance."
	/>
</div>

<script lang="ts">
	import { GitFork } from '$lib/icons';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import type { GitRepository } from '$lib/proto/distroface/v1/git_pb';

	// Fork repositories may target their upstream, the picker only renders for forks
	let {
		repo,
		onSelect
	}: {
		repo: GitRepository;
		onSelect: (fullName: string) => void;
	} = $props();

	const self = $derived(`${repo.namespace}/${repo.name}`);
</script>

{#if repo.forkOf}
	<Select
		type="single"
		value={self}
		onValueChange={(v) => {
			if (v && v !== self) onSelect(v);
		}}
	>
		<SelectTrigger class="max-w-full font-mono sm:max-w-64" aria-label="Base repository">
			<GitFork class="size-3.5" />
			{self}
		</SelectTrigger>
		<SelectContent>
			<SelectItem value={self}>{self}</SelectItem>
			<SelectItem value={repo.forkOf}>{repo.forkOf}</SelectItem>
		</SelectContent>
	</Select>
{/if}

<script lang="ts">
	import { GitFork } from '@lucide/svelte';
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
	<Select type="single" value={self} onValueChange={(v) => { if (v && v !== self) onSelect(v); }}>
		<SelectTrigger class="h-8 max-w-full sm:max-w-[16rem] text-[12.5px] font-mono" size="sm" aria-label="Base repository">
			<GitFork class="h-3.5 w-3.5 text-muted-foreground" />
			{self}
		</SelectTrigger>
		<SelectContent>
			<SelectItem value={self}>{self}</SelectItem>
			<SelectItem value={repo.forkOf}>{repo.forkOf}</SelectItem>
		</SelectContent>
	</Select>
{/if}

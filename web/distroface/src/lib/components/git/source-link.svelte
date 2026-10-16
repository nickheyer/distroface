<script lang="ts">
	import { resolve } from '$app/paths';
	import { GitBranch } from '@lucide/svelte';
	import { cn } from '$lib/utils';

	let {
		namespace,
		name,
		label = 'Source repository',
		class: className
	}: {
		namespace: string;
		name: string;
		label?: string;
		class?: string;
	} = $props();
</script>

<div class={cn('flex items-center gap-1.5 text-[13px] text-muted-foreground', className)}>
	<GitBranch class="h-3.5 w-3.5 shrink-0" />
	<span>{label}</span>
	<a href={resolve('/[namespace]/[name]', { namespace, name })} class="font-medium text-foreground hover:text-primary transition-colors truncate">
		{namespace}/{name}
	</a>
</div>

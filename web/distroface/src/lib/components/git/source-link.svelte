<script lang="ts">
	import { resolve } from '$app/paths';
	import { GitBranch } from '$lib/icons';
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

<div class={cn('flex items-center gap-1.5 text-sm text-muted-foreground', className)}>
	<GitBranch class="size-3.5 shrink-0" />
	<span>{label}</span>
	<a
		href={resolve('/[namespace]/[name]', { namespace, name })}
		class="truncate font-medium text-foreground transition-colors hover:text-primary"
	>
		{namespace}/{name}
	</a>
</div>

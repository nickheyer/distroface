<script lang="ts">
	import { resolve } from '$app/paths';
	import { Check, Copy } from '@lucide/svelte';
	import { cn } from '$lib/utils';

	let {
		sha,
		namespace,
		name,
		copy = true,
		length = 7,
		class: className
	}: {
		sha: string;
		namespace: string;
		name: string;
		copy?: boolean;
		length?: number;
		class?: string;
	} = $props();

	let copied = $state(false);
	let timer: ReturnType<typeof setTimeout> | undefined;

	function copySha(e: MouseEvent) {
		e.preventDefault();
		e.stopPropagation();
		navigator.clipboard.writeText(sha).then(() => {
			copied = true;
			clearTimeout(timer);
			timer = setTimeout(() => (copied = false), 1500);
		});
	}
</script>

<span
	class={cn(
		'inline-flex h-5 items-center overflow-hidden rounded-md border bg-muted font-mono text-xs',
		className
	)}
>
	<a
		href={resolve('/[namespace]/[name]/commit/[sha]', { namespace, name, sha })}
		class="px-1.5 text-muted-foreground transition-colors hover:text-primary"
		title={sha}
	>
		{sha.slice(0, length)}
	</a>
	{#if copy}
		<button
			type="button"
			class="border-l px-1 text-muted-foreground transition-colors hover:text-foreground"
			onclick={copySha}
			aria-label="Copy full sha"
		>
			{#if copied}
				<Check class="size-3 text-success" />
			{:else}
				<Copy class="size-3" />
			{/if}
		</button>
	{/if}
</span>

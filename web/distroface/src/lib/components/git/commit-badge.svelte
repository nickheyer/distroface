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

<span class={cn('inline-flex items-center rounded-md border border-border/60 bg-muted/40 font-mono text-[11px] h-5 overflow-hidden', className)}>
	<a href={resolve('/[namespace]/[name]/commit/[sha]', { namespace, name, sha })} class="px-1.5 text-foreground/80 hover:text-primary hover:bg-muted transition-colors" title={sha}>
		{sha.slice(0, length)}
	</a>
	{#if copy}
		<button type="button" class="px-1 border-l border-border/60 text-muted-foreground hover:text-foreground hover:bg-muted transition-colors" onclick={copySha} aria-label="Copy full sha">
			{#if copied}
				<Check class="h-3 w-3 text-success" />
			{:else}
				<Copy class="h-3 w-3" />
			{/if}
		</button>
	{/if}
</span>

<script lang="ts">
	import { resolve } from '$app/paths';

	let {
		namespace,
		name,
		ref,
		path = '',
		leafIsFile = false
	}: {
		namespace: string;
		name: string;
		ref: string;
		path?: string;
		leafIsFile?: boolean;
	} = $props();

	const encodedRef = $derived(encodeURIComponent(ref));
	const segments = $derived(path ? path.split('/').filter(Boolean) : []);

	function pathAt(i: number): string {
		return segments.slice(0, i + 1).join('/');
	}
</script>

<nav class="flex min-w-0 flex-wrap items-center gap-1 text-sm break-all">
	{#if ref}
		<a
			href={resolve('/[namespace]/[name]/tree/[ref]/[...path]', {
				namespace,
				name,
				ref: encodedRef,
				path: ''
			})}
			class="link font-semibold">{name}</a
		>
	{:else}
		<a href={resolve('/[namespace]/[name]', { namespace, name })} class="link font-semibold"
			>{name}</a
		>
	{/if}
	{#each segments as seg, i (i)}
		<span class="text-muted-foreground/50">/</span>
		{#if i === segments.length - 1}
			<span class="font-semibold {leafIsFile ? '' : 'text-foreground'}">{seg}</span>
		{:else}
			<a
				href={resolve('/[namespace]/[name]/tree/[ref]/[...path]', {
					namespace,
					name,
					ref: encodedRef,
					path: pathAt(i)
				})}
				class="link">{seg}</a
			>
		{/if}
	{/each}
</nav>

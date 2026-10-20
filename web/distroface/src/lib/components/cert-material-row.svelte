<script lang="ts">
	import { Button } from '$lib/components/ui/button';
	import { Loader2 } from '@lucide/svelte';
	import { certDate } from '$lib/cert-utils';
	import type { TLSMaterialInfo } from '$lib/proto/distroface/v1/certificate_pb';

	let {
		title,
		empty,
		material = null,
		busy = false,
		error = '',
		issueLabel = '',
		onIssue,
		onGenerate,
		onUpload,
		onDownload,
		onRemove
	}: {
		title: string;
		empty: string;
		material?: TLSMaterialInfo | null;
		busy?: boolean;
		error?: string;
		issueLabel?: string;
		onIssue?: () => void;
		onGenerate?: () => void;
		onUpload: () => void;
		onDownload?: () => void;
		onRemove: () => void;
	} = $props();
</script>

<div class="flex items-center justify-between gap-4 rounded-lg border px-4 py-3">
	<div class="min-w-0">
		<p class="text-sm font-medium">{title}</p>
		<p class="mt-0.5 truncate text-sm text-muted-foreground">
			{#if material}
				<span class="font-mono">{material.subject}</span>
				{#if material.sans.length}
					<span>({material.sans.join(', ')})</span>
				{/if}
				<span>&middot; expires {certDate(material)}</span>
			{:else}
				{empty}
			{/if}
		</p>
		{#if error}
			<p class="mt-0.5 text-xs text-destructive" role="alert">{error}</p>
		{/if}
	</div>
	<div class="flex shrink-0 items-center gap-1.5">
		{#if busy}
			<Loader2 class="size-4 animate-spin text-muted-foreground" />
		{/if}
		{#if material}
			{#if onDownload}
				<Button variant="outline" size="sm" onclick={onDownload}>Download</Button>
			{/if}
			<Button variant="outline" size="sm" disabled={busy} onclick={onUpload}>Replace</Button>
			<Button variant="outline-destructive" size="sm" disabled={busy} onclick={onRemove}
				>Remove</Button
			>
		{:else}
			{#if onIssue}
				<Button variant="outline" size="sm" disabled={busy} onclick={onIssue}>{issueLabel}</Button>
			{/if}
			{#if onGenerate}
				<Button variant="outline" size="sm" disabled={busy} onclick={onGenerate}>Generate</Button>
			{/if}
			<Button variant="outline" size="sm" disabled={busy} onclick={onUpload}>Upload</Button>
		{/if}
	</div>
</div>

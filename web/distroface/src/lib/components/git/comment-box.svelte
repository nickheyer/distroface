<script lang="ts">
	import type { Snippet } from 'svelte';
	import { Bold, Italic, Code, Link, List, ListChecks, Quote, Loader2 } from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import Markdown from '$lib/components/git/markdown.svelte';
	import RefSuggest from '$lib/components/git/ref-suggest.svelte';
	import { cn } from '$lib/utils';

	let {
		value = $bindable(''),
		placeholder = 'Leave a comment',
		submitLabel = 'Comment',
		submitting = false,
		disabled = false,
		onSubmit,
		extra,
		autofocus = false,
		hideSubmit = false
	}: {
		value: string;
		placeholder?: string;
		submitLabel?: string;
		submitting?: boolean;
		disabled?: boolean;
		onSubmit: () => void | Promise<void>;
		extra?: Snippet;
		autofocus?: boolean;
		// Host form owns the primary button
		hideSubmit?: boolean;
	} = $props();

	let tab = $state<'write' | 'preview'>('write');
	let textarea = $state<HTMLTextAreaElement | null>(null);

	const canSubmit = $derived(!disabled && !submitting && value.trim() !== '');

	function wrap(before: string, after = before, fallback = 'text') {
		const el = textarea;
		if (!el) return;
		const start = el.selectionStart;
		const end = el.selectionEnd;
		const selected = value.slice(start, end) || fallback;
		value = value.slice(0, start) + before + selected + after + value.slice(end);
		const cursor = start + before.length;
		queueMicrotask(() => {
			el.focus();
			el.setSelectionRange(cursor, cursor + selected.length);
		});
	}

	function prefixLines(prefix: string) {
		const el = textarea;
		if (!el) return;
		const start = el.selectionStart;
		const end = el.selectionEnd;
		const lineStart = value.lastIndexOf('\n', start - 1) + 1;
		const searchFrom = end > start && value[end - 1] === '\n' ? end - 1 : end;
		const nl = value.indexOf('\n', searchFrom);
		const lineEnd = nl === -1 ? value.length : nl;
		const chunk = value.slice(lineStart, lineEnd);
		const lines = chunk
			.split('\n')
			.map((l) => (l.startsWith(prefix) ? l.slice(prefix.length) : prefix + l));
		const next = lines.join('\n');
		value = value.slice(0, lineStart) + next + value.slice(lineEnd);
		queueMicrotask(() => {
			el.focus();
			el.setSelectionRange(lineStart, lineStart + next.length);
		});
	}

	function onKeydown(e: KeyboardEvent) {
		if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
			e.preventDefault();
			if (canSubmit) void onSubmit();
		}
	}

	$effect(() => {
		if (autofocus && textarea) textarea.focus();
	});

	const tools = [
		{ icon: Bold, title: 'Bold', run: () => wrap('**') },
		{ icon: Italic, title: 'Italic', run: () => wrap('_') },
		{ icon: Code, title: 'Code', run: () => wrap('`', '`', 'code') },
		{ icon: Link, title: 'Link', run: () => wrap('[', '](url)') },
		{ icon: List, title: 'List', run: () => prefixLines('- ') },
		{ icon: ListChecks, title: 'Task list', run: () => prefixLines('- [ ] ') },
		{ icon: Quote, title: 'Quote', run: () => prefixLines('> ') }
	];
</script>

<div class={cn('box', disabled && 'opacity-60')}>
	<div class="flex flex-wrap items-center gap-1 border-b bg-muted/40 px-2">
		<div class="flex">
			<button
				type="button"
				class="-mb-px border-b-2 px-3 py-2 text-sm font-medium transition-colors {tab === 'write'
					? 'border-primary text-foreground'
					: 'border-transparent text-muted-foreground hover:text-foreground'}"
				onclick={() => (tab = 'write')}
			>
				Write
			</button>
			<button
				type="button"
				class="-mb-px border-b-2 px-3 py-2 text-sm font-medium transition-colors {tab === 'preview'
					? 'border-primary text-foreground'
					: 'border-transparent text-muted-foreground hover:text-foreground'}"
				onclick={() => (tab = 'preview')}
			>
				Preview
			</button>
		</div>
		{#if tab === 'write'}
			<div class="ml-auto flex items-center gap-0.5">
				{#each tools as tool (tool.title)}
					{@const Icon = tool.icon}
					<Button
						variant="ghost-muted"
						size="icon-sm"
						title={tool.title}
						aria-label={tool.title}
						onclick={tool.run}
						{disabled}
					>
						<Icon />
					</Button>
				{/each}
			</div>
		{/if}
	</div>

	{#if tab === 'write'}
		<textarea
			bind:this={textarea}
			bind:value
			{placeholder}
			{disabled}
			rows={6}
			onkeydown={onKeydown}
			class="min-h-36 w-full resize-y bg-transparent px-3 py-2.5 font-mono text-sm leading-relaxed outline-none"
		></textarea>
		<RefSuggest {textarea} bind:value />
	{:else}
		<div class="min-h-36 px-4 py-3 text-sm">
			{#if value.trim()}
				<Markdown source={value} />
			{:else}
				<p class="text-muted-foreground italic">Nothing to preview</p>
			{/if}
		</div>
	{/if}

	<div class="flex flex-wrap items-center gap-2 border-t bg-muted/30 px-3 py-2">
		<span class="hidden text-xs text-muted-foreground sm:inline"
			>Markdown supported, Ctrl+Enter submits</span
		>
		<div class="ml-auto flex flex-wrap items-center justify-end gap-2">
			{#if extra}
				{@render extra()}
			{/if}
			{#if !hideSubmit}
				<Button onclick={() => void onSubmit()} disabled={!canSubmit}>
					{#if submitting}
						<Loader2 class="animate-spin" />
					{/if}
					{submitting ? 'Saving' : submitLabel}
				</Button>
			{/if}
		</div>
	</div>
</div>

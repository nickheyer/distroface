<script lang="ts">
	import type { Snippet } from 'svelte';
	import { Bold, Italic, Code, Link, List, ListChecks, Quote } from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import Markdown from '$lib/components/git/markdown.svelte';
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
		const lines = chunk.split('\n').map((l) => (l.startsWith(prefix) ? l.slice(prefix.length) : prefix + l));
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

<div class={cn('rounded-xl border border-border/60 bg-card overflow-hidden', disabled && 'opacity-60')}>
	<div class="flex flex-wrap items-center gap-1 border-b border-border/50 bg-muted/20 px-2">
		<div class="flex">
			<button
				type="button"
				class="px-3 py-2 text-[12.5px] font-medium border-b-2 -mb-px transition-colors {tab === 'write' ? 'border-primary text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'}"
				onclick={() => (tab = 'write')}
			>
				Write
			</button>
			<button
				type="button"
				class="px-3 py-2 text-[12.5px] font-medium border-b-2 -mb-px transition-colors {tab === 'preview' ? 'border-primary text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'}"
				onclick={() => (tab = 'preview')}
			>
				Preview
			</button>
		</div>
		{#if tab === 'write'}
			<div class="ml-auto flex items-center gap-0.5">
				{#each tools as tool (tool.title)}
					{@const Icon = tool.icon}
					<button
						type="button"
						class="h-7 w-7 rounded-md inline-flex items-center justify-center text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
						title={tool.title}
						aria-label={tool.title}
						onclick={tool.run}
						{disabled}
					>
						<Icon class="h-3.5 w-3.5" />
					</button>
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
			class="w-full min-h-[9rem] resize-y bg-transparent px-3 py-2.5 text-sm outline-none placeholder:text-muted-foreground font-mono leading-relaxed"
		></textarea>
	{:else}
		<div class="px-4 py-3 min-h-[9rem] text-sm">
			{#if value.trim()}
				<Markdown source={value} />
			{:else}
				<p class="text-muted-foreground italic">Nothing to preview</p>
			{/if}
		</div>
	{/if}

	<div class="flex flex-wrap items-center gap-2 border-t border-border/50 bg-muted/10 px-3 py-2">
		<span class="text-[11.5px] text-muted-foreground hidden sm:inline">Markdown supported, Ctrl+Enter submits</span>
		<div class="ml-auto flex flex-wrap items-center justify-end gap-2">
			{#if extra}
				{@render extra()}
			{/if}
			{#if !hideSubmit}
				<Button size="sm" class="h-8" onclick={() => void onSubmit()} disabled={!canSubmit}>
					{submitting ? 'Saving...' : submitLabel}
				</Button>
			{/if}
		</div>
	</div>
</div>

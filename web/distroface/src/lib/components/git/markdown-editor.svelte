<script lang="ts">
	import { tick } from 'svelte';
	import {
		Bold,
		Italic,
		Strikethrough,
		Code,
		Braces,
		Quote,
		List,
		ListOrdered,
		ListChecks,
		Link,
		Image,
		Table,
		Minus,
		Heading as Heading2,
		CircleHelp,
		Hash,
		BookOpen,
		Eye,
		Pencil,
		Plus,
		Search,
		Loader
	} from '$lib/icons';
	import { Popover, PopoverContent, PopoverTrigger } from '$lib/components/ui/popover';
	import { Button } from '$lib/components/ui/button';
	import Markdown from '$lib/components/git/markdown.svelte';
	import RefSuggest from '$lib/components/git/ref-suggest.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { IssueState, type Issue } from '$lib/proto/distroface/v1/issue_pb';
	import type { WikiPage } from '$lib/proto/distroface/v1/wiki_pb';
	import { cn } from '$lib/utils';

	let {
		value = $bindable(''),
		namespace,
		name,
		placeholder = 'Write in markdown',
		wikiBase,
		rawBase,
		id,
		class: className
	}: {
		value: string;
		namespace: string;
		name: string;
		placeholder?: string;
		// Set on wiki pages to enable [[Page]] links and the page picker
		wikiBase?: string;
		rawBase?: string;
		id?: string;
		class?: string;
	} = $props();

	let tab = $state<'write' | 'preview'>('write');
	let textarea = $state<HTMLTextAreaElement | null>(null);

	async function place(start: number, end = start) {
		await tick();
		const el = textarea;
		if (!el) return;
		el.focus();
		el.setSelectionRange(start, end);
	}

	// Wraps the selection, or a placeholder word, in markers
	function wrap(before: string, after = before, fallback = 'text') {
		const el = textarea;
		if (!el) return;
		const start = el.selectionStart;
		const end = el.selectionEnd;
		const selected = value.slice(start, end) || fallback;
		value = value.slice(0, start) + before + selected + after + value.slice(end);
		const cursor = start + before.length;
		void place(cursor, cursor + selected.length);
	}

	// Toggles a prefix on every selected line
	function prefixLines(prefix: string, numbered = false) {
		const el = textarea;
		if (!el) return;
		const start = el.selectionStart;
		const end = el.selectionEnd;
		const lineStart = value.lastIndexOf('\n', start - 1) + 1;
		const searchFrom = end > start && value[end - 1] === '\n' ? end - 1 : end;
		const nl = value.indexOf('\n', searchFrom);
		const lineEnd = nl === -1 ? value.length : nl;
		const chunk = value.slice(lineStart, lineEnd);
		const lines = chunk.split('\n').map((l, i) => {
			const p = numbered ? `${i + 1}. ` : prefix;
			const stripped = numbered
				? l.replace(/^\d+[.)]\s+/, '')
				: l.startsWith(prefix)
					? l.slice(prefix.length)
					: null;
			if (stripped !== null && (!numbered || stripped !== l)) return stripped;
			return p + l;
		});
		const next = lines.join('\n');
		value = value.slice(0, lineStart) + next + value.slice(lineEnd);
		void place(lineStart, lineStart + next.length);
	}

	// Drops a block on its own lines at the cursor
	function insertBlock(text: string, selectFrom?: number, selectTo?: number) {
		const el = textarea;
		if (!el) return;
		const start = el.selectionStart;
		const end = el.selectionEnd;
		const needsLeadingBreak = start > 0 && value[start - 1] !== '\n';
		const lead = needsLeadingBreak ? '\n\n' : start > 0 && value[start - 2] !== '\n' ? '\n' : '';
		const tail = end < value.length && value[end] !== '\n' ? '\n\n' : '\n';
		const block = lead + text + tail;
		value = value.slice(0, start) + block + value.slice(end);
		const base = start + lead.length;
		if (selectFrom !== undefined) void place(base + selectFrom, base + (selectTo ?? selectFrom));
		else void place(start + block.length);
	}

	// Inline text at the cursor, replacing a selection
	function insertInline(text: string) {
		const el = textarea;
		if (!el) return;
		const start = el.selectionStart;
		const end = el.selectionEnd;
		value = value.slice(0, start) + text + value.slice(end);
		void place(start + text.length);
	}

	const codeBlock = '```\ncode\n```';
	const tableBlock = '| Column | Column |\n| --- | --- |\n| Cell | Cell |';

	const tools = [
		{ icon: Heading2, title: 'Heading', run: () => prefixLines('## ') },
		{ icon: Bold, title: 'Bold (Ctrl+B)', run: () => wrap('**') },
		{ icon: Italic, title: 'Italic (Ctrl+I)', run: () => wrap('_') },
		{ icon: Strikethrough, title: 'Strikethrough', run: () => wrap('~~') },
		{ icon: Code, title: 'Inline code', run: () => wrap('`', '`', 'code') },
		{ icon: Braces, title: 'Code block', run: () => insertBlock(codeBlock, 4, 8) },
		{ icon: Quote, title: 'Quote', run: () => prefixLines('> ') },
		{ icon: List, title: 'Bulleted list', run: () => prefixLines('- ') },
		{ icon: ListOrdered, title: 'Numbered list', run: () => prefixLines('', true) },
		{ icon: ListChecks, title: 'Task list', run: () => prefixLines('- [ ] ') },
		{ icon: Link, title: 'Link (Ctrl+K)', run: () => wrap('[', '](url)') },
		{ icon: Image, title: 'Image', run: () => wrap('![', '](image-url)', 'alt text') },
		{ icon: Table, title: 'Table', run: () => insertBlock(tableBlock, 2, 8) },
		{ icon: Minus, title: 'Horizontal rule', run: () => insertBlock('---') }
	];

	// Enter continues bullets, numbers, and task boxes
	function continueList(e: KeyboardEvent): boolean {
		const el = textarea;
		if (!el || el.selectionStart !== el.selectionEnd) return false;
		const start = el.selectionStart;
		const lineStart = value.lastIndexOf('\n', start - 1) + 1;
		const line = value.slice(lineStart, start);
		const m = /^(\s*)([-*+]|\d+[.)])(\s+\[[ xX]\]\s+|\s+)(.*)$/.exec(line);
		if (!m) return false;
		e.preventDefault();
		if (!m[4].trim()) {
			value = value.slice(0, lineStart) + value.slice(start);
			void place(lineStart);
			return true;
		}
		let marker = m[2];
		const n = parseInt(marker, 10);
		if (!Number.isNaN(n)) marker = `${n + 1}${marker.endsWith(')') ? ')' : '.'}`;
		const box = /\[[ xX]\]/.test(m[3]) ? ' [ ] ' : ' ';
		const insert = `\n${m[1]}${marker}${box}`;
		value = value.slice(0, start) + insert + value.slice(start);
		void place(start + insert.length);
		return true;
	}

	function onKeydown(e: KeyboardEvent) {
		const mod = e.metaKey || e.ctrlKey;
		if (mod && !e.shiftKey && !e.altKey) {
			const k = e.key.toLowerCase();
			if (k === 'b') {
				e.preventDefault();
				wrap('**');
				return;
			}
			if (k === 'i') {
				e.preventDefault();
				wrap('_');
				return;
			}
			if (k === 'k') {
				e.preventDefault();
				wrap('[', '](url)');
				return;
			}
		}
		if (e.key === 'Enter' && !mod && !e.shiftKey && !e.altKey) continueList(e);
	}

	// Wiki page picker
	let pageOpen = $state(false);
	let pageQuery = $state('');
	let pages = $state<WikiPage[]>([]);
	let pagesLoaded = $state(false);
	let pagesLoading = $state(false);

	async function loadPages() {
		if (pagesLoaded || pagesLoading) return;
		pagesLoading = true;
		try {
			const resp = await rpcClient.wiki.listWikiPages(
				{
					namespace,
					name,
					page: { pageSize: 500, pageToken: '', orderBy: '', query: { text: '', filters: [] } }
				},
				silentCallOptions
			);
			pages = resp.pages.filter((p) => !p.title.startsWith('_'));
			pagesLoaded = true;
		} catch {
			pages = [];
		} finally {
			pagesLoading = false;
		}
	}

	$effect(() => {
		if (pageOpen) void loadPages();
	});

	const pageMatches = $derived.by(() => {
		const q = pageQuery.trim().toLowerCase();
		return pages.filter((p) => !q || p.title.toLowerCase().includes(q)).slice(0, 50);
	});

	function pickPage(title: string) {
		pageOpen = false;
		pageQuery = '';
		insertInline(`[[${title}]]`);
	}

	// Issue picker
	let issueOpen = $state(false);
	let issueQuery = $state('');
	let issues = $state<Issue[]>([]);
	let issuesLoading = $state(false);
	let issueDebounce: ReturnType<typeof setTimeout> | undefined;

	async function searchIssues() {
		issuesLoading = true;
		try {
			const resp = await rpcClient.issue.listIssues(
				{
					namespace,
					name,
					state: IssueState.OPEN,
					page: {
						pageSize: 10,
						pageToken: '',
						orderBy: '',
						query: { text: issueQuery.trim(), filters: [] }
					}
				},
				silentCallOptions
			);
			issues = resp.issues;
		} catch {
			issues = [];
		} finally {
			issuesLoading = false;
		}
	}

	function onIssueQuery() {
		clearTimeout(issueDebounce);
		issueDebounce = setTimeout(searchIssues, 200);
	}

	$effect(() => {
		if (issueOpen) void searchIssues();
	});

	function pickIssue(ref: string) {
		issueOpen = false;
		issueQuery = '';
		insertInline(ref);
	}

	const helpRows: [string, string][] = [
		['# Heading', 'Headings, one to six hashes'],
		['**bold**  _italic_  ~~struck~~', 'Emphasis'],
		['`code`', 'Inline code'],
		['```lang', 'Fenced code block, close with three backticks'],
		['> quote', 'Block quote'],
		['- item  1. item  - [ ] task', 'Bulleted, numbered, and task lists'],
		['[text](https://example.com)', 'Link'],
		['![alt](https://example.com/a.png)', 'Image from a url'],
		['| a | b |', 'Table, second row is | --- | --- |'],
		['#12  ns/name#12', 'Links to an issue or pull request'],
		['@user', 'Links to a profile']
	];
	const wikiHelpRows: [string, string][] = [
		['[[Page Title]]', 'Links to another page of this wiki'],
		['[[Shown text|Page Title]]', 'Wiki link with its own text'],
		['![alt](diagram.png)', 'Image committed to the wiki repository, relative to its root']
	];
</script>

<div class={cn('box', className)}>
	<div class="flex flex-wrap items-center gap-1 border-b bg-muted/40 px-2">
		<div class="flex">
			<button
				type="button"
				class="-mb-px inline-flex items-center gap-1.5 border-b-2 px-3 py-2 text-sm font-medium transition-colors {tab ===
				'write'
					? 'border-primary text-foreground'
					: 'border-transparent text-muted-foreground hover:text-foreground'}"
				onclick={() => (tab = 'write')}
			>
				<Pencil class="size-3.5" />Write
			</button>
			<button
				type="button"
				class="-mb-px inline-flex items-center gap-1.5 border-b-2 px-3 py-2 text-sm font-medium transition-colors {tab ===
				'preview'
					? 'border-primary text-foreground'
					: 'border-transparent text-muted-foreground hover:text-foreground'}"
				onclick={() => (tab = 'preview')}
			>
				<Eye class="size-3.5" />Preview
			</button>
		</div>
		{#if tab === 'write'}
			<div class="ml-auto flex flex-wrap items-center gap-0.5 py-1">
				{#each tools as tool (tool.title)}
					{@const Icon = tool.icon}
					<Button
						variant="ghost-muted"
						size="icon-sm"
						title={tool.title}
						aria-label={tool.title}
						onclick={tool.run}
					>
						<Icon />
					</Button>
				{/each}
				<span class="mx-1 h-4 w-px bg-border" aria-hidden="true"></span>
				{#if wikiBase}
					<Popover bind:open={pageOpen}>
						<PopoverTrigger>
							{#snippet child({ props })}
								<Button {...props} variant="ghost-muted" size="sm" title="Link to a wiki page">
									<BookOpen />Page
								</Button>
							{/snippet}
						</PopoverTrigger>
						<PopoverContent class="w-[min(20rem,calc(100vw-2rem))] p-0" align="end">
							<div class="flex items-center gap-2 border-b px-2.5 py-2">
								<Search class="size-3.5 shrink-0 text-muted-foreground" />
								<input
									class="flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
									placeholder="Find a page"
									bind:value={pageQuery}
								/>
								{#if pagesLoading}<Loader
										class="size-3.5 animate-spin text-muted-foreground"
									/>{/if}
							</div>
							<div class="max-h-64 overflow-y-auto py-1">
								{#if pagesLoaded && pageMatches.length === 0}
									<p class="px-3 py-4 text-center text-sm text-muted-foreground">
										{pages.length ? 'No pages match' : 'No pages yet'}
									</p>
								{/if}
								{#each pageMatches as p (p.title)}
									<button
										type="button"
										class="flex w-full items-center gap-2 px-2.5 py-1.5 text-left text-sm transition-colors hover:bg-muted"
										onclick={() => pickPage(p.title)}
									>
										<BookOpen class="size-3.5 shrink-0 text-muted-foreground" />
										<span class="truncate">{p.title}</span>
									</button>
								{/each}
								{#if pageQuery.trim() && !pages.some((p) => p.title.toLowerCase() === pageQuery
												.trim()
												.toLowerCase())}
									<button
										type="button"
										class="flex w-full items-center gap-2 px-2.5 py-1.5 text-left text-sm text-muted-foreground transition-colors hover:bg-muted"
										onclick={() => pickPage(pageQuery.trim())}
									>
										<Plus class="size-3.5 shrink-0" />
										<span class="truncate">Link to new page "{pageQuery.trim()}"</span>
									</button>
								{/if}
							</div>
						</PopoverContent>
					</Popover>
				{/if}
				<Popover bind:open={issueOpen}>
					<PopoverTrigger>
						{#snippet child({ props })}
							<Button
								{...props}
								variant="ghost-muted"
								size="sm"
								title="Link to an issue or pull request"
							>
								<Hash />Issue
							</Button>
						{/snippet}
					</PopoverTrigger>
					<PopoverContent class="w-[min(20rem,calc(100vw-2rem))] p-0" align="end">
						<div class="flex items-center gap-2 border-b px-2.5 py-2">
							<Search class="size-3.5 shrink-0 text-muted-foreground" />
							<input
								class="flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
								placeholder="Search open issues or type #12"
								bind:value={issueQuery}
								oninput={onIssueQuery}
								onkeydown={(e) => {
									if (e.key === 'Enter' && /^(\S+#)?\d+$/.test(issueQuery.trim()))
										pickIssue(
											issueQuery.trim().startsWith('#') || issueQuery.includes('#')
												? issueQuery.trim()
												: `#${issueQuery.trim()}`
										);
								}}
							/>
							{#if issuesLoading}<Loader class="size-3.5 animate-spin text-muted-foreground" />{/if}
						</div>
						<div class="max-h-64 overflow-y-auto py-1">
							{#if !issuesLoading && issues.length === 0}
								<p class="px-3 py-4 text-center text-sm text-muted-foreground">
									No open issues match, type #12 or ns/name#12 and press Enter
								</p>
							{/if}
							{#each issues as i (i.id)}
								<button
									type="button"
									class="flex w-full items-center gap-2 px-2.5 py-1.5 text-left text-sm transition-colors hover:bg-muted"
									onclick={() => pickIssue(`#${i.number}`)}
								>
									<span class="shrink-0 text-muted-foreground">#{i.number}</span>
									<span class="truncate">{i.title}</span>
								</button>
							{/each}
						</div>
					</PopoverContent>
				</Popover>
				<Popover>
					<PopoverTrigger>
						{#snippet child({ props })}
							<Button
								{...props}
								variant="ghost-muted"
								size="icon-sm"
								title="Formatting help"
								aria-label="Formatting help"
							>
								<CircleHelp />
							</Button>
						{/snippet}
					</PopoverTrigger>
					<PopoverContent class="w-[min(26rem,calc(100vw-2rem))] p-0" align="end">
						<div class="border-b px-3 py-2 text-sm font-semibold">Formatting</div>
						<div class="max-h-80 overflow-y-auto">
							<table class="w-full text-xs">
								<tbody class="divide-y">
									{#each helpRows as [syntax, meaning] (syntax)}
										<tr>
											<td class="px-3 py-1.5 align-top font-mono whitespace-pre text-foreground"
												>{syntax}</td
											>
											<td class="px-3 py-1.5 align-top text-muted-foreground">{meaning}</td>
										</tr>
									{/each}
									{#if wikiBase}
										{#each wikiHelpRows as [syntax, meaning] (syntax)}
											<tr>
												<td class="px-3 py-1.5 align-top font-mono whitespace-pre text-foreground"
													>{syntax}</td
												>
												<td class="px-3 py-1.5 align-top text-muted-foreground">{meaning}</td>
											</tr>
										{/each}
									{/if}
								</tbody>
							</table>
						</div>
						{#if wikiBase}
							<p class="border-t px-3 py-2 text-xs leading-snug text-muted-foreground">
								Images and attachments live in the wiki repository. Clone it, commit the files, and
								reference them by relative path.
							</p>
						{/if}
					</PopoverContent>
				</Popover>
			</div>
		{/if}
	</div>

	{#if tab === 'write'}
		<textarea
			bind:this={textarea}
			bind:value
			{id}
			{placeholder}
			spellcheck="true"
			onkeydown={onKeydown}
			class="max-h-[75vh] min-h-112 w-full resize-y bg-transparent px-5 py-4 text-sm leading-7 outline-none placeholder:text-muted-foreground"
		></textarea>
		<RefSuggest {textarea} bind:value {namespace} {name} />
	{:else}
		<div class="min-h-112 min-w-0 overflow-x-auto px-5 py-4 text-sm">
			{#if value.trim()}
				<Markdown source={value} baseHref={wikiBase} {wikiBase} {rawBase} />
			{:else}
				<p class="text-sm text-muted-foreground">Nothing to preview yet</p>
			{/if}
		</div>
	{/if}
</div>

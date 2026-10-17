<script lang="ts">
	import CodeMirror from 'svelte-codemirror-editor';
	import { oneDark } from '@codemirror/theme-one-dark';
	import { languages } from '@codemirror/language-data';
	import { LanguageDescription, type LanguageSupport } from '@codemirror/language';
	import { mode } from 'mode-watcher';

	let {
		value = $bindable(''),
		filename = '',
		readonly = false,
		placeholder = ''
	}: {
		value: string;
		filename?: string;
		readonly?: boolean;
		placeholder?: string;
	} = $props();

	const theme = $derived(mode.current === 'dark' ? oneDark : undefined);
	let lang = $state<LanguageSupport | null>(null);

	// Grammar loads lazily from the file name
	$effect(() => {
		const base = filename.split('/').pop() ?? '';
		const desc = base ? LanguageDescription.matchFilename(languages, base) : null;
		if (!desc) {
			lang = null;
			return;
		}
		let cancelled = false;
		void desc.load().then((support) => {
			if (!cancelled) lang = support;
		});
		return () => {
			cancelled = true;
		};
	});
</script>

<div class="rounded-xl border border-border/60 overflow-hidden text-sm [&_.cm-editor]:min-h-96 [&_.cm-editor]:max-h-[70vh] [&_.cm-scroller]:overflow-auto [&_.cm-editor.cm-focused]:outline-none">
	<CodeMirror bind:value {lang} {theme} {readonly} {placeholder} lineWrapping tabSize={4} useTab />
</div>

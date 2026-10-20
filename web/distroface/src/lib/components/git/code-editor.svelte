<script lang="ts">
	import CodeMirror, { type CodeMirrorProps } from 'svelte-codemirror-editor';
	import { oneDark } from '@codemirror/theme-one-dark';
	import { languages } from '@codemirror/language-data';
	import { LanguageDescription, type LanguageSupport } from '@codemirror/language';
	import { mode } from 'mode-watcher';

	type EditorView = Parameters<NonNullable<CodeMirrorProps['onready']>>[0];

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
	let view = $state<EditorView | null>(null);

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

	// Wrapper drops value changes landing in its mount flush
	$effect(() => {
		if (!view) return;
		const doc = view.state.doc.toString();
		if (doc !== value) view.dispatch({ changes: { from: 0, to: doc.length, insert: value } });
	});
</script>

<div
	class="overflow-hidden rounded-lg border text-sm [&_.cm-editor]:max-h-[70vh] [&_.cm-editor]:min-h-96 [&_.cm-editor.cm-focused]:outline-none [&_.cm-scroller]:overflow-auto"
>
	<CodeMirror
		bind:value
		{lang}
		{theme}
		{readonly}
		{placeholder}
		lineWrapping
		tabSize={4}
		useTab
		onready={(v) => (view = v)}
	/>
</div>

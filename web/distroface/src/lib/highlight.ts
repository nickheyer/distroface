import type { BundledLanguage, GrammarState, Highlighter } from 'shiki';
import '$lib/components/git/code.css';

export const THEMES = { light: 'github-light', dark: 'github-dark' } as const;

let highlighterPromise: Promise<Highlighter> | null = null;
let shikiModule: Promise<typeof import('shiki')> | null = null;
const loadedLangs = new Set<string>();

function shiki() {
	if (!shikiModule) shikiModule = import('shiki');
	return shikiModule;
}

// Lazy singleton, grammars load on first use per language
async function getHighlighter(): Promise<Highlighter> {
	if (!highlighterPromise) {
		highlighterPromise = shiki().then((s) =>
			s.createHighlighter({
				themes: [THEMES.light, THEMES.dark],
				langs: [],
				engine: s.createJavaScriptRegexEngine({ forgiving: true })
			})
		);
	}
	return highlighterPromise;
}

const EXTENSION_LANGS: Record<string, string> = {
	js: 'javascript',
	mjs: 'javascript',
	cjs: 'javascript',
	jsx: 'jsx',
	ts: 'typescript',
	mts: 'typescript',
	cts: 'typescript',
	tsx: 'tsx',
	go: 'go',
	mod: 'go-mod',
	sum: 'go-sum',
	py: 'python',
	rb: 'ruby',
	rs: 'rust',
	java: 'java',
	kt: 'kotlin',
	kts: 'kotlin',
	swift: 'swift',
	c: 'c',
	h: 'c',
	cc: 'cpp',
	cpp: 'cpp',
	cxx: 'cpp',
	hpp: 'cpp',
	hh: 'cpp',
	cs: 'csharp',
	php: 'php',
	sh: 'bash',
	bash: 'bash',
	zsh: 'bash',
	fish: 'fish',
	ps1: 'powershell',
	json: 'json',
	jsonc: 'jsonc',
	json5: 'json5',
	yaml: 'yaml',
	yml: 'yaml',
	toml: 'toml',
	ini: 'ini',
	cfg: 'ini',
	conf: 'ini',
	env: 'dotenv',
	md: 'markdown',
	markdown: 'markdown',
	mdx: 'mdx',
	html: 'html',
	htm: 'html',
	xml: 'xml',
	svg: 'xml',
	css: 'css',
	scss: 'scss',
	sass: 'sass',
	less: 'less',
	svelte: 'svelte',
	vue: 'vue',
	astro: 'astro',
	sql: 'sql',
	graphql: 'graphql',
	gql: 'graphql',
	proto: 'proto',
	dockerfile: 'dockerfile',
	makefile: 'makefile',
	mk: 'makefile',
	cmake: 'cmake',
	lua: 'lua',
	pl: 'perl',
	r: 'r',
	scala: 'scala',
	clj: 'clojure',
	ex: 'elixir',
	exs: 'elixir',
	erl: 'erlang',
	hs: 'haskell',
	ml: 'ocaml',
	dart: 'dart',
	zig: 'zig',
	nim: 'nim',
	tf: 'terraform',
	hcl: 'hcl',
	nix: 'nix',
	diff: 'diff',
	patch: 'diff',
	tex: 'latex',
	vim: 'viml',
	bat: 'bat',
	cmd: 'bat',
	groovy: 'groovy',
	gradle: 'groovy',
	jl: 'julia',
	elm: 'elm',
	fs: 'fsharp',
	v: 'verilog',
	wasm: 'wasm',
	wat: 'wasm',
	txt: 'text'
};

const FILENAME_LANGS: Record<string, string> = {
	dockerfile: 'dockerfile',
	containerfile: 'dockerfile',
	makefile: 'makefile',
	gnumakefile: 'makefile',
	cmakelists: 'cmake',
	'go.mod': 'go-mod',
	'go.sum': 'go-sum',
	'.gitignore': 'ini',
	'.dockerignore': 'ini',
	'.env': 'dotenv',
	'.bashrc': 'bash',
	'.zshrc': 'bash',
	'.profile': 'bash',
	'.editorconfig': 'ini',
	jenkinsfile: 'groovy',
	rakefile: 'ruby',
	gemfile: 'ruby',
	procfile: 'ini'
};

// Shiki language id for a path, empty when unknown
export function languageFromPath(path: string): string {
	const file = path.split('/').pop() ?? '';
	const lower = file.toLowerCase();
	if (FILENAME_LANGS[lower]) return FILENAME_LANGS[lower];
	const dot = lower.lastIndexOf('.');
	if (dot < 0) return '';
	const ext = lower.slice(dot + 1);
	if (ext.startsWith('dockerfile') || lower.startsWith('dockerfile.')) return 'dockerfile';
	return EXTENSION_LANGS[ext] ?? ext;
}

export function escapeHtml(text: string): string {
	return text
		.replace(/&/g, '&amp;')
		.replace(/</g, '&lt;')
		.replace(/>/g, '&gt;')
		.replace(/"/g, '&quot;');
}

async function resolveLang(lang: string): Promise<BundledLanguage | null> {
	const key = (lang || '').trim().toLowerCase();
	if (!key || key === 'text' || key === 'plain' || key === 'txt') return null;
	const s = await shiki();
	const mapped = EXTENSION_LANGS[key] ?? key;
	return mapped in s.bundledLanguages ? (mapped as BundledLanguage) : null;
}

async function ensureLang(lang: BundledLanguage): Promise<Highlighter> {
	const hl = await getHighlighter();
	if (!loadedLangs.has(lang)) {
		await hl.loadLanguage(lang);
		loadedLangs.add(lang);
	}
	return hl;
}

// Escaped pre when the language is unknown
export async function highlight(code: string, lang: string): Promise<string> {
	const resolved = await resolveLang(lang);
	if (!resolved) {
		return `<pre class="shiki"><code>${escapeHtml(code)}</code></pre>`;
	}
	try {
		const hl = await ensureLang(resolved);
		return hl.codeToHtml(code, { lang: resolved, themes: THEMES, defaultColor: false });
	} catch {
		return `<pre class="shiki"><code>${escapeHtml(code)}</code></pre>`;
	}
}

// Lines tokenized per turn of the event loop so big files never freeze the page
const CHUNK_LINES = 400;

function renderTokens(line: { htmlStyle?: Record<string, string>; content: string }[]): string {
	let out = '';
	for (const token of line) {
		const style = token.htmlStyle
			? Object.entries(token.htmlStyle)
					.map(([k, v]) => `${k}:${v}`)
					.join(';')
			: '';
		out += style
			? `<span style="${style}">${escapeHtml(token.content)}</span>`
			: escapeHtml(token.content);
	}
	return out;
}

// Per line html so callers own numbering, grammar state carries across chunks
export async function highlightLines(code: string, lang: string): Promise<string[]> {
	const body = code.endsWith('\n') ? code.slice(0, -1) : code;
	const lines = body.split('\n');
	const plain = lines.map(escapeHtml);
	const resolved = await resolveLang(lang);
	if (!resolved || body.length > 1_000_000) return plain;
	try {
		const hl = await ensureLang(resolved);
		const out: string[] = [];
		let grammarState: GrammarState | undefined;
		for (let start = 0; start < lines.length; start += CHUNK_LINES) {
			if (start > 0) await new Promise((r) => setTimeout(r, 0));
			const slice = lines.slice(start, start + CHUNK_LINES).join('\n');
			const result = hl.codeToTokens(slice, {
				lang: resolved,
				themes: THEMES,
				defaultColor: false,
				grammarState
			});
			grammarState = result.grammarState;
			for (const line of result.tokens) out.push(renderTokens(line));
		}
		return out.length === plain.length ? out : plain;
	} catch {
		return plain;
	}
}

import { Marked, type Tokens, type Token, type TokenizerAndRendererExtension } from 'marked';
import DOMPurify from 'dompurify';
import { highlight, escapeHtml } from '$lib/highlight';

export interface MarkdownOptions {
	// Directory of the document for relative links
	baseHref?: string;
	// Raw file directory for images
	rawBase?: string;
	// ns/name that #12, ns/name#12, and @user resolve against
	repo?: string;
	// Wiki page directory that [[Page]] links resolve against
	wikiBase?: string;
}

interface WikiLinkToken extends Tokens.Generic {
	type: 'wikiLink';
	page: string;
	text: string;
}

interface RefLinkToken extends Tokens.Generic {
	type: 'refLink';
	href: string;
	text: string;
	// Hovercard target, rendered as data attributes
	hover: Record<string, string>;
}

// [[Page]] and [[Text|Page]] become links into the wiki
function wikiLinks(wikiBase: string): TokenizerAndRendererExtension {
	return {
		name: 'wikiLink',
		level: 'inline',
		start(src) {
			return src.indexOf('[[');
		},
		tokenizer(src) {
			const m = /^\[\[([^\]|\n]+?)(?:\|([^\]\n]+?))?\]\]/.exec(src);
			if (!m) return undefined;
			const page = m[1].trim();
			if (!page) return undefined;
			const token: WikiLinkToken = {
				type: 'wikiLink',
				raw: m[0],
				page,
				text: (m[2] ?? m[1]).trim()
			};
			return token;
		},
		renderer(token) {
			const t = token as WikiLinkToken;
			return `<a href="${escapeHtml(wikiBase + encodeURIComponent(t.page))}">${escapeHtml(t.text)}</a>`;
		}
	};
}

const REF_START =
	/(?:^|[\s(])(?:#\d|[A-Za-z0-9][\w.-]*\/[A-Za-z0-9][\w.-]*[#@]|@[A-Za-z0-9]|[0-9a-f]{40}\b)/;
const REF_TOKEN =
	/^(?:([A-Za-z0-9][\w.-]*\/[A-Za-z0-9][\w.-]*)?#(\d+)|@([A-Za-z0-9][A-Za-z0-9_.-]*[A-Za-z0-9]|[A-Za-z0-9])|(?:([A-Za-z0-9][\w.-]*\/[A-Za-z0-9][\w.-]*)@)?([0-9a-f]{40}))(?![\w#@/-])/;

// #12, ns/name#12, @user, and full shas autolink the way issue bodies do
function refLinks(repo: string): TokenizerAndRendererExtension {
	return {
		name: 'refLink',
		level: 'inline',
		start(src) {
			const m = REF_START.exec(src);
			if (!m) return -1;
			return m.index + (/^[\s(]/.test(m[0]) ? 1 : 0);
		},
		tokenizer(src) {
			const m = REF_TOKEN.exec(src);
			if (!m) return undefined;
			if (m[3]) {
				const token: RefLinkToken = {
					type: 'refLink',
					raw: m[0],
					href: `/${encodeURIComponent(m[3])}`,
					text: m[0],
					hover: { hover: 'user', user: m[3] }
				};
				return token;
			}
			if (m[5]) {
				const target = m[4] || repo;
				if (!target) return undefined;
				const token: RefLinkToken = {
					type: 'refLink',
					raw: m[0],
					href: `/${target}/commit/${m[5]}`,
					text: `${m[4] ? `${m[4]}@` : ''}${m[5].slice(0, 7)}`,
					hover: { hover: 'commit', repo: target, sha: m[5] }
				};
				return token;
			}
			const target = m[1] || repo;
			if (!target) return undefined;
			const token: RefLinkToken = {
				type: 'refLink',
				raw: m[0],
				href: `/${target}/issues/${m[2]}`,
				text: m[0],
				hover: { hover: 'issue', repo: target, number: m[2] }
			};
			return token;
		},
		renderer(token) {
			const t = token as RefLinkToken;
			const data = Object.entries(t.hover)
				.map(([k, v]) => ` data-${k}="${escapeHtml(v)}"`)
				.join('');
			return `<a href="${escapeHtml(t.href)}" class="ref-link"${data}>${escapeHtml(t.text)}</a>`;
		}
	};
}

const SCHEME = /^[a-z][a-z0-9+.-]*:/i;

function isRelative(href: string): boolean {
	if (!href) return false;
	if (href.startsWith('#') || href.startsWith('/') || href.startsWith('//')) return false;
	return !SCHEME.test(href);
}

// Joins a relative href onto a directory prefix, dots folded
export function resolveRelative(base: string | undefined, href: string): string {
	if (!base || !isRelative(href)) return href;
	const dir = base.endsWith('/') ? base : `${base}/`;
	try {
		const url = new URL(href, `http://x${dir.startsWith('/') ? '' : '/'}${dir}`);
		return `${url.pathname}${url.search}${url.hash}`;
	} catch {
		return href;
	}
}

// Resolves each srcset url, data uris leave it untouched
export function resolveSrcset(base: string | undefined, srcset: string): string {
	if (!base || /(^|,)\s*data:/i.test(srcset)) return srcset;
	return srcset
		.split(',')
		.map((candidate) => {
			const parts = candidate.trim().split(/\s+/);
			if (!parts[0]) return '';
			parts[0] = resolveRelative(base, parts[0]);
			return parts.join(' ');
		})
		.filter(Boolean)
		.join(', ');
}

const URL_ATTRS = ['src', 'poster', 'href'] as const;

// Relative paths in raw html resolve like markdown ones
function resolveEmbedded(root: HTMLElement, opts: MarkdownOptions): void {
	const mediaBase = opts.rawBase ?? opts.baseHref;
	for (const el of root.querySelectorAll<HTMLElement>('[src],[srcset],[poster],[href]')) {
		const link = el.tagName === 'A';
		const base = link ? opts.baseHref : mediaBase;
		for (const attr of URL_ATTRS) {
			const value = el.getAttribute(attr);
			if (value === null) continue;
			const target = resolveRelative(base, value.trim());
			if (target !== value) el.setAttribute(attr, target);
		}
		const srcset = el.getAttribute('srcset');
		if (srcset !== null) {
			const target = resolveSrcset(base, srcset);
			if (target !== srcset) el.setAttribute('srcset', target);
		}
		if (link) {
			const href = el.getAttribute('href') ?? '';
			if (SCHEME.test(href) || href.startsWith('//')) el.setAttribute('rel', 'nofollow noopener');
		}
	}
}

export function slugify(text: string): string {
	return text
		.toLowerCase()
		.trim()
		.replace(/<[^>]+>/g, '')
		.replace(/[^\w\s-]/g, '')
		.replace(/\s+/g, '-')
		.replace(/-+/g, '-');
}

function codeLang(lang: string | undefined): string {
	const first = (lang ?? '').trim().split(/\s+/)[0] ?? '';
	// Mermaid stays a plain block by project rule
	return first.toLowerCase() === 'mermaid' ? '' : first;
}

const PURIFY_CONFIG = {
	USE_PROFILES: { html: true },
	FORBID_TAGS: [
		'style',
		'form',
		'script',
		'iframe',
		'object',
		'embed',
		'noscript',
		'template',
		'svg',
		'math'
	],
	FORBID_ATTR: ['style'],
	ADD_ATTR: ['align'],
	ALLOW_UNKNOWN_PROTOCOLS: false
};

export async function renderMarkdown(source: string, opts: MarkdownOptions = {}): Promise<string> {
	const highlighted = new Map<Token, string>();
	const usedIds = new Map<string, number>();

	const extensions: TokenizerAndRendererExtension[] = [];
	if (opts.wikiBase) extensions.push(wikiLinks(opts.wikiBase));
	if (opts.repo) extensions.push(refLinks(opts.repo));

	const marked = new Marked({
		gfm: true,
		breaks: false,
		async: true,
		extensions,
		async walkTokens(token) {
			if (token.type !== 'code') return;
			const code = token as Tokens.Code;
			const lang = codeLang(code.lang);
			highlighted.set(token, lang ? await highlight(code.text, lang) : '');
		},
		renderer: {
			code(token) {
				const html = highlighted.get(token);
				if (html) return html;
				const lang = codeLang(token.lang);
				const cls = lang ? ` class="language-${escapeHtml(lang)}"` : '';
				return `<pre class="shiki"><code${cls}>${escapeHtml(token.text)}</code></pre>\n`;
			},
			heading({ tokens, depth }) {
				const inner = this.parser.parseInline(tokens);
				const plain = this.parser.parseInline(tokens, this.parser.textRenderer);
				let id = slugify(plain) || 'section';
				const seen = usedIds.get(id) ?? 0;
				usedIds.set(id, seen + 1);
				if (seen > 0) id = `${id}-${seen}`;
				return `<h${depth} id="${id}">${inner}<a class="heading-anchor" href="#${id}" aria-hidden="true">#</a></h${depth}>\n`;
			},
			link({ href, title, tokens }) {
				const target = resolveRelative(opts.baseHref, href);
				const text = this.parser.parseInline(tokens);
				const titleAttr = title ? ` title="${escapeHtml(title)}"` : '';
				const external = SCHEME.test(target) || target.startsWith('//');
				const rel = external ? ' rel="nofollow noopener"' : '';
				return `<a href="${escapeHtml(target)}"${titleAttr}${rel}>${text}</a>`;
			},
			image({ href, title, text }) {
				const target = resolveRelative(opts.rawBase ?? opts.baseHref, href);
				const titleAttr = title ? ` title="${escapeHtml(title)}"` : '';
				return `<img src="${escapeHtml(target)}" alt="${escapeHtml(text)}"${titleAttr} loading="lazy">`;
			},
			listitem(item) {
				const body = this.parser.parse(item.tokens);
				return item.task ? `<li class="task-item">${body}</li>\n` : `<li>${body}</li>\n`;
			}
		}
	});

	const html = await marked.parse(source ?? '');
	// Browser only, unsanitized html never leaves here
	if (typeof window === 'undefined') return '';
	const body = DOMPurify.sanitize(html, { ...PURIFY_CONFIG, RETURN_DOM: true }) as HTMLElement;
	resolveEmbedded(body, opts);
	return body.innerHTML;
}

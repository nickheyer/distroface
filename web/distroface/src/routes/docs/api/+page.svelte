<script lang="ts">
	import { onMount } from 'svelte';
	import { SvelteSet } from 'svelte/reactivity';
	import { page } from '$app/state';
	import { replaceState } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { authStore } from '$lib/stores/auth.svelte';

	// Every generated spec is picked up automatically
	const specModules = import.meta.glob('$lib/proto/specs/**/*.openapi.json', {
		eager: true,
		import: 'default'
	}) as Record<string, Record<string, unknown>>;

	// eslint-disable-next-line @typescript-eslint/no-explicit-any
	type Spec = Record<string, any>;
	type ScalarWindow = Window &
		typeof globalThis & {
			Scalar?: { createApiReference: (target: string, config: unknown) => unknown };
		};

	// App tokens the reference is painted with
	const tokens = [
		'background',
		'foreground',
		'card',
		'popover',
		'popover-foreground',
		'field',
		'primary',
		'primary-foreground',
		'secondary',
		'muted',
		'muted-foreground',
		'accent',
		'destructive',
		'success',
		'warning',
		'info',
		'done',
		'border',
		'border-strong',
		'input',
		'radius'
	];

	// Unlayered and body scoped to outrank scalar rules
	const themeCss = `
		html {
			background: var(--background);
			scrollbar-width: thin;
			scrollbar-color: color-mix(in oklch, var(--foreground) 22%, transparent) transparent;
			-webkit-font-smoothing: antialiased;
			-moz-osx-font-smoothing: grayscale;
			text-rendering: optimizeLegibility;
			font-feature-settings: 'cv11', 'ss03';
		}
		body {
			background: var(--background);
		}
		::selection {
			background: color-mix(in oklch, var(--primary) 22%, transparent);
		}
		:root,
		.light-mode,
		.dark-mode {
			--scalar-font: var(--font-sans);
			--scalar-font-code: var(--font-mono);
			--scalar-radius: calc(var(--radius) - 2px);
			--scalar-radius-lg: var(--radius);
			--scalar-radius-xl: calc(var(--radius) + 4px);
			--scalar-border-width: 1px;
			--scalar-heading-1: 24px;
			--scalar-heading-2: 18px;
			--scalar-paragraph: 14px;
			--scalar-page-description: 14px;
			--scalar-font-size-2: 14px;
			--scalar-font-size-3: 13px;
			--scalar-line-height-2: 22px;
			--scalar-bold: 600;
			--scalar-text-decoration: none;
			--scalar-text-decoration-hover: underline;

			--scalar-background-1: var(--background);
			--scalar-background-2: var(--muted);
			--scalar-background-3: var(--accent);
			--scalar-background-4: var(--border);
			--scalar-background-accent: color-mix(in oklch, var(--primary) 10%, transparent);
			--scalar-background-card: var(--card);
			--scalar-color-1: var(--foreground);
			--scalar-color-2: var(--muted-foreground);
			--scalar-color-3: color-mix(in oklch, var(--muted-foreground) 78%, transparent);
			--scalar-color-accent: var(--primary);
			--scalar-color-disabled: color-mix(in oklch, var(--muted-foreground) 55%, transparent);
			--scalar-border-color: var(--border);

			--scalar-color-green: var(--success);
			--scalar-color-red: var(--destructive);
			--scalar-color-yellow: var(--warning);
			--scalar-color-orange: var(--warning);
			--scalar-color-blue: var(--info);
			--scalar-color-purple: var(--done);
			--scalar-color-danger: var(--destructive);
			--scalar-color-alert: var(--warning);
			--scalar-background-danger: color-mix(in oklch, var(--destructive) 8%, var(--background));
			--scalar-background-alert: color-mix(in oklch, var(--warning) 8%, var(--background));

			--scalar-link-color: var(--primary);
			--scalar-link-color-hover: var(--primary);
			--scalar-button-1: var(--primary);
			--scalar-button-1-hover: color-mix(in oklch, var(--primary) 88%, var(--foreground));
			--scalar-button-1-color: var(--primary-foreground);
			--scalar-tooltip-background: var(--popover);
			--scalar-tooltip-color: var(--popover-foreground);
			--scalar-scrollbar-color: color-mix(in oklch, var(--foreground) 22%, transparent);
			--scalar-scrollbar-color-active: color-mix(in oklch, var(--foreground) 40%, transparent);
			--scalar-shadow-1: 0 1px 2px 0 rgb(0 0 0 / 0.06);
			--scalar-shadow-2:
				0 0 0 1px var(--border), 0 8px 24px -6px rgb(0 0 0 / 0.16), 0 2px 6px -2px rgb(0 0 0 / 0.08);

			--scalar-sidebar-background-1: var(--background);
			--scalar-sidebar-color-1: var(--foreground);
			--scalar-sidebar-color-2: var(--muted-foreground);
			--scalar-sidebar-color-active: var(--foreground);
			--scalar-sidebar-border-color: var(--border);
			--scalar-sidebar-item-hover-background: color-mix(in oklch, var(--muted) 60%, transparent);
			--scalar-sidebar-item-hover-color: var(--foreground);
			--scalar-sidebar-item-active-background: var(--muted);
			--scalar-sidebar-font-weight-active: 500;
			--scalar-sidebar-indent-border: var(--border);
			--scalar-sidebar-indent-border-hover: var(--border-strong);
			--scalar-sidebar-indent-border-active: var(--primary);
			--scalar-sidebar-search-background: var(--field);
			--scalar-sidebar-search-border-color: var(--input);
			--scalar-sidebar-search-color: var(--muted-foreground);
		}
		body .scalar-app.references-layout {
			box-sizing: border-box;
			width: 100%;
			min-width: 0;
			max-width: 80rem;
			margin-inline: auto;
			padding-inline: 1rem;
		}
		body .scalar-app .markdown {
			font-size: 0.875rem;
		}
		body .scalar-app .section {
			padding-block: 4rem;
		}
		body .scalar-app .section.introduction-section {
			padding-top: 2rem;
		}
		body .scalar-app .darklight-reference {
			display: none;
		}
		@media (width >= 40rem) {
			body .scalar-app.references-layout {
				padding-inline: 1.5rem;
			}
		}
		@media (width > 1000px) {
			body .scalar-app .t-doc__sidebar {
				margin-left: -0.75rem;
				padding-top: 1.25rem;
			}
			body .scalar-app .section-container {
				padding-inline: 2.5rem 0;
			}
		}
		.light-mode .t-doc__sidebar,
		.dark-mode .t-doc__sidebar {
			--scalar-sidebar-background-1: var(--background);
			--scalar-sidebar-item-hover-background: color-mix(in oklch, var(--muted) 60%, transparent);
			--scalar-sidebar-item-hover-color: var(--foreground);
			--scalar-sidebar-item-active-background: var(--muted);
			--scalar-sidebar-border-color: var(--border);
			--scalar-sidebar-search-background: var(--field);
			--scalar-sidebar-search-border-color: var(--input);
			--scalar-sidebar-search-color: var(--muted-foreground);
		}
	`;

	let loaded = $state(false);
	let frame: HTMLIFrameElement | null = $state(null);

	function mergeSpecs(specs: Spec[]): Spec {
		const merged: Spec = { openapi: '3.1.0', paths: {}, components: {}, tags: [] };

		for (const spec of specs) {
			merged.paths = { ...merged.paths, ...(spec?.paths ?? {}) };

			for (const [k, v] of Object.entries(spec?.components ?? {})) {
				if (v && typeof v === 'object' && !Array.isArray(v)) {
					merged.components[k] = { ...(merged.components[k] ?? {}), ...v };
				} else {
					merged.components[k] = v;
				}
			}

			if (Array.isArray(spec?.tags)) merged.tags.push(...spec.tags);
		}

		const seen = new SvelteSet<string>();
		merged.tags = merged.tags
			.filter((t: { name?: string }) => {
				if (!t?.name || seen.has(t.name)) return false;
				seen.add(t.name);
				return true;
			})
			.sort((a: { name: string }, b: { name: string }) => a.name.localeCompare(b.name));

		return merged;
	}

	function buildSpec(): Spec {
		const merged = mergeSpecs(Object.values(specModules));

		merged.info = {
			title: 'Distroface API',
			version: __APP_VERSION__,
			description:
				'ConnectRPC service reference. Every method is an HTTP POST accepting a JSON body, so any HTTP client works.'
		};
		merged.servers = [{ url: window.location.origin }];
		merged.components.securitySchemes = {
			bearerAuth: { type: 'http', scheme: 'bearer', bearerFormat: 'JWT' }
		};
		merged.security = [{ bearerAuth: [] }];

		return merged;
	}

	function isDark(): boolean {
		return document.documentElement.classList.contains('dark');
	}

	// Family stack a tailwind font utility resolves to
	function fontStack(utility: string): string {
		const probe = document.createElement('span');
		probe.className = utility;
		document.body.append(probe);
		const stack = getComputedStyle(probe).fontFamily;
		probe.remove();
		return stack;
	}

	// App font faces with absolute urls
	function fontFaces(): string {
		const out: string[] = [];
		for (const sheet of Array.from(document.styleSheets)) {
			let rules: CSSRuleList;
			try {
				rules = sheet.cssRules;
			} catch {
				continue;
			}
			const base = sheet.href ?? document.baseURI;
			for (const rule of Array.from(rules)) {
				if (!(rule instanceof CSSFontFaceRule)) continue;
				out.push(
					rule.cssText.replace(
						/url\((['"]?)([^'")]+)\1\)/g,
						(_, __, u: string) => `url("${new URL(u, base).href}")`
					)
				);
			}
		}
		return out.join('\n');
	}

	// Copies live tokens and mode into the frame
	function syncTheme() {
		const doc = frame?.contentDocument;
		if (!doc?.body) return;
		const app = getComputedStyle(document.documentElement);
		const root = doc.documentElement;
		for (const t of tokens) root.style.setProperty(`--${t}`, app.getPropertyValue(`--${t}`));
		const dark = isDark();
		root.style.colorScheme = dark ? 'dark' : 'light';
		doc.body.classList.toggle('dark-mode', dark);
		doc.body.classList.toggle('light-mode', !dark);
	}

	onMount(() => {
		const el = frame;
		const doc = el?.contentDocument;
		if (!el || !doc) return;

		let alive = true;
		const scriptClose = '</' + 'script>';
		const config = {
			content: buildSpec(),
			slug: 'distroface',
			theme: 'none',
			layout: 'modern',
			withDefaultFonts: false,
			forceDarkModeState: isDark() ? 'dark' : 'light',
			hideDarkModeToggle: true,
			hideClientButton: true,
			showDeveloperTools: 'never',
			telemetry: false,
			agent: { disabled: true },
			mcp: { disabled: true },
			showOperationId: true,
			authentication: {
				preferredSecurityScheme: 'bearerAuth',
				securitySchemes: { bearerAuth: { token: authStore.token ?? '' } }
			}
		};

		const boot = () => {
			const win = el.contentWindow as ScalarWindow | null;
			if (!alive || !win?.Scalar) return;
			syncTheme();

			// Written documents drop the fragment, deep links need it
			const history = win.history;
			const replace = history.replaceState.bind(history);
			if (window.location.hash) replace(null, '', window.location.hash);

			// Mirrors scalar hash changes into the address bar
			history.replaceState = (state, unused, url) => {
				replace(state, unused, url);
				if (!alive || url == null) return;
				const hash = new URL(String(url), win.location.href).hash;
				if (hash && hash !== window.location.hash) {
					replaceState(resolve(`/docs/api${page.url.search}${hash}` as `/${string}`), page.state);
				}
			};

			// Parsed in the frame so objects share its realm
			const options = win.JSON.parse(JSON.stringify(config));
			options.onLoaded = () => (loaded = true);
			win.Scalar.createApiReference('#api-reference', options);
			setTimeout(() => (loaded = true), 4000);
		};

		el.addEventListener('load', boot, { once: true });
		doc.open();
		doc.write(`<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>
${fontFaces()}
:root { --font-sans: ${fontStack('font-sans')}; --font-mono: ${fontStack('font-mono')}; }
${themeCss}
</style>
<script src="/scalar.js">${scriptClose}
</head>
<body class="${isDark() ? 'dark-mode' : 'light-mode'}">
<div id="api-reference"></div>
</body>
</html>`);
		doc.close();
		syncTheme();

		// Tokens change when mode-watcher flips the root class
		const observer = new MutationObserver(syncTheme);
		observer.observe(document.documentElement, {
			attributes: true,
			attributeFilter: ['class', 'style']
		});

		return () => {
			alive = false;
			observer.disconnect();
			el.removeEventListener('load', boot);
		};
	});
</script>

<svelte:head>
	<title>API Reference - Distroface</title>
</svelte:head>

<div class="absolute inset-0">
	{#if !loaded}
		<div class="absolute inset-0 flex items-center justify-center" role="status">
			<div
				class="size-4 animate-spin rounded-full border-2 border-primary/25 border-t-primary"
			></div>
			<span class="sr-only">Loading API reference</span>
		</div>
	{/if}
	<iframe
		bind:this={frame}
		title="API reference"
		class="size-full border-0 transition-opacity duration-150 {loaded ? '' : 'opacity-0'}"
		referrerpolicy="same-origin"
		sandbox="allow-scripts allow-same-origin allow-downloads allow-popups"
	></iframe>
</div>

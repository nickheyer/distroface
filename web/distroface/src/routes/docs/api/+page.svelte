<script lang="ts">
	import { onMount } from 'svelte';
	import { session } from '$lib/state/session.svelte';

	// Every generated spec is picked up automatically
	const specModules = import.meta.glob('$lib/proto/specs/**/*.openapi.json', {
		eager: true,
		import: 'default'
	}) as Record<string, Record<string, unknown>>;

	let frame: HTMLIFrameElement | null = $state(null);
	let loading = $state(true);

	// eslint-disable-next-line @typescript-eslint/no-explicit-any
	type Spec = Record<string, any>;

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
		const seen = new Set<string>();
		merged.tags = merged.tags
			.filter((t: { name?: string }) => {
				if (!t?.name || seen.has(t.name)) return false;
				seen.add(t.name);
				return true;
			})
			.sort((a: { name: string }, b: { name: string }) => a.name.localeCompare(b.name));
		return merged;
	}

	function buildSpec(): string {
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
		// Escape to keep spec text from closing the inline script
		return JSON.stringify(merged).replace(/</g, '\\u003c');
	}

	function buildFrame(): string {
		const config = JSON.stringify({
			darkMode: document.documentElement.dataset.theme === 'dark',
			hideDarkModeToggle: true,
			hideClientButton: true,
			agent: { disabled: true },
			mcp: { disabled: true },
			showOperationId: true,
			authentication: {
				preferredSecurityScheme: 'bearerAuth',
				securitySchemes: { bearerAuth: { token: session.token ?? '' } }
			}
		});

		return (
			`<!DOCTYPE html><html><head><meta charset="utf-8">` +
			`<meta name="viewport" content="width=device-width, initial-scale=1">` +
			`<script src="/scalar.js"><\/script>` +
			`<style>a[href="https://www.scalar.com"]{display:none !important}</style>` +
			`</head><body style="margin:0;padding:0"><div id="api-reference"></div><script>` +
			`window.addEventListener('load', () => {` +
			`window.Scalar.createApiReference('#api-reference', { content: ` +
			buildSpec() +
			`, ...` +
			config +
			` });` +
			`window.parent.postMessage({ type: 'scalar-loaded' }, '*');` +
			`});<\/script></body></html>`
		);
	}

	onMount(() => {
		const doc = frame?.contentDocument;
		if (doc) {
			doc.open();
			doc.write(buildFrame());
			doc.close();
		}
		const onMessage = (e: MessageEvent) => {
			if (e.data?.type === 'scalar-loaded') {
				loading = false;
				window.removeEventListener('message', onMessage);
			}
		};
		window.addEventListener('message', onMessage);
		return () => window.removeEventListener('message', onMessage);
	});
</script>

<hgroup class="folio">
	<p class="kicker">Reference</p>
	<h1>The API</h1>
	<p class="sub">
		Every service, method, and message, generated from the protobuf definitions.
		{#if session.signedIn}Requests sent from this page use your session.{/if}
	</p>
</hgroup>

{#if loading}
	<p class="working">loading</p>
{/if}
<iframe bind:this={frame} title="API reference" class="console" class:hidden={loading}></iframe>

<style>
	.console {
		width: 100%;
		height: calc(100vh - 16rem);
		min-height: 30rem;
		border: 1px solid var(--hairline-dark);
		background: var(--paper-high);
	}
	.hidden {
		visibility: hidden;
		height: 0;
		min-height: 0;
		border: 0;
	}
</style>

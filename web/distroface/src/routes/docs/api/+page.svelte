<script lang="ts">
	import { onMount } from 'svelte';
	import { mode } from 'mode-watcher';
	import { Progress } from '$lib/components/ui/progress';
	import { authStore } from '$lib/stores/auth.svelte';

	// Every generated spec is picked up automatically
	const specModules = import.meta.glob('$lib/proto/specs/**/*.openapi.json', {
		eager: true,
		import: 'default'
	}) as Record<string, Record<string, unknown>>;

	let isLoading = $state(true);
	let loadingProgress = $state(10);
	let iframeElement: HTMLIFrameElement | null = $state(null);

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
		const scalarConfig = JSON.stringify({
			darkMode: mode.current === 'dark',
			hideDarkModeToggle: true,
			hideClientButton: true,
			agent: { disabled: true },
			mcp: { disabled: true },
			showOperationId: true,
			authentication: {
				preferredSecurityScheme: 'bearerAuth',
				securitySchemes: { bearerAuth: { token: authStore.token ?? '' } }
			}
		});

		return (
			`
			<!DOCTYPE html>
			<html>
			<head>
				<meta charset="utf-8">
				<meta name="viewport" content="width=device-width, initial-scale=1">
				<script src="/scalar.js"><\/script>
				<style>
					a[href="https://www.scalar.com"] { display: none !important; }
				</style>
			</head>
			<body style="margin: 0; padding: 0;">
				<div id="api-reference"></div>
				<script>
					window.addEventListener('load', () => {
						window.parent.postMessage({ type: 'scalar-progress', value: 50 }, '*');
						window.Scalar.createApiReference('#api-reference', {
							content: ` +
			buildSpec() +
			`,
							...` +
			scalarConfig +
			`
						});
						window.parent.postMessage({ type: 'scalar-loaded' }, '*');
					});
				<\/script>
			</body>
			</html>
		`
		);
	}

	onMount(() => {
		const doc = iframeElement?.contentDocument;
		if (doc) {
			doc.open();
			doc.write(buildFrame());
			doc.close();
		}

		// Fake progress until scalar reports real load state
		const progressInterval = setInterval(() => {
			if (loadingProgress < 90) loadingProgress += 10;
		}, 200);

		const handleMessage = (e: MessageEvent) => {
			if (e.data?.type === 'scalar-progress') {
				loadingProgress = e.data.value;
			} else if (e.data?.type === 'scalar-loaded') {
				clearInterval(progressInterval);
				loadingProgress = 100;
				setTimeout(() => (isLoading = false), 300);
				window.removeEventListener('message', handleMessage);
			}
		};
		window.addEventListener('message', handleMessage);

		return () => {
			clearInterval(progressInterval);
			window.removeEventListener('message', handleMessage);
		};
	});
</script>

<svelte:head>
	<title>API Reference - Distroface</title>
</svelte:head>

<div class="relative h-[calc(100vh-8rem)] w-full overflow-hidden rounded-lg border border-border/50">
	{#if isLoading}
		<div class="absolute inset-0 z-10 flex items-center justify-center bg-background/80">
			<div class="w-full max-w-md px-8">
				<div class="mb-4 text-center">
					<p class="text-sm text-muted-foreground">Loading API reference...</p>
				</div>
				<Progress value={loadingProgress} max={100} class="h-2" />
			</div>
		</div>
	{/if}
	<iframe
		bind:this={iframeElement}
		title="API Reference"
		class="h-full w-full border-0 {isLoading ? 'invisible' : ''}"
		referrerpolicy="same-origin"
		sandbox="allow-scripts allow-same-origin"
	></iframe>
</div>

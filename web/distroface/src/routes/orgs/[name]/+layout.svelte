<script lang="ts">
	import { page } from '$app/state';
	import { setContext } from 'svelte';
	import { OrgCtx, ORG_CTX } from '$lib/state/orgctx.svelte';

	let { children } = $props();

	const ctx = new OrgCtx();
	setContext(ORG_CTX, ctx);

	const orgName = $derived(page.params.name!);

	$effect(() => {
		ctx.load(orgName);
	});

	const path = $derived(page.url.pathname);
	const base = $derived(`/orgs/${orgName}`);

	function at(sub: string): boolean {
		const p = base + sub;
		return sub === '' ? path === base : path === p || path.startsWith(p + '/');
	}

	const tabs = $derived([
		{ href: '', label: 'Overview' },
		{ href: '/portals', label: 'Portals' },
		{ href: '/trust', label: 'Trust' },
		{ href: '/webhooks', label: 'Webhooks' },
		...(ctx.isAdmin ? [{ href: '/settings', label: 'Settings' }] : [])
	]);
</script>

{#if ctx.missing}
	<hgroup class="folio">
		<p class="kicker">Organizations</p>
		<h1>Not found</h1>
		<p class="sub">
			No organization named <span class="mono">{orgName}</span> exists here. Back to the
			<a href="/orgs">list</a>.
		</p>
	</hgroup>
{:else if ctx.org}
	<hgroup class="folio">
		<p class="kicker"><a href="/orgs">Organizations</a> / {ctx.org.name}</p>
		<h1>{ctx.org.displayName || ctx.org.name}</h1>
		{#if ctx.org.description}
			<p class="sub">{ctx.org.description}</p>
		{/if}
	</hgroup>

	<nav class="orgnav">
		{#each tabs as tab (tab.href)}
			<a href="{base}{tab.href}" aria-current={at(tab.href)}>{tab.label}</a>
		{/each}
		<a href="/?ns={ctx.org.name}" class="apart-link">Repositories&nbsp;→</a>
		<a href="/artifacts?ns={ctx.org.name}">Artifacts&nbsp;→</a>
	</nav>

	{@render children()}
{:else}
	<p class="working" style="margin-top: 4rem">loading</p>
{/if}

<style>
	.orgnav {
		display: flex;
		gap: 1.4rem;
		flex-wrap: wrap;
		border-top: 1px solid var(--ink);
		border-bottom: 1px solid var(--hairline);
		padding: 0.45rem 0;
		margin-top: 1.2rem;
	}
	.orgnav a {
		font-family: var(--mono);
		font-size: var(--caps-size);
		letter-spacing: var(--caps-track);
		text-transform: uppercase;
		color: var(--ink-soft);
		text-decoration: none;
	}
	.orgnav a:hover {
		color: var(--ink);
	}
	.orgnav a[aria-current='true'] {
		color: var(--wax);
	}
	.orgnav .apart-link {
		margin-left: auto;
	}
</style>

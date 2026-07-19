<script lang="ts">
	import { page } from '$app/state';
	import { session } from '$lib/state/session.svelte';

	let { children } = $props();

	const path = $derived(page.url.pathname);

	const sections = $derived(
		[
			session.canReadUsers ? { href: '/admin/users', label: 'Users' } : null,
			session.canReadRoles ? { href: '/admin/roles', label: 'Roles' } : null,
			session.canReadSettings ? { href: '/admin/invites', label: 'Invites' } : null,
			session.canManageSettings ? { href: '/admin/trust', label: 'Trust' } : null,
			session.canReadSettings ? { href: '/admin/settings', label: 'Settings' } : null,
			session.canManageSettings ? { href: '/admin/storage', label: 'Storage' } : null,
			session.canManageSettings ? { href: '/admin/audit', label: 'Audit' } : null
		].filter((s) => s !== null)
	);
</script>

<hgroup class="folio">
	<p class="kicker">Distroface</p>
	<h1>Administration</h1>
</hgroup>

<nav class="orgnav">
	{#each sections as s (s.href)}
		<a href={s.href} aria-current={path === s.href || path.startsWith(s.href + '/')}>{s.label}</a>
	{/each}
</nav>

{@render children()}

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
</style>

<script lang="ts">
	import '../app.css';
	import { page } from '$app/state';
	import { goto, afterNavigate } from '$app/navigation';
	import { session } from '$lib/state/session.svelte';
	import { site, gate } from '$lib/state/site.svelte';
	import { errata } from '$lib/state/errata.svelte';
	import { theme } from '$lib/state/theme.svelte';

	let { children } = $props();

	let booted = $state(false);

	$effect(() => {
		theme.init();
		Promise.all([session.init(), site.init(), gate.init()]).finally(() => (booted = true));
	});

	afterNavigate(() => errata.sweep());

	const path = $derived(page.url.pathname);
	const publicPaths = ['/login'];

	// Route guard, waits for boot then walks unauthenticated readers out
	$effect(() => {
		if (!booted || !session.ready) return;
		if (publicPaths.includes(path)) return;
		if (!session.signedIn && !session.anonymousAccess) {
			goto('/login', { replaceState: true });
			return;
		}
		if (session.user?.mustChangePassword && path !== '/change-password') {
			goto('/change-password', { replaceState: true });
		}
	});

	function at(prefix: string): boolean {
		if (prefix === '/') return path === '/' || /^\/r\//.test(path);
		return path === prefix || path.startsWith(prefix + '/');
	}

	async function signOut() {
		await session.logout();
		goto('/login');
	}
</script>

<svelte:head>
	<title>{gate.isPortal ? gate.displayName : 'Distroface'}</title>
</svelte:head>

<div class="sheet">
	<header class="masthead">
		{#if gate.isPortal}
			<a class="brand" href="/">
				<span class="seal">DF</span>
				<b>{gate.displayName}</b>
			</a>
			<nav class="leaves">
				<a href="/" aria-current={path === '/'}>Registry</a>
				{#if gate.primaryOrigin}
					<a href={gate.primaryOrigin} rel="external">Primary&nbsp;↗</a>
				{/if}
				{#if session.signedIn}
					<a href="/account" aria-current={at('/account')}>Account</a>
				{:else}
					<a href="/login" aria-current={at('/login')}>Sign in</a>
				{/if}
				<button class="mode" onclick={() => theme.toggle()} aria-label="switch color theme">
					{theme.mode === 'dark' ? 'Light' : 'Dark'}
				</button>
			</nav>
		{:else}
			<a class="brand" href="/">
				<span class="seal">DF</span>
				<b>DISTROFACE</b>
			</a>
			<nav class="leaves">
				<a href="/" aria-current={at('/')}>Registry</a>
				<a href="/artifacts" aria-current={at('/artifacts')}>Artifacts</a>
				<a href="/orgs" aria-current={at('/orgs')}>Organizations</a>
				{#if session.adminGate}
					<a href="/admin" aria-current={at('/admin')}>Administration</a>
				{/if}
				{#if session.signedIn}
					<a href="/account" aria-current={at('/account')}>Account</a>
				{:else if session.ready}
					<a href="/login" aria-current={at('/login')}>Sign in</a>
				{/if}
				<button class="mode" onclick={() => theme.toggle()} aria-label="switch color theme">
					{theme.mode === 'dark' ? 'Light' : 'Dark'}
				</button>
			</nav>
		{/if}
	</header>

	{#if errata.slips.length > 0}
		<div class="errata">
			{#each errata.slips as slip (slip.id)}
				<div class="slip" class:plain={slip.kind === 'plain'}>
					<span class="caps faint">{slip.kind === 'fault' ? 'error' : 'notice'}</span>
					<span class="what">{slip.text}</span>
					<button onclick={() => errata.dismiss(slip.id)} aria-label="dismiss">×</button>
				</div>
			{/each}
		</div>
	{/if}

	<main>
		{#if booted && session.ready}
			{@render children()}
		{:else}
			<p class="working" style="margin-top: 4rem">loading</p>
		{/if}
	</main>

	<footer class="colophon">
		<span>Distroface {__APP_VERSION__}</span>
		<a href="/docs/api">API reference</a>
		{#if session.signedIn}
			<span class="apart">{session.user?.username}</span>
			<button
				class="rowact plain"
				style="font-size: 0.66rem; letter-spacing: 0.1em; text-transform: uppercase"
				onclick={signOut}>sign out</button>
		{/if}
	</footer>
</div>

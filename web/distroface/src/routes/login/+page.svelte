<script lang="ts">
	import { goto } from '$app/navigation';
	import { rpc, hush } from '$lib/rpc';
	import { session } from '$lib/state/session.svelte';
	import { gate } from '$lib/state/site.svelte';

	let mode = $state<'enter' | 'enroll'>('enter');

	let identifier = $state('');
	let password = $state('');

	let username = $state('');
	let email = $state('');
	let newPassword = $state('');
	let inviteCode = $state('');
	let invitePin = $state('');
	let needsPin = $state(false);

	let busy = $state(false);
	let fault = $state('');

	// OIDC returns with the session token in the fragment
	$effect(() => {
		const m = window.location.hash.match(/#token=([^&]+)/);
		if (m) {
			history.replaceState(null, '', window.location.pathname);
			session
				.adoptToken(decodeURIComponent(m[1]))
				.then(() => landing())
				.catch(() => (fault = 'The returned session was not accepted.'));
		}
	});

	// Invite links land on registration with the code filled in
	$effect(() => {
		const code = new URLSearchParams(window.location.search).get('invite');
		if (code) {
			inviteCode = code;
			mode = 'enroll';
			checkInvite();
		}
	});

	$effect(() => {
		if (session.ready && session.signedIn && !session.user?.mustChangePassword) goto('/');
	});

	function landing() {
		goto(session.user?.mustChangePassword ? '/change-password' : '/', { replaceState: true });
	}

	async function enter(e: Event) {
		e.preventDefault();
		busy = true;
		fault = '';
		try {
			await session.login(identifier, password);
			landing();
		} catch (err) {
			fault = err instanceof Error ? err.message : 'Sign-in failed.';
		} finally {
			busy = false;
		}
	}

	async function checkInvite() {
		if (!inviteCode.trim()) {
			needsPin = false;
			return;
		}
		try {
			const r = await rpc.auth.validateInvite({ code: inviteCode.trim() }, hush);
			needsPin = r.valid && r.requiresPin;
		} catch {
			needsPin = false;
		}
	}

	async function enroll(e: Event) {
		e.preventDefault();
		busy = true;
		fault = '';
		try {
			await session.register(
				username,
				email,
				newPassword,
				inviteCode.trim() || undefined,
				invitePin || undefined
			);
			landing();
		} catch (err) {
			fault = err instanceof Error ? err.message : 'Registration failed.';
		} finally {
			busy = false;
		}
	}

	async function viaOIDC() {
		busy = true;
		try {
			const r = await rpc.auth.getOIDCLoginURL({});
			window.location.href = r.redirectUrl;
		} finally {
			busy = false;
		}
	}

	const mayEnroll = $derived(session.firstUserSetup || session.allowRegistration);
</script>

<div class="gatefold">
	<hgroup class="folio">
		<p class="kicker">{gate.isPortal ? gate.displayName : 'Distroface'}</p>
		<h1>{mode === 'enter' ? 'Sign in' : session.firstUserSetup ? 'First account' : 'Create account'}</h1>
		{#if session.firstUserSetup && mode === 'enroll'}
			<p class="sub">
				This instance has no accounts yet. The first account created becomes its administrator.
			</p>
		{/if}
	</hgroup>

	{#if fault}
		<p class="fault note wax-ink">† {fault}</p>
	{/if}

	{#if mode === 'enter'}
		{#if session.localEnabled}
			<form onsubmit={enter}>
				<label class="field">
					<span>Username or email</span>
					<input type="text" bind:value={identifier} autocomplete="username" required />
				</label>
				<label class="field">
					<span>Password</span>
					<input type="password" bind:value={password} autocomplete="current-password" required />
				</label>
				<div class="row gap-top">
					<button class="act wax" type="submit" disabled={busy}>Sign in</button>
					{#if session.oidcEnabled}
						<button class="act" type="button" disabled={busy} onclick={viaOIDC}
							>Via identity provider</button>
					{/if}
				</div>
			</form>
		{:else if session.oidcEnabled}
			<p class="note">Local accounts are disabled. Sign in through your identity provider.</p>
			<div class="gap-top">
				<button class="act wax" disabled={busy} onclick={viaOIDC}>Continue to provider</button>
			</div>
		{:else}
			<p class="note">No sign-in method is enabled on this instance.</p>
		{/if}

		{#if mayEnroll}
			<p class="note gap-top">
				No account?
				<button class="rowact" onclick={() => (mode = 'enroll')}>
					{session.firstUserSetup ? 'create the first account' : 'register with an invite'}
				</button>
			</p>
		{/if}
	{:else}
		<form onsubmit={enroll}>
			<label class="field">
				<span>Username</span>
				<input type="text" bind:value={username} autocomplete="username" required />
			</label>
			<label class="field">
				<span>Email</span>
				<input type="email" bind:value={email} autocomplete="email" required />
			</label>
			<label class="field">
				<span>Password</span>
				<input type="password" bind:value={newPassword} autocomplete="new-password" required />
			</label>
			{#if !session.firstUserSetup}
				<label class="field">
					<span>Invite code</span>
					<input type="text" bind:value={inviteCode} onblur={checkInvite} />
					<span class="hint">Issued by an administrator. Some invites also carry a PIN.</span>
				</label>
				{#if needsPin}
					<label class="field">
						<span>Invite PIN</span>
						<input type="password" bind:value={invitePin} required />
					</label>
				{/if}
			{/if}
			<div class="row gap-top">
				<button class="act wax" type="submit" disabled={busy}>Create account</button>
				<button class="rowact plain" type="button" onclick={() => (mode = 'enter')}>
					back to sign in
				</button>
			</div>
		</form>
	{/if}
</div>

<style>
	.gatefold {
		max-width: 26rem;
		margin: 3rem auto 0;
	}
	.fault {
		margin-bottom: 1rem;
	}
</style>

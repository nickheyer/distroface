<script lang="ts">
	import { goto } from '$app/navigation';
	import { rpc } from '$lib/rpc';
	import { Lister } from '$lib/list.svelte';
	import type { APIToken } from '$lib/proto/distroface/v1/types_pb';
	import { fmtDate, fmtWhen } from '$lib/fmt';
	import { session } from '$lib/state/session.svelte';
	import { gate } from '$lib/state/site.svelte';
	import { errata } from '$lib/state/errata.svelte';
	import Leaf from '$lib/bits/Leaf.svelte';
	import Tally from '$lib/bits/Tally.svelte';
	import Copy from '$lib/bits/Copy.svelte';
	import Confirm from '$lib/bits/Confirm.svelte';

	let displayName = $state('');
	let email = $state('');
	let profileBusy = $state(false);

	$effect(() => {
		if (session.user) {
			displayName = session.user.displayName;
			email = session.user.email;
		}
	});

	async function saveProfile(e: Event) {
		e.preventDefault();
		profileBusy = true;
		try {
			await rpc.user.updateUser({ displayName, email });
			await session.refresh();
			errata.remark('Profile saved.');
		} catch {
			// Interceptor reports
		} finally {
			profileBusy = false;
		}
	}

	const tokens = new Lister<APIToken>((page) =>
		rpc.token.listAPITokens({ page }).then((r) => ({ rows: r.tokens, page: r.page }))
	);

	$effect(() => {
		tokens.first();
	});

	let tokenName = $state('');
	let tokenDays = $state('');
	let tokenBusy = $state(false);
	let issued = $state<{ name: string; secret: string } | null>(null);

	async function issueToken(e: Event) {
		e.preventDefault();
		tokenBusy = true;
		try {
			const r = await rpc.token.createAPIToken({
				name: tokenName.trim(),
				expiresInDays: tokenDays.trim() ? Number(tokenDays) : undefined
			});
			issued = { name: tokenName.trim(), secret: r.plaintextToken };
			tokenName = '';
			tokenDays = '';
			await tokens.first();
		} catch {
			// Interceptor reports
		} finally {
			tokenBusy = false;
		}
	}

	async function revoke(t: APIToken) {
		await rpc.token.deleteAPIToken({ id: t.id });
		errata.remark(`Token ${t.name} revoked.`);
		if (issued?.name === t.name) issued = null;
		await tokens.first();
	}

	async function signOut() {
		await session.logout();
		goto('/login');
	}
</script>

<hgroup class="folio">
	<p class="kicker">Account</p>
	<h1>{session.user?.displayName || session.user?.username}</h1>
	<p class="sub">
		<span class="mono">{session.user?.username}</span>
		· joined {fmtDate(session.user?.createdAt)}
		· via {session.user?.authProvider || 'local'} ·
		<a href="/u/{session.user?.username}" style="font-style: normal">public profile</a> ·
		<button class="rowact plain" style="font-style: normal" onclick={signOut}>sign out</button>
	</p>
</hgroup>

<Leaf no="01" title="Profile">
	<dl class="docket" style="max-width: 40rem; margin-bottom: 1.2rem">
		<dt>Username</dt>
		<dd class="mono">{session.user?.username}</dd>
		<dt>Roles</dt>
		<dd>
			{#if session.user?.roles.length}
				{session.user.roles.map((r) => r.name).join(', ')}
			{:else}
				<span class="faint">none</span>
			{/if}
		</dd>
		<dt>Provider</dt>
		<dd class="mono">{session.user?.authProvider || 'local'}</dd>
	</dl>

	<form onsubmit={saveProfile}>
		<label class="field">
			<span>Display name</span>
			<input type="text" bind:value={displayName} />
		</label>
		<label class="field">
			<span>Email</span>
			<input type="email" bind:value={email} />
		</label>
		<button class="act" type="submit" disabled={profileBusy}>Save profile</button>
	</form>
</Leaf>

{#if session.user?.authProvider === 'local' || !session.user?.authProvider}
	<Leaf no="02" title="Password">
		<p class="note">
			Passwords are changed on their own page: <a href="/change-password">change password</a>.
		</p>
	</Leaf>
{/if}

<Leaf no="03" title="Access tokens">
	<p class="note" style="margin-bottom: 0.9rem">
		Personal tokens for docker logins and the API. Each is shown once at issue and never again.
	</p>

	{#if issued}
		<div class="panel">
			<p class="panel-title">Issued · {issued.name}</p>
			<p class="note wax-ink" style="margin-bottom: 0.6rem">
				† Copy it now; it will not be shown again.
			</p>
			<div class="cmdline" style="white-space: normal; overflow-wrap: anywhere">
				{issued.secret}
				<Copy text={issued.secret} />
			</div>
			<p class="note gap-top" style="margin-bottom: 0.4rem">Use it as the password for docker or the API:</p>
			<div class="stack">
				<div class="cmdline">
					docker login {gate.host()} -u {session.user?.username}
					<Copy text={`docker login ${gate.host()} -u ${session.user?.username}`} />
				</div>
				<div class="cmdline" style="white-space: normal; overflow-wrap: anywhere">
					curl -H "Authorization: Bearer {issued.secret}" https://{gate.host()}/api/…
					<Copy text={`curl -H "Authorization: Bearer ${issued.secret}"`} />
				</div>
			</div>
			<div class="gap-top">
				<button class="rowact plain" onclick={() => (issued = null)}>dismiss</button>
			</div>
		</div>
	{/if}

	{#if tokens.loaded && tokens.rows.length === 0}
		<p class="vacant">No tokens issued.</p>
	{:else}
		<div class="ledger-scroll">
			<table class="ledger">
				<thead>
					<tr>
						<th>Token</th>
						<th>Issued</th>
						<th>Expires</th>
						<th>Last used</th>
						<th class="end">&nbsp;</th>
					</tr>
				</thead>
				<tbody>
					{#each tokens.rows as t (t.id)}
						<tr>
							<td>{t.name}</td>
							<td class="mono">{fmtDate(t.createdAt)}</td>
							<td class="mono">{fmtDate(t.expiresAt)}</td>
							<td class="mono">{fmtWhen(t.lastUsedAt)}</td>
							<td class="end">
								<Confirm label="revoke" onconfirm={() => revoke(t)} />
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
		<Tally lister={tokens} unit="tokens" />
	{/if}

	<form class="row gap-top" onsubmit={issueToken}>
		<input
			type="text"
			style="width: 13rem"
			placeholder="token name…"
			bind:value={tokenName}
			aria-label="token name"
		/>
		<input
			type="text"
			style="width: 11rem"
			placeholder="expiry days, blank = never"
			bind:value={tokenDays}
			aria-label="expiry days"
		/>
		<button class="act" type="submit" disabled={tokenBusy || !tokenName.trim()}>Issue token</button>
	</form>
</Leaf>

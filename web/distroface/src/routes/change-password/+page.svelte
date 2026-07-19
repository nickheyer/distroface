<script lang="ts">
	import { goto } from '$app/navigation';
	import { rpc } from '$lib/rpc';
	import { session } from '$lib/state/session.svelte';
	import { errata } from '$lib/state/errata.svelte';

	let current = $state('');
	let fresh = $state('');
	let again = $state('');
	let busy = $state(false);
	let show = $state(false);

	const forced = $derived(session.user?.mustChangePassword ?? false);
	const tooShort = $derived(fresh.length > 0 && fresh.length < 8);
	const mismatch = $derived(again.length > 0 && fresh !== again);

	async function change(e: Event) {
		e.preventDefault();
		if (fresh.length < 8) {
			errata.report('The new password must be at least 8 characters.');
			return;
		}
		if (fresh !== again) {
			errata.report('The new passwords do not match.');
			return;
		}
		busy = true;
		try {
			await rpc.user.changePassword({ currentPassword: current, newPassword: fresh });
			await session.refresh();
			errata.remark('Password changed.');
			goto('/');
		} catch {
			// Interceptor reports
		} finally {
			busy = false;
		}
	}
</script>

<div class="gatefold">
	<hgroup class="folio">
		<p class="kicker">Account</p>
		<h1>Change password</h1>
		{#if forced}
			<p class="sub">
				This account was issued a temporary password. Choose a new one before proceeding.
			</p>
		{/if}
	</hgroup>

	<form onsubmit={change}>
		<label class="field">
			<span>Current password</span>
			<input
				type={show ? 'text' : 'password'}
				bind:value={current}
				autocomplete="current-password"
				required
			/>
		</label>
		<label class="field">
			<span>New password</span>
			<input
				type={show ? 'text' : 'password'}
				bind:value={fresh}
				autocomplete="new-password"
				minlength="8"
				required
			/>
			{#if tooShort}
				<span class="hint wax-ink">At least 8 characters.</span>
			{:else}
				<span class="hint">At least 8 characters.</span>
			{/if}
		</label>
		<label class="field">
			<span>New password, again</span>
			<input
				type={show ? 'text' : 'password'}
				bind:value={again}
				autocomplete="new-password"
				required
			/>
			{#if mismatch}
				<span class="hint wax-ink">Does not match.</span>
			{/if}
		</label>
		<label class="tick">
			<input type="checkbox" bind:checked={show} />
			Show passwords
		</label>
		<div class="gap-top">
			<button class="act wax" type="submit" disabled={busy || tooShort || mismatch}>Change</button>
		</div>
	</form>
</div>

<style>
	.gatefold {
		max-width: 26rem;
		margin: 3rem auto 0;
	}
</style>

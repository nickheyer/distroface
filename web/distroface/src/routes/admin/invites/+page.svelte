<script lang="ts">
	import { rpc } from '$lib/rpc';
	import { Lister } from '$lib/list.svelte';
	import type { Role } from '$lib/proto/distroface/v1/types_pb';
	import type { RegistrationInvite } from '$lib/proto/distroface/v1/auth_pb';
	import { fmtDate } from '$lib/fmt';
	import { errata } from '$lib/state/errata.svelte';
	import Leaf from '$lib/bits/Leaf.svelte';
	import Tally from '$lib/bits/Tally.svelte';
	import Copy from '$lib/bits/Copy.svelte';
	import Confirm from '$lib/bits/Confirm.svelte';

	const invites = new Lister<RegistrationInvite>((page) =>
		rpc.auth.listInvites({ page }).then((r) => ({ rows: r.invites, page: r.page }))
	);

	let allRoles = $state<Role[]>([]);

	$effect(() => {
		invites.first();
		rpc.role.listRoles({ page: { pageSize: 200 } }).then((r) => (allRoles = r.roles));
	});

	let picked = $state<Set<string>>(new Set());

	function togglePick(id: string, on: boolean) {
		const next = new Set(picked);
		if (on) next.add(id);
		else next.delete(id);
		picked = next;
	}

	let creating = $state(false);
	let newDesc = $state('');
	let newRoles = $state<Set<string>>(new Set());
	let newPin = $state('');
	let newMaxUses = $state('');
	let newExpiry = $state('');
	let busy = $state(false);

	function toggleRole(id: string, on: boolean) {
		const next = new Set(newRoles);
		if (on) next.add(id);
		else next.delete(id);
		newRoles = next;
	}

	async function issue(e: Event) {
		e.preventDefault();
		busy = true;
		try {
			await rpc.auth.createInvite({
				description: newDesc,
				roleIds: [...newRoles],
				pin: newPin || undefined,
				maxUses: newMaxUses.trim() ? Number(newMaxUses) : undefined,
				expiresInHours: newExpiry.trim() ? Number(newExpiry) : undefined
			});
			errata.remark('Invite issued.');
			creating = false;
			newDesc = '';
			newRoles = new Set();
			newPin = '';
			newMaxUses = '';
			newExpiry = '';
			await invites.first();
		} catch {
			// Interceptor reports
		} finally {
			busy = false;
		}
	}

	async function withdraw(inv: RegistrationInvite) {
		await rpc.auth.deleteInvite({ id: inv.id });
		errata.remark('Invite revoked.');
		await invites.fetch();
	}

	async function withdrawPicked() {
		const r = await rpc.auth.bulkDeleteInvites({ ids: [...picked] });
		if (r.errors.length) errata.report(`${r.deletedCount} revoked; ${r.errors.length} failed.`);
		else errata.remark(`${r.deletedCount} revoked.`);
		picked = new Set();
		await invites.first();
	}

	function inviteLink(code: string): string {
		return `${window.location.origin}/login?invite=${encodeURIComponent(code)}`;
	}
</script>

<Leaf no="01" title="Active invites">
	{#if invites.loaded && invites.rows.length === 0}
		<p class="vacant">No invites yet.</p>
	{:else}
		<div class="ledger-scroll">
			<table class="ledger">
				<thead>
					<tr>
						<th>&nbsp;</th>
						<th>Code</th>
						<th>Description</th>
						<th>Roles</th>
						<th class="num">Uses</th>
						<th>Expires</th>
						<th>Created by</th>
						<th class="end">&nbsp;</th>
					</tr>
				</thead>
				<tbody>
					{#each invites.rows as inv (inv.id)}
						<tr>
							<td>
								<input
									type="checkbox"
									checked={picked.has(inv.id)}
									onchange={(e) => togglePick(inv.id, e.currentTarget.checked)}
									aria-label="select invite"
								/>
							</td>
							<td class="mono">
								{inv.code}
								<Copy text={inv.code} />
								<Copy text={inviteLink(inv.code)} label="copy link" />
								{#if inv.hasPin}
									<span class="caps soft" title="a PIN is required">· pin</span>
								{/if}
							</td>
							<td class="note">{inv.description || ''}</td>
							<td><span class="caps soft">{inv.roles.map((r) => r.name).join(' · ') || '—'}</span></td>
							<td class="num mono">{inv.useCount}{inv.maxUses ? ` / ${inv.maxUses}` : ''}</td>
							<td class="mono">{fmtDate(inv.expiresAt)}</td>
							<td>{inv.createdBy}</td>
							<td class="end">
								<Confirm label="revoke" onconfirm={() => withdraw(inv)} />
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
		<Tally lister={invites} unit="invites" />
	{/if}

	{#if picked.size > 0}
		<div class="row gap-top">
			<Confirm label="revoke all {picked.size} selected" onconfirm={withdrawPicked} />
		</div>
	{/if}
</Leaf>

<Leaf no="02" title="New invite">
	{#if creating}
		<form class="panel" onsubmit={issue}>
			<label class="field">
				<span>Description</span>
				<input type="text" bind:value={newDesc} placeholder="the platform team…" />
			</label>
			<fieldset class="field">
				<span>Roles granted on registration</span>
				{#each allRoles as r (r.id)}
					<label class="tick">
						<input
							type="checkbox"
							checked={newRoles.has(r.id)}
							onchange={(e) => toggleRole(r.id, e.currentTarget.checked)}
						/>
						{r.name}
					</label>
				{/each}
			</fieldset>
			<div class="row">
				<label class="field" style="flex: 1; min-width: 9rem">
					<span>PIN</span>
					<input type="text" bind:value={newPin} placeholder="optional" />
				</label>
				<label class="field" style="flex: 1; min-width: 9rem">
					<span>Uses allowed</span>
					<input type="text" bind:value={newMaxUses} placeholder="unlimited" />
				</label>
				<label class="field" style="flex: 1; min-width: 9rem">
					<span>Expires in, hours</span>
					<input type="text" bind:value={newExpiry} placeholder="never" />
				</label>
			</div>
			<div class="row gap-top">
				<button class="act wax" type="submit" disabled={busy}>Create invite</button>
				<button class="rowact plain" type="button" onclick={() => (creating = false)}>cancel</button>
			</div>
		</form>
	{:else}
		<button class="act" onclick={() => (creating = true)}>Create invite</button>
	{/if}
</Leaf>

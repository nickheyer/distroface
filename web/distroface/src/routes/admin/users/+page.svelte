<script lang="ts">
	import { rpc } from '$lib/rpc';
	import { Lister } from '$lib/list.svelte';
	import type { Role, User } from '$lib/proto/distroface/v1/types_pb';
	import { fmtDate } from '$lib/fmt';
	import { session } from '$lib/state/session.svelte';
	import { errata } from '$lib/state/errata.svelte';
	import Leaf from '$lib/bits/Leaf.svelte';
	import Find from '$lib/bits/Find.svelte';
	import Tally from '$lib/bits/Tally.svelte';
	import Mark from '$lib/bits/Mark.svelte';
	import Confirm from '$lib/bits/Confirm.svelte';

	const users = new Lister<User>((page) =>
		rpc.user.listUsers({ page }).then((r) => ({ rows: r.users, page: r.page }))
	);

	let allRoles = $state<Role[]>([]);

	$effect(() => {
		users.first();
		rpc.role.listRoles({ page: { pageSize: 200 } }).then((r) => (allRoles = r.roles));
	});

	let picked = $state<Set<string>>(new Set());

	function togglePick(id: string, on: boolean) {
		const next = new Set(picked);
		if (on) next.add(id);
		else next.delete(id);
		picked = next;
	}

	function pickAll(on: boolean) {
		picked = on ? new Set(users.rows.map((u) => u.id)) : new Set();
	}

	// Editing one account
	let editing = $state<User | null>(null);
	let editEmail = $state('');
	let editActive = $state(true);
	let editRoles = $state<Set<string>>(new Set());
	let busy = $state(false);

	function startEdit(u: User) {
		if (editing?.id === u.id) {
			editing = null;
			return;
		}
		editing = u;
		creating = false;
		editEmail = u.email;
		editActive = u.isActive;
		editRoles = new Set(u.roles.map((r) => r.id));
	}

	function toggleEditRole(id: string, on: boolean) {
		const next = new Set(editRoles);
		if (on) next.add(id);
		else next.delete(id);
		editRoles = next;
	}

	async function saveEdit(e: Event) {
		e.preventDefault();
		if (!editing) return;
		busy = true;
		try {
			await rpc.user.adminUpdateUser({
				userId: editing.id,
				email: editEmail,
				isActive: editActive,
				roleIds: [...editRoles]
			});
			errata.remark(`${editing.username} updated.`);
			editing = null;
			await users.fetch();
		} catch {
			// Interceptor reports
		} finally {
			busy = false;
		}
	}

	async function removeUser(u: User) {
		await rpc.user.adminDeleteUser({ userId: u.id });
		errata.remark(`${u.username} deleted.`);
		await users.fetch();
	}

	// Admitting a new account
	let creating = $state(false);
	let newUsername = $state('');
	let newPassword = $state('');
	let newEmail = $state('');
	let newDisplay = $state('');
	let newRoles = $state<Set<string>>(new Set());
	let newMustChange = $state(true);

	function toggleNewRole(id: string, on: boolean) {
		const next = new Set(newRoles);
		if (on) next.add(id);
		else next.delete(id);
		newRoles = next;
	}

	async function admit(e: Event) {
		e.preventDefault();
		busy = true;
		try {
			await rpc.user.adminCreateUser({
				username: newUsername.trim(),
				password: newPassword,
				email: newEmail.trim(),
				displayName: newDisplay.trim(),
				roleIds: [...newRoles],
				mustChangePassword: newMustChange
			});
			errata.remark(`${newUsername.trim()} created.`);
			creating = false;
			newUsername = '';
			newPassword = '';
			newEmail = '';
			newDisplay = '';
			newRoles = new Set();
			await users.first();
		} catch {
			// Interceptor reports
		} finally {
			busy = false;
		}
	}

	// Bulk acts on the picked set
	let bulkRole = $state('');

	async function bulkActive(on: boolean) {
		const r = await rpc.user.adminBulkUpdateUsers({ userIds: [...picked], isActive: on });
		reportBulk(`${r.updatedCount} ${on ? 'reinstated' : 'suspended'}`, r.errors.length);
		picked = new Set();
		await users.fetch();
	}

	async function bulkRoleChange(add: boolean) {
		if (!bulkRole) return;
		const r = await rpc.user.adminBulkUpdateUsers({
			userIds: [...picked],
			addRoleIds: add ? [bulkRole] : [],
			removeRoleIds: add ? [] : [bulkRole]
		});
		reportBulk(`${r.updatedCount} updated`, r.errors.length);
		picked = new Set();
		await users.fetch();
	}

	async function bulkDelete() {
		const r = await rpc.user.adminBulkDeleteUsers({ userIds: [...picked] });
		reportBulk(`${r.deletedCount} deleted`, r.errors.length);
		picked = new Set();
		await users.first();
	}

	function reportBulk(done: string, faults: number) {
		if (faults > 0) errata.report(`${done}; ${faults} failed.`);
		else errata.remark(`${done}.`);
	}
</script>

<Leaf no="01" title="Users">
	{#snippet aside()}
		<Find lister={users} placeholder="username or email…" />
	{/snippet}

	{#if users.loaded && users.rows.length === 0}
		<p class="vacant">No user accounts yet.</p>
	{:else}
		<div class="ledger-scroll">
			<table class="ledger">
				<thead>
					<tr>
						<th>
							<input
								type="checkbox"
								checked={picked.size > 0 && picked.size === users.rows.length}
								onchange={(e) => pickAll(e.currentTarget.checked)}
								aria-label="pick all"
							/>
						</th>
						<th>User</th>
						<th>Email</th>
						<th>Roles</th>
						<th>Provider</th>
						<th>Status</th>
						<th>Created</th>
						<th class="end">&nbsp;</th>
					</tr>
				</thead>
				<tbody>
					{#each users.rows as u (u.id)}
						<tr>
							<td>
								<input
									type="checkbox"
									checked={picked.has(u.id)}
									onchange={(e) => togglePick(u.id, e.currentTarget.checked)}
									aria-label="pick {u.username}"
								/>
							</td>
							<td>
								<a href="/u/{u.username}">{u.username}</a>
								{#if u.displayName && u.displayName !== u.username}
									<span class="faint">· {u.displayName}</span>
								{/if}
							</td>
							<td class="mono">{u.email || '—'}</td>
							<td><span class="caps soft">{u.roles.map((r) => r.name).join(' · ') || '—'}</span></td>
							<td class="mono soft">{u.authProvider || 'local'}</td>
							<td>
								{#if u.isActive}
									<Mark kind="ok" label="active" />
								{:else}
									<Mark kind="bad" label="suspended" />
								{/if}
								{#if u.mustChangePassword}
									<Mark kind="mid" label="password reset" title="must change password at next sign-in" />
								{/if}
							</td>
							<td class="mono">{fmtDate(u.createdAt)}</td>
							<td class="end">
								<button class="rowact plain" onclick={() => startEdit(u)}>
									{editing?.id === u.id ? 'close' : 'edit'}
								</button>
								{#if u.id !== session.user?.id}
									<Confirm label="delete" onconfirm={() => removeUser(u)} />
								{/if}
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
		<Tally lister={users} unit="users" />
	{/if}

	{#if picked.size > 0}
		<div class="panel">
			<p class="panel-title">{picked.size} selected</p>
			<div class="row">
				<button class="act" onclick={() => bulkActive(true)}>Activate</button>
				<button class="act" onclick={() => bulkActive(false)}>Suspend</button>
				<select bind:value={bulkRole} style="width: auto" aria-label="role">
					<option value="">role…</option>
					{#each allRoles as r (r.id)}
						<option value={r.id}>{r.name}</option>
					{/each}
				</select>
				<button class="act" disabled={!bulkRole} onclick={() => bulkRoleChange(true)}>Grant role</button>
				<button class="act" disabled={!bulkRole} onclick={() => bulkRoleChange(false)}>Remove role</button>
				<Confirm label="delete selected" onconfirm={bulkDelete} />
			</div>
		</div>
	{/if}

	{#if editing}
		<form class="panel" onsubmit={saveEdit}>
			<p class="panel-title">Edit · {editing.username}</p>
			<label class="field">
				<span>Email</span>
				<input type="email" bind:value={editEmail} />
			</label>
			<fieldset class="field">
				<span>Roles</span>
				{#each allRoles as r (r.id)}
					<label class="tick">
						<input
							type="checkbox"
							checked={editRoles.has(r.id)}
							onchange={(e) => toggleEditRole(r.id, e.currentTarget.checked)}
						/>
						{r.name}
						{#if r.description}
							<span class="hint">{r.description}</span>
						{/if}
					</label>
				{/each}
			</fieldset>
			<label class="tick">
				<input type="checkbox" bind:checked={editActive} />
				Active
			</label>
			<div class="row gap-top">
				<button class="act wax" type="submit" disabled={busy}>Save</button>
				<button class="rowact plain" type="button" onclick={() => (editing = null)}>cancel</button>
			</div>
		</form>
	{/if}
</Leaf>

<Leaf no="02" title="New user">
	{#if creating}
		<form class="panel" onsubmit={admit}>
			<div class="row">
				<label class="field" style="flex: 1; min-width: 12rem">
					<span>Username</span>
					<input type="text" bind:value={newUsername} required />
				</label>
				<label class="field" style="flex: 1; min-width: 12rem">
					<span>Password</span>
					<input type="text" bind:value={newPassword} required />
				</label>
			</div>
			<div class="row">
				<label class="field" style="flex: 1; min-width: 12rem">
					<span>Email</span>
					<input type="email" bind:value={newEmail} />
				</label>
				<label class="field" style="flex: 1; min-width: 12rem">
					<span>Display name</span>
					<input type="text" bind:value={newDisplay} />
				</label>
			</div>
			<fieldset class="field">
				<span>Roles</span>
				{#each allRoles as r (r.id)}
					<label class="tick">
						<input
							type="checkbox"
							checked={newRoles.has(r.id)}
							onchange={(e) => toggleNewRole(r.id, e.currentTarget.checked)}
						/>
						{r.name}
					</label>
				{/each}
			</fieldset>
			<label class="tick">
				<input type="checkbox" bind:checked={newMustChange} />
				Require password change at first sign-in
			</label>
			<div class="row gap-top">
				<button class="act wax" type="submit" disabled={busy || !newUsername.trim() || !newPassword}>
					Create user
				</button>
				<button class="rowact plain" type="button" onclick={() => (creating = false)}>cancel</button>
			</div>
		</form>
	{:else}
		<button class="act" onclick={() => { creating = true; editing = null; }}>Create user</button>
	{/if}
</Leaf>

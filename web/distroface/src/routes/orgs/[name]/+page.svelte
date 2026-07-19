<script lang="ts">
	import { getContext } from 'svelte';
	import { goto } from '$app/navigation';
	import { rpc } from '$lib/rpc';
	import { Lister } from '$lib/list.svelte';
	import { OrgRole, type OrgMember } from '$lib/proto/distroface/v1/types_pb';
	import { fmtDate, orgRoleLabel } from '$lib/fmt';
	import { OrgCtx, ORG_CTX } from '$lib/state/orgctx.svelte';
	import { errata } from '$lib/state/errata.svelte';
	import Leaf from '$lib/bits/Leaf.svelte';
	import Tally from '$lib/bits/Tally.svelte';
	import Confirm from '$lib/bits/Confirm.svelte';

	const ctx = getContext<OrgCtx>(ORG_CTX);

	const members = new Lister<OrgMember>((page) =>
		rpc.organization
			.listOrgMembers({ page, orgId: ctx.org?.id ?? '' })
			.then((r) => ({ rows: r.members, page: r.page }))
	);

	$effect(() => {
		if (ctx.org) members.first();
	});

	let addName = $state('');
	let addRole = $state<OrgRole>(OrgRole.MEMBER);
	let addBusy = $state(false);

	async function addMember(e: Event) {
		e.preventDefault();
		if (!ctx.org) return;
		addBusy = true;
		try {
			const u = await rpc.user.getUser({ username: addName.trim() });
			if (!u.user) throw new Error('No such user.');
			await rpc.organization.addOrgMember({ orgId: ctx.org.id, userId: u.user.id, role: addRole });
			errata.remark(`${addName.trim()} added.`);
			addName = '';
			await members.fetch();
			await ctx.refresh();
		} catch {
			// Interceptor reports
		} finally {
			addBusy = false;
		}
	}

	async function setRole(m: OrgMember, role: OrgRole) {
		if (!ctx.org) return;
		await rpc.organization.updateOrgMemberRole({ orgId: ctx.org.id, userId: m.userId, role });
		await members.fetch();
	}

	async function removeMember(m: OrgMember) {
		if (!ctx.org) return;
		await rpc.organization.removeOrgMember({ orgId: ctx.org.id, userId: m.userId });
		errata.remark(`${m.username} removed.`);
		await members.fetch();
		await ctx.refresh();
	}

	async function transferTo(m: OrgMember) {
		if (!ctx.org) return;
		await rpc.organization.transferOrgOwnership({ orgId: ctx.org.id, userId: m.userId });
		errata.remark(`Ownership transferred to ${m.username}.`);
		await members.fetch();
		await ctx.refresh();
	}

	let editDisplay = $state('');
	let editDesc = $state('');
	let editBusy = $state(false);

	$effect(() => {
		if (ctx.org) {
			editDisplay = ctx.org.displayName;
			editDesc = ctx.org.description;
		}
	});

	async function amend(e: Event) {
		e.preventDefault();
		if (!ctx.org) return;
		editBusy = true;
		try {
			await rpc.organization.updateOrganization({
				id: ctx.org.id,
				displayName: editDisplay,
				description: editDesc
			});
			errata.remark('Details saved.');
			await ctx.refresh();
		} catch {
			// Interceptor reports
		} finally {
			editBusy = false;
		}
	}

	async function dissolve() {
		if (!ctx.org) return;
		await rpc.organization.deleteOrganization({ id: ctx.org.id });
		errata.remark(`${ctx.org.name} deleted.`);
		goto('/orgs');
	}
</script>

<Leaf no="01" title="Details">
	<dl class="docket" style="max-width: 40rem">
		<dt>Name</dt>
		<dd class="mono">{ctx.org?.name}</dd>
		<dt>Members</dt>
		<dd class="mono">{ctx.org?.memberCount}</dd>
		<dt>Created</dt>
		<dd class="mono">{fmtDate(ctx.org?.createdAt)}</dd>
		<dt>Your role</dt>
		<dd>
			<span class="caps soft"
				>{ctx.org && ctx.org.currentUserRole !== OrgRole.UNSPECIFIED
					? orgRoleLabel[ctx.org.currentUserRole]
					: 'none'}</span>
		</dd>
	</dl>
</Leaf>

<Leaf no="02" title="Members">
	{#if members.loaded && members.rows.length === 0}
		<p class="vacant">No members yet.</p>
	{:else}
		<div class="ledger-scroll">
			<table class="ledger">
				<thead>
					<tr>
						<th>Member</th>
						<th>Role</th>
						<th>Joined</th>
						{#if ctx.isAdmin}
							<th class="end">&nbsp;</th>
						{/if}
					</tr>
				</thead>
				<tbody>
					{#each members.rows as m (m.userId)}
						<tr>
							<td><a href="/u/{m.username}">{m.username}</a></td>
							<td>
								{#if ctx.isAdmin && m.role !== OrgRole.OWNER}
									<select
										style="width: auto"
										value={m.role}
										onchange={(e) => setRole(m, Number(e.currentTarget.value) as OrgRole)}
									>
										<option value={OrgRole.ADMIN}>admin</option>
										<option value={OrgRole.MEMBER}>member</option>
									</select>
								{:else}
									<span class="caps soft">{orgRoleLabel[m.role]}</span>
								{/if}
							</td>
							<td class="mono">{fmtDate(m.joinedAt)}</td>
							{#if ctx.isAdmin}
								<td class="end">
									{#if m.role !== OrgRole.OWNER}
										{#if ctx.isOwner}
											<Confirm label="transfer ownership" onconfirm={() => transferTo(m)} />
										{/if}
										<Confirm label="remove" onconfirm={() => removeMember(m)} />
									{/if}
								</td>
							{/if}
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
		<Tally lister={members} unit="members" />
	{/if}

	{#if ctx.isAdmin}
		<form class="row gap-top" onsubmit={addMember}>
			<input
				type="text"
				style="width: 13rem"
				placeholder="username…"
				bind:value={addName}
				aria-label="username"
			/>
			<select bind:value={addRole} style="width: auto" aria-label="role">
				<option value={OrgRole.MEMBER}>member</option>
				<option value={OrgRole.ADMIN}>admin</option>
			</select>
			<button class="act" type="submit" disabled={addBusy || !addName.trim()}>Add member</button>
		</form>
	{/if}
</Leaf>

{#if ctx.isAdmin}
	<Leaf no="03" title="Edit details">
		<form onsubmit={amend}>
			<label class="field">
				<span>Display name</span>
				<input type="text" bind:value={editDisplay} />
			</label>
			<label class="field">
				<span>Description</span>
				<textarea rows="2" bind:value={editDesc}></textarea>
			</label>
			<button class="act" type="submit" disabled={editBusy}>Save</button>
		</form>
	</Leaf>
{/if}

{#if ctx.isOwner}
	<Leaf no="04" title="Delete organization">
		<p class="note">
			Deleting the organization removes its members, portals, and trust material. Repositories in
			its namespace become unreachable.
		</p>
		<div class="gap-top">
			<Confirm label="delete organization" onconfirm={dissolve} />
		</div>
	</Leaf>
{/if}

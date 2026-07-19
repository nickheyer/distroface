<script lang="ts">
	import { goto } from '$app/navigation';
	import { rpc } from '$lib/rpc';
	import { Lister } from '$lib/list.svelte';
	import { OrgRole, type Organization } from '$lib/proto/distroface/v1/types_pb';
	import { fmtDate, orgRoleLabel } from '$lib/fmt';
	import { session } from '$lib/state/session.svelte';
	import { errata } from '$lib/state/errata.svelte';
	import Find from '$lib/bits/Find.svelte';
	import Tally from '$lib/bits/Tally.svelte';
	import Leaf from '$lib/bits/Leaf.svelte';

	const orgs = new Lister<Organization>((page) =>
		rpc.organization.listOrganizations({ page }).then((r) => ({ rows: r.organizations, page: r.page }))
	);

	$effect(() => {
		orgs.first();
	});

	let formOpen = $state(false);
	let newName = $state('');
	let newDisplay = $state('');
	let newDesc = $state('');
	let busy = $state(false);

	async function createOrg(e: Event) {
		e.preventDefault();
		busy = true;
		try {
			const r = await rpc.organization.createOrganization({
				name: newName.trim(),
				displayName: newDisplay.trim(),
				description: newDesc
			});
			errata.remark(`Organization ${newName.trim()} created.`);
			goto(`/orgs/${r.organization?.name ?? newName.trim()}`);
		} catch {
			// Interceptor reports
		} finally {
			busy = false;
		}
	}
</script>

<hgroup class="folio">
	<p class="kicker">Distroface</p>
	<h1>Organizations</h1>
	<p class="sub">
		Organizations hold repositories in their own namespace, run portals, and manage their own
		trust.
	</p>
</hgroup>

<Leaf no="01" title="All organizations">
	{#snippet aside()}
		<Find lister={orgs} placeholder="organization…" />
	{/snippet}

	{#if orgs.loaded && orgs.rows.length === 0}
		<p class="vacant">No organizations yet.</p>
	{:else}
		<div class="ledger-scroll">
			<table class="ledger">
				<thead>
					<tr>
						<th>Organization</th>
						<th class="num">Members</th>
						<th>Your role</th>
						<th>Created</th>
					</tr>
				</thead>
				<tbody>
					{#each orgs.rows as org (org.id)}
						<tr>
							<td>
								<a href="/orgs/{org.name}">{org.displayName || org.name}</a>
								<span class="mono faint">·&nbsp;{org.name}</span>
								{#if org.description}
									<div class="note" style="font-size: 0.8125rem">{org.description}</div>
								{/if}
							</td>
							<td class="num mono">{org.memberCount}</td>
							<td>
								{#if org.currentUserRole !== OrgRole.UNSPECIFIED}
									<span class="caps soft">{orgRoleLabel[org.currentUserRole]}</span>
								{:else}
									<span class="faint">—</span>
								{/if}
							</td>
							<td class="mono">{fmtDate(org.createdAt)}</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
		<Tally lister={orgs} unit="organizations" />
	{/if}
</Leaf>

{#if session.signedIn && session.canCreateOrgs}
	<Leaf no="02" title="New organization">
		{#if formOpen}
			<form class="panel" onsubmit={createOrg}>
				<label class="field">
					<span>Name</span>
					<input type="text" bind:value={newName} required placeholder="lowercase, becomes the namespace" />
				</label>
				<label class="field">
					<span>Display name</span>
					<input type="text" bind:value={newDisplay} />
				</label>
				<label class="field">
					<span>Description</span>
					<textarea rows="2" bind:value={newDesc}></textarea>
				</label>
				<div class="row gap-top">
					<button class="act wax" type="submit" disabled={busy || !newName.trim()}
						>Create organization</button>
					<button class="rowact plain" type="button" onclick={() => (formOpen = false)}>cancel</button>
				</div>
			</form>
		{:else}
			<button class="act" onclick={() => (formOpen = true)}>New organization</button>
		{/if}
	</Leaf>
{/if}

<script lang="ts">
	import { rpc } from '$lib/rpc';
	import { Lister } from '$lib/list.svelte';
	import type { Permission, Role } from '$lib/proto/distroface/v1/types_pb';
	import type { ResourceActions, ScopeableObject } from '$lib/proto/distroface/v1/role_pb';
	import { errata } from '$lib/state/errata.svelte';
	import Leaf from '$lib/bits/Leaf.svelte';
	import Tally from '$lib/bits/Tally.svelte';
	import Confirm from '$lib/bits/Confirm.svelte';

	const roles = new Lister<Role>((page) =>
		rpc.role.listRoles({ page }).then((r) => ({ rows: r.roles, page: r.page }))
	);

	let matrix = $state<ResourceActions[]>([]);
	let rolePerms = $state<Record<string, Permission[]>>({});

	async function loadMatrix() {
		const r = await rpc.role.getPermissionMatrix({});
		matrix = r.resourceActions;
		const perms: Record<string, Permission[]> = {};
		for (const [roleId, rp] of Object.entries(r.rolePermissions)) perms[roleId] = rp.permissions;
		rolePerms = perms;
	}

	$effect(() => {
		roles.first();
		loadMatrix();
	});

	// The grants desk for one role
	let desk = $state<Role | null>(null);
	let global = $state<Set<string>>(new Set());
	let scoped = $state<{ resource: string; action: string; objectId: string }[]>([]);
	let sweeping = $state<{ resource: string; action: string; objectId: string }[]>([]);
	let busy = $state(false);

	function isGlobal(objectId: string): boolean {
		return objectId === '' || objectId === '*';
	}

	function isWildcard(p: Permission): boolean {
		return p.resource === '*' || p.action === '*';
	}

	function openDesk(role: Role) {
		if (desk?.id === role.id) {
			desk = null;
			return;
		}
		desk = role;
		const perms = rolePerms[role.id] ?? role.permissions;
		sweeping = perms
			.filter((p) => isWildcard(p))
			.map((p) => ({ resource: p.resource, action: p.action, objectId: p.objectId }));
		global = new Set(
			perms
				.filter((p) => isGlobal(p.objectId) && !isWildcard(p))
				.map((p) => `${p.resource}|${p.action}`)
		);
		scoped = perms
			.filter((p) => !isGlobal(p.objectId) && !isWildcard(p))
			.map((p) => ({ resource: p.resource, action: p.action, objectId: p.objectId }));
	}

	function dropSweeping(i: number) {
		sweeping = sweeping.filter((_, j) => j !== i);
	}

	function toggleGlobal(resource: string, action: string, on: boolean) {
		const key = `${resource}|${action}`;
		const next = new Set(global);
		if (on) next.add(key);
		else next.delete(key);
		global = next;
	}

	// Adding an object-scoped grant
	let pickResource = $state('');
	let pickAction = $state('');
	let pickObject = $state('');
	let objects = $state<ScopeableObject[]>([]);

	$effect(() => {
		if (!pickResource) {
			objects = [];
			return;
		}
		rpc.role
			.listScopeableObjects({ page: { pageSize: 200 }, resource: pickResource })
			.then((r) => (objects = r.objects));
	});

	function addScoped() {
		if (!pickResource || !pickAction || !pickObject) return;
		scoped = [...scoped, { resource: pickResource, action: pickAction, objectId: pickObject }];
		pickObject = '';
	}

	function dropScoped(i: number) {
		scoped = scoped.filter((_, j) => j !== i);
	}

	async function saveDesk() {
		if (!desk) return;
		busy = true;
		try {
			const permissions: Pick<Permission, 'resource' | 'action' | 'objectId'>[] = [
				...sweeping,
				...[...global].map((key) => {
					const [resource, action] = key.split('|');
					return { resource, action, objectId: '' };
				}),
				...scoped
			];
			await rpc.role.updatePermissions({ roleId: desk.id, permissions });
			errata.remark(`Permissions for ${desk.name} saved.`);
			await loadMatrix();
		} catch {
			// Interceptor reports
		} finally {
			busy = false;
		}
	}

	async function removeRole(role: Role) {
		await rpc.role.deleteRole({ id: role.id });
		errata.remark(`Role ${role.name} deleted.`);
		if (desk?.id === role.id) desk = null;
		await roles.first();
		await loadMatrix();
	}

	// Chartering a role
	let creating = $state(false);
	let newName = $state('');
	let newDesc = $state('');
	let newDefault = $state(false);

	async function createRole(e: Event) {
		e.preventDefault();
		busy = true;
		try {
			await rpc.role.createRole({
				name: newName.trim(),
				description: newDesc,
				isDefault: newDefault,
				permissions: []
			});
			errata.remark(`Role ${newName.trim()} created.`);
			creating = false;
			newName = '';
			newDesc = '';
			newDefault = false;
			await roles.first();
			await loadMatrix();
		} catch {
			// Interceptor reports
		} finally {
			busy = false;
		}
	}

	let editDesc = $state('');
	let editDefault = $state(false);

	$effect(() => {
		if (desk) {
			editDesc = desk.description;
			editDefault = desk.isDefault;
		}
	});

	async function saveRoleMeta() {
		if (!desk) return;
		await rpc.role.updateRole({ id: desk.id, description: editDesc, isDefault: editDefault });
		errata.remark('Role updated.');
		await roles.fetch();
	}

	const actionsOf = $derived((resource: string) => matrix.find((m) => m.resource === resource)?.actions ?? []);
</script>

<Leaf no="01" title="Roles">
	{#if roles.loaded && roles.rows.length === 0}
		<p class="vacant">No roles yet.</p>
	{:else}
		<div class="ledger-scroll">
			<table class="ledger">
				<thead>
					<tr>
						<th>Role</th>
						<th>Description</th>
						<th>Type</th>
						<th class="end">&nbsp;</th>
					</tr>
				</thead>
				<tbody>
					{#each roles.rows as role (role.id)}
						<tr>
							<td><b>{role.name}</b></td>
							<td class="note">{role.description || ''}</td>
							<td>
								<span class="caps soft">
									{role.isSystem ? 'system' : 'custom'}{role.isDefault ? ' · assigned to new users' : ''}
								</span>
							</td>
							<td class="end">
								<button class="rowact plain" onclick={() => openDesk(role)}>
									{desk?.id === role.id ? 'close' : 'permissions'}
								</button>
								{#if !role.isSystem}
									<Confirm label="delete" onconfirm={() => removeRole(role)} />
								{/if}
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
		<Tally lister={roles} unit="roles" />
	{/if}

	{#if creating}
		<form class="panel" onsubmit={createRole}>
			<p class="panel-title">New role</p>
			<label class="field">
				<span>Name</span>
				<input type="text" bind:value={newName} required />
			</label>
			<label class="field">
				<span>Description</span>
				<input type="text" bind:value={newDesc} />
			</label>
			<label class="tick">
				<input type="checkbox" bind:checked={newDefault} />
				Assigned to new users
			</label>
			<div class="row gap-top">
				<button class="act wax" type="submit" disabled={busy || !newName.trim()}>Create role</button>
				<button class="rowact plain" type="button" onclick={() => (creating = false)}>cancel</button>
			</div>
		</form>
	{:else}
		<div class="gap-top">
			<button class="act" onclick={() => (creating = true)}>New role</button>
		</div>
	{/if}
</Leaf>

{#if desk}
	<Leaf no="02" title="Permissions · {desk.name}">
		{#if !desk.isSystem}
			<div class="row foot" style="margin-bottom: 1rem">
				<label class="field" style="margin: 0; flex: 1; min-width: 16rem">
					<span>Description</span>
					<input type="text" bind:value={editDesc} />
				</label>
				<label class="tick">
					<input type="checkbox" bind:checked={editDefault} />
					Assigned to new users
				</label>
				<button class="act" onclick={saveRoleMeta}>Save</button>
			</div>
		{/if}

		{#if sweeping.length > 0}
			<div class="panel" style="margin-top: 0">
				<p class="panel-title">Wildcard permissions</p>
				<p class="note" style="margin-bottom: 0.6rem">
					Wildcards cover whole resources or every action. They apply in addition to the matrix
					below and are kept when saving.
				</p>
				{#each sweeping as g, i (g.resource + g.action + g.objectId)}
					<div class="row" style="margin-bottom: 0.3rem">
						<span class="mono"
							>{g.resource === '*' ? 'every resource' : g.resource} × {g.action === '*'
								? 'every action'
								: g.action}{g.objectId && g.objectId !== '*' ? ` on ${g.objectId}` : ''}</span>
						{#if !desk.isSystem}
							<button class="rowact plain" onclick={() => dropSweeping(i)}>drop</button>
						{/if}
					</div>
				{/each}
			</div>
		{/if}

		<div class="ledger-scroll">
			<table class="ledger">
				<thead>
					<tr>
						<th>Resource</th>
						<th>Actions granted on all objects</th>
					</tr>
				</thead>
				<tbody>
					{#each matrix as ra (ra.resource)}
						<tr>
							<td class="mono">{ra.resource}</td>
							<td>
								<span class="row" style="gap: 1.1rem">
									{#each ra.actions as action (action)}
										<label class="tick" style="margin: 0">
											<input
												type="checkbox"
												checked={global.has(`${ra.resource}|${action}`)}
												onchange={(e) => toggleGlobal(ra.resource, action, e.currentTarget.checked)}
											/>
											{action}
										</label>
									{/each}
								</span>
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>

		<div class="panel">
			<p class="panel-title">Object-scoped permissions</p>
			{#if scoped.length === 0}
				<p class="note">None. Everything above applies to all objects of its resource.</p>
			{:else}
				<div class="ledger-scroll">
					<table class="ledger">
						<thead>
							<tr>
								<th>Resource</th>
								<th>Action</th>
								<th>Object</th>
								<th class="end">&nbsp;</th>
							</tr>
						</thead>
						<tbody>
							{#each scoped as g, i (g.resource + g.action + g.objectId)}
								<tr>
									<td class="mono">{g.resource}</td>
									<td class="mono">{g.action}</td>
									<td class="mono">{g.objectId}</td>
									<td class="end">
										<button class="rowact plain" onclick={() => dropScoped(i)}>drop</button>
									</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			{/if}

			<div class="row gap-top">
				<select bind:value={pickResource} style="width: auto" aria-label="resource">
					<option value="">resource…</option>
					{#each matrix as ra (ra.resource)}
						<option value={ra.resource}>{ra.resource}</option>
					{/each}
				</select>
				<select bind:value={pickAction} style="width: auto" aria-label="action" disabled={!pickResource}>
					<option value="">action…</option>
					{#each actionsOf(pickResource) as action (action)}
						<option value={action}>{action}</option>
					{/each}
				</select>
				<select bind:value={pickObject} style="width: auto; max-width: 18rem" aria-label="object" disabled={!pickResource}>
					<option value="">object…</option>
					{#each objects as o (o.id)}
						<option value={o.id}>{o.name} ({o.scopeSource})</option>
					{/each}
				</select>
				<button class="act" disabled={!pickResource || !pickAction || !pickObject} onclick={addScoped}>
					Add
				</button>
			</div>
		</div>

		<div class="row gap-top">
			<button class="act wax" disabled={busy} onclick={saveDesk}>Save permissions</button>
			<span class="note">Saving replaces the role's entire permission list with what is shown.</span>
		</div>
	</Leaf>
{/if}

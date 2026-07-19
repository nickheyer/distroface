<script lang="ts">
	import { rpc, hush } from '$lib/rpc';
	import { fmtCount } from '$lib/fmt';
	import { session } from '$lib/state/session.svelte';

	const contents = $derived(
		[
			session.canReadUsers
				? { href: '/admin/users', title: 'Users', what: 'Create, edit, suspend, and delete accounts.' }
				: null,
			session.canReadRoles
				? { href: '/admin/roles', title: 'Roles', what: 'Roles and the permissions they grant.' }
				: null,
			session.canReadSettings
				? { href: '/admin/invites', title: 'Invites', what: 'Registration invites, their PINs, uses, and expiry.' }
				: null,
			session.canManageSettings
				? { href: '/admin/trust', title: 'Trust', what: 'The instance root CA, the primary certificate, registered hostnames, and approvals.' }
				: null,
			session.canReadSettings
				? { href: '/admin/settings', title: 'Settings', what: 'Every runtime setting, with where each value comes from.' }
				: null,
			session.canManageSettings
				? { href: '/admin/storage', title: 'Storage', what: 'Disk usage and garbage collection.' }
				: null,
			session.canManageSettings
				? { href: '/admin/audit', title: 'Audit', what: 'The log of security-relevant actions.' }
				: null
		].filter((s) => s !== null)
	);

	let counts = $state<{ label: string; value: number }[]>([]);

	// One row per list rpc, only the totals are read
	$effect(() => {
		const page = { pageSize: 1 };
		const wants: [string, Promise<number>][] = [];
		if (session.canReadUsers) {
			wants.push(['users', rpc.user.listUsers({ page }, hush).then((r) => Number(r.page?.totalCount ?? 0n))]);
		}
		wants.push(['organizations', rpc.organization.listOrganizations({ page }, hush).then((r) => Number(r.page?.totalCount ?? 0n))]);
		wants.push(['repositories', rpc.repository.listRepositories({ page }, hush).then((r) => Number(r.page?.totalCount ?? 0n))]);
		if (session.canReadRoles) {
			wants.push(['roles', rpc.role.listRoles({ page }, hush).then((r) => Number(r.page?.totalCount ?? 0n))]);
		}
		Promise.allSettled(wants.map(([, p]) => p)).then((results) => {
			counts = results
				.map((r, i) => (r.status === 'fulfilled' ? { label: wants[i][0], value: r.value } : null))
				.filter((c) => c !== null);
		});
	});
</script>

{#if counts.length > 0}
	<section class="leaf">
		<span class="no">§</span>
		<div class="body">
			<header class="leaf-head"><h2>At a glance</h2></header>
			<div class="row" style="gap: 2.6rem">
				{#each counts as c (c.label)}
					<div>
						<span class="mono" style="font-size: 1.3rem">{fmtCount(c.value)}</span>
						<span class="caps faint" style="margin-left: 0.4rem">{c.label}</span>
					</div>
				{/each}
			</div>
		</div>
	</section>
{/if}

<section class="leaf">
	<span class="no">§</span>
	<div class="body">
		<header class="leaf-head"><h2>Contents</h2></header>
		<dl class="docket" style="max-width: 46rem">
			{#each contents as c (c.href)}
				<dt><a href={c.href} style="font-family: var(--mono)">{c.title}</a></dt>
				<dd class="note">{c.what}</dd>
			{/each}
		</dl>
	</div>
</section>

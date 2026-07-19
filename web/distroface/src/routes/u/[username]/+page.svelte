<script lang="ts">
	import { page } from '$app/state';
	import { rpc, hush } from '$lib/rpc';
	import { Lister } from '$lib/list.svelte';
	import type { Repository, User } from '$lib/proto/distroface/v1/types_pb';
	import { fmtBytes, fmtCount, fmtDate, visibilityLabel } from '$lib/fmt';
	import Leaf from '$lib/bits/Leaf.svelte';
	import Tally from '$lib/bits/Tally.svelte';

	const username = $derived(page.params.username!);

	let user = $state<User | null>(null);
	let missing = $state(false);

	const repos = new Lister<Repository>((p) =>
		rpc.repository
			.listRepositories({ page: p, namespace: username })
			.then((r) => ({ rows: r.repositories, page: r.page }))
	);

	$effect(() => {
		void username;
		user = null;
		missing = false;
		rpc.user
			.getUser({ username }, hush)
			.then((r) => (user = r.user ?? null))
			.catch(() => (missing = true));
		repos.first();
	});
</script>

{#if missing}
	<hgroup class="folio">
		<p class="kicker">Users</p>
		<h1>Not found</h1>
		<p class="sub">
			No user named <span class="mono">{username}</span> exists here. Back to the
			<a href="/">registry</a>.
		</p>
	</hgroup>
{:else if user}
	<hgroup class="folio">
		<p class="kicker">Users</p>
		<h1>{user.displayName || user.username}</h1>
		<p class="sub">
			<span class="mono">{user.username}</span>
			{#if user.createdAt}· joined {fmtDate(user.createdAt)}{/if}
			{#if user.roles.length}· {user.roles.map((r) => r.name).join(', ')}{/if}
		</p>
	</hgroup>

	<Leaf no="01" title="Repositories">
		{#if repos.loaded && repos.rows.length === 0}
			<p class="vacant">No repositories in this namespace.</p>
		{:else}
			<div class="ledger-scroll">
				<table class="ledger">
					<thead>
						<tr>
							<th>Repository</th>
							<th>Visibility</th>
							<th class="num">Tags</th>
							<th class="num">Size</th>
							<th class="num">Pulls</th>
							<th>Last push</th>
						</tr>
					</thead>
					<tbody>
						{#each repos.rows as repo (repo.id)}
							<tr>
								<td>
									<a href="/r/{repo.namespace}/{repo.name}">{repo.fullName}</a>
									{#if repo.description}
										<div class="note" style="font-size: 0.8125rem">{repo.description}</div>
									{/if}
								</td>
								<td><span class="caps soft">{visibilityLabel[repo.visibility]}</span></td>
								<td class="num mono">{repo.tagCount}</td>
								<td class="num mono">{fmtBytes(repo.sizeBytes)}</td>
								<td class="num mono">{fmtCount(repo.pullCount)}</td>
								<td class="mono">{fmtDate(repo.lastPushedAt)}</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
			<Tally lister={repos} unit="repositories" />
		{/if}
		<p class="note gap-top">
			<a href="/artifacts?ns={username}">Artifact repositories in this namespace →</a>
		</p>
	</Leaf>
{:else}
	<p class="working" style="margin-top: 4rem">loading</p>
{/if}

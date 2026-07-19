<script lang="ts">
	import { page } from '$app/state';
	import { rpc } from '$lib/rpc';
	import { Lister } from '$lib/list.svelte';
	import { Visibility, type Repository } from '$lib/proto/distroface/v1/types_pb';
	import { fmtBytes, fmtCount, fmtDate, visibilityLabel } from '$lib/fmt';
	import { session } from '$lib/state/session.svelte';
	import { gate, site } from '$lib/state/site.svelte';
	import Find from '$lib/bits/Find.svelte';
	import Tally from '$lib/bits/Tally.svelte';
	import Leaf from '$lib/bits/Leaf.svelte';
	import Copy from '$lib/bits/Copy.svelte';

	let namespace = $state(page.url.searchParams.get('ns') ?? '');
	let visibility = $state<Visibility>(Visibility.UNSPECIFIED);
	let shelf = $state<'all' | 'starred'>('all');

	const repos = new Lister<Repository>((page) => {
		if (shelf === 'starred') {
			return rpc.repository
				.listStarredRepositories({ page })
				.then((r) => ({ rows: r.repositories, page: r.page }));
		}
		return rpc.repository
			.listRepositories({
				page,
				namespace: gate.isPortal ? gate.orgName : namespace.trim(),
				visibility
			})
			.then((r) => ({ rows: r.repositories, page: r.page }));
	});

	$effect(() => {
		void visibility;
		void shelf;
		void namespace;
		repos.first();
	});

	async function toggleStar(repo: Repository) {
		const req = { namespace: repo.namespace, name: repo.name };
		const r = repo.isStarred
			? await rpc.repository.unstarRepository(req)
			: await rpc.repository.starRepository(req);
		repo.isStarred = !repo.isStarred;
		repo.starCount = r.starCount;
	}

	const host = $derived(gate.host());
	const exampleRef = $derived(
		gate.isPortal && gate.mapUnqualified ? 'image' : 'namespace/image'
	);
</script>

<hgroup class="folio">
	<p class="kicker">{gate.isPortal ? `Portal · ${gate.portalName}` : site.publicHostname}</p>
	<h1>{gate.isPortal ? gate.displayName : 'Registry index'}</h1>
	{#if gate.isPortal}
		<p class="sub">
			Container repositories of {gate.displayName}, served at <span class="mono">{host}</span
			>{gate.allowPush ? '' : ' - read-only'}.
		</p>
	{:else}
		<p class="sub">Container repositories hosted on this instance.</p>
	{/if}
</hgroup>

<Leaf no="01" title="Repositories">
	{#snippet aside()}
		{#if session.signedIn && !gate.isPortal}
			<button
				class="rowact plain"
				style:color={shelf === 'starred' ? 'var(--wax)' : undefined}
				onclick={() => (shelf = shelf === 'starred' ? 'all' : 'starred')}
				>★ starred only</button>
		{/if}
		{#if !gate.isPortal}
			<input
				type="text"
				style="width: 9.5rem"
				placeholder="namespace…"
				bind:value={namespace}
				aria-label="namespace"
			/>
		{/if}
		<select bind:value={visibility} style="width: auto" aria-label="visibility">
			<option value={Visibility.UNSPECIFIED}>any visibility</option>
			<option value={Visibility.PUBLIC}>public</option>
			<option value={Visibility.PRIVATE}>private</option>
		</select>
		<Find lister={repos} placeholder="repository…" />
	{/snippet}

	{#if repos.loaded && repos.rows.length === 0}
		<p class="vacant">
			{shelf === 'starred' ? 'Nothing starred yet.' : 'No repositories yet.'}
		</p>
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
						<th class="end">★</th>
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
							<td class="end">
								{#if session.signedIn}
									<button
										class="rowact plain"
										title={repo.isStarred ? 'unstar' : 'star'}
										style="text-decoration: none; font-size: 0.85rem"
										style:color={repo.isStarred ? 'var(--wax)' : undefined}
										onclick={() => toggleStar(repo)}>{repo.isStarred ? '★' : '☆'} {fmtCount(repo.starCount)}</button>
								{:else}
									<span class="mono faint">★ {fmtCount(repo.starCount)}</span>
								{/if}
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
		<Tally lister={repos} unit="repositories" />
	{/if}
</Leaf>

{#if !gate.isPortal || gate.allowPush}
	<Leaf no="02" title="Publishing">
		<p class="note">
			Repositories are created by pushing. A repository appears in the list after its first push.
		</p>
		<div class="stack gap-top">
			<div class="cmdline">
				docker login {host}
				<Copy text={`docker login ${host}`} />
			</div>
			<div class="cmdline">
				docker push {host}/{exampleRef}:tag
				<Copy text={`docker push ${host}/${exampleRef}:tag`} />
			</div>
		</div>
	</Leaf>
{/if}

<script lang="ts">
	import { page } from '$app/state';
	import { rpc } from '$lib/rpc';
	import { Lister } from '$lib/list.svelte';
	import type { ArtifactRepository } from '$lib/proto/distroface/v1/types_pb';
	import { fmtBytes, fmtCount, fmtDate } from '$lib/fmt';
	import { session } from '$lib/state/session.svelte';
	import { errata } from '$lib/state/errata.svelte';
	import Find from '$lib/bits/Find.svelte';
	import Tally from '$lib/bits/Tally.svelte';
	import Leaf from '$lib/bits/Leaf.svelte';

	let namespace = $state(page.url.searchParams.get('ns') ?? '');

	const repos = new Lister<ArtifactRepository>((page) =>
		rpc.artifact
			.listArtifactRepositories({ page, namespace: namespace.trim() })
			.then((r) => ({ rows: r.repositories, page: r.page }))
	);

	$effect(() => {
		void namespace;
		repos.first();
	});

	let formOpen = $state(false);
	let newName = $state('');
	let newNamespace = $state('');
	let newDesc = $state('');
	let newPrivate = $state(true);
	let busy = $state(false);

	async function createRepo(e: Event) {
		e.preventDefault();
		busy = true;
		try {
			await rpc.artifact.createArtifactRepository({
				name: newName.trim(),
				namespace: newNamespace.trim(),
				description: newDesc,
				isPrivate: newPrivate
			});
			errata.remark(`Repository ${newName.trim()} created.`);
			formOpen = false;
			newName = '';
			newDesc = '';
			await repos.first();
		} catch {
			// Interceptor reports
		} finally {
			busy = false;
		}
	}
</script>

<hgroup class="folio">
	<p class="kicker">Distroface</p>
	<h1>Artifact repositories</h1>
	<p class="sub">General files hosted by this instance: packages, bundles, reports, anything with a version.</p>
</hgroup>

<Leaf no="01" title="Repositories">
	{#snippet aside()}
		<input
			type="text"
			style="width: 9.5rem"
			placeholder="namespace…"
			bind:value={namespace}
			aria-label="namespace"
		/>
		<Find lister={repos} placeholder="repository…" />
	{/snippet}

	{#if repos.loaded && repos.rows.length === 0}
		<p class="vacant">No artifact repositories yet.</p>
	{:else}
		<div class="ledger-scroll">
			<table class="ledger">
				<thead>
					<tr>
						<th>Repository</th>
						<th>Visibility</th>
						<th class="num">Artifacts</th>
						<th class="num">Held</th>
						<th>Updated</th>
					</tr>
				</thead>
				<tbody>
					{#each repos.rows as repo (repo.id)}
						<tr>
							<td>
								<a href="/artifacts/{repo.namespace}/{repo.name}">{repo.fullName}</a>
								{#if repo.description}
									<div class="note" style="font-size: 0.8125rem">{repo.description}</div>
								{/if}
							</td>
							<td><span class="caps soft">{repo.isPrivate ? 'private' : 'public'}</span></td>
							<td class="num mono">{fmtCount(repo.artifactCount)}</td>
							<td class="num mono">{fmtBytes(repo.totalSize)}</td>
							<td class="mono">{fmtDate(repo.updatedAt)}</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
		<Tally lister={repos} unit="repositories" />
	{/if}
</Leaf>

{#if session.signedIn}
	<Leaf no="02" title="New repository">
		{#if formOpen}
			<form class="panel" onsubmit={createRepo}>
				<label class="field">
					<span>Name</span>
					<input type="text" bind:value={newName} required />
				</label>
				<label class="field">
					<span>Namespace</span>
					<input type="text" bind:value={newNamespace} placeholder={session.user?.username} />
					<span class="hint">Your username, or an organization you belong to. Empty means yours.</span>
				</label>
				<label class="field">
					<span>Description</span>
					<textarea rows="2" bind:value={newDesc}></textarea>
				</label>
				<label class="tick">
					<input type="checkbox" bind:checked={newPrivate} />
					Private
					<span class="hint">Private repositories require credentials to read.</span>
				</label>
				<div class="row gap-top">
					<button class="act wax" type="submit" disabled={busy || !newName.trim()}
						>Create repository</button>
					<button class="rowact plain" type="button" onclick={() => (formOpen = false)}>cancel</button>
				</div>
			</form>
		{:else}
			<button class="act" onclick={() => (formOpen = true)}>New repository</button>
		{/if}
	</Leaf>
{/if}

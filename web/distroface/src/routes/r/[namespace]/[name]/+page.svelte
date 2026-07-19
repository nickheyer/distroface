<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { rpc } from '$lib/rpc';
	import { Lister } from '$lib/list.svelte';
	import { Visibility, type Descriptor, type Repository, type Tag } from '$lib/proto/distroface/v1/types_pb';
	import { fmtBytes, fmtCount, fmtDate, fmtWhen, digestShort, visibilityLabel } from '$lib/fmt';
	import { session } from '$lib/state/session.svelte';
	import { gate } from '$lib/state/site.svelte';
	import { errata } from '$lib/state/errata.svelte';
	import Leaf from '$lib/bits/Leaf.svelte';
	import Find from '$lib/bits/Find.svelte';
	import Tally from '$lib/bits/Tally.svelte';
	import Copy from '$lib/bits/Copy.svelte';
	import Confirm from '$lib/bits/Confirm.svelte';
	import DescriptorTree from '$lib/bits/DescriptorTree.svelte';
	import WebhookDesk from '$lib/bits/WebhookDesk.svelte';

	const namespace = $derived(page.params.namespace!);
	const name = $derived(page.params.name!);

	let repo = $state<Repository | null>(null);
	let missing = $state(false);

	const tags = new Lister<Tag>((p) =>
		rpc.repository.listTags({ page: p, namespace, name }).then((r) => ({ rows: r.tags, page: r.page }))
	);

	$effect(() => {
		void namespace;
		void name;
		repo = null;
		missing = false;
		inspected = null;
		rpc.repository
			.getRepository({ namespace, name })
			.then((r) => (repo = r.repository ?? null))
			.catch(() => (missing = true));
		tags.first();
	});

	// Inspect tags
	let inspected = $state<{ tag: Tag; d: Descriptor } | null>(null);
	let inspecting = $state('');

	async function inspect(tag: Tag) {
		if (inspected?.tag.name === tag.name) {
			inspected = null;
			return;
		}
		inspecting = tag.name;
		try {
			const r = await rpc.repository.resolveTag({ namespace, name, tag: tag.name });
			if (r.descriptor) inspected = { tag, d: r.descriptor };
		} finally {
			inspecting = '';
		}
	}

	// Disposition
	let editDesc = $state('');
	let editVis = $state<Visibility>(Visibility.PRIVATE);
	let savingMeta = $state(false);

	$effect(() => {
		if (repo) {
			editDesc = repo.description;
			editVis = repo.visibility;
		}
	});

	async function saveMeta(e: Event) {
		e.preventDefault();
		savingMeta = true;
		try {
			const r = await rpc.repository.updateRepository({
				namespace,
				name,
				description: editDesc,
				visibility: editVis
			});
			repo = r.repository ?? repo;
			errata.remark('Repository saved.');
		} catch {
			// Interceptor reports
		} finally {
			savingMeta = false;
		}
	}

	async function removeRepo() {
		await rpc.repository.deleteRepository({ namespace, name });
		errata.remark(`${namespace}/${name} deleted.`);
		goto('/');
	}

	async function toggleStar() {
		if (!repo) return;
		const req = { namespace, name };
		const r = repo.isStarred
			? await rpc.repository.unstarRepository(req)
			: await rpc.repository.starRepository(req);
		repo.isStarred = !repo.isStarred;
		repo.starCount = r.starCount;
	}

	const host = $derived(gate.host());
	const pullRef = $derived(gate.imageRef(namespace, name));
	const firstTag = $derived(tags.rows[0]?.name ?? 'latest');

	function platformList(tag: Tag): string {
		return tag.platforms
			.map((p) => [p.os, p.architecture, p.variant].filter(Boolean).join('/'))
			.filter(Boolean)
			.join(', ');
	}

	// Object scoped grants and own namespace count too
	const ownNamespace = $derived(namespace === session.user?.username);
	const mayEdit = $derived(
		session.signedIn && (session.can('repositories', 'update', `${namespace}/${name}`) || ownNamespace)
	);
	const mayDelete = $derived(
		session.signedIn && (session.can('repositories', 'delete', `${namespace}/${name}`) || ownNamespace)
	);
	const config = $derived(inspected?.d.imageConfig);
</script>

{#if missing}
	<hgroup class="folio">
		<p class="kicker">Registry</p>
		<h1>Not found</h1>
		<p class="sub">
			No repository <span class="mono">{namespace}/{name}</span> exists in this registry. Back to
			the <a href="/">registry</a>.
		</p>
	</hgroup>
{:else if repo}
	<hgroup class="folio">
		<p class="kicker"><a href="/">Registry</a> / {namespace}</p>
		<h1>{repo.fullName}</h1>
		<p class="sub">
			{visibilityLabel[repo.visibility]}
			{repo.isOrgNamespace ? 'organization repository' : 'repository'}
			· {repo.tagCount} {repo.tagCount === 1 ? 'tag' : 'tags'}
			· {fmtBytes(repo.sizeBytes)}
			· {fmtCount(repo.pullCount)} pulls
			{#if session.signedIn}
				·
				<button
					class="rowact plain"
					style="font-style: normal"
					style:color={repo.isStarred ? 'var(--wax)' : undefined}
					onclick={toggleStar}>{repo.isStarred ? '★ starred' : '☆ star'} ({fmtCount(repo.starCount)})</button>
			{/if}
		</p>
		{#if repo.description}
			<p class="sub">{repo.description}</p>
		{/if}
	</hgroup>

	<Leaf no="01" title="Pull">
		<div class="cmdline">
			docker pull {host}/{pullRef}:{firstTag}
			<Copy text={`docker pull ${host}/${pullRef}:${firstTag}`} />
		</div>
	</Leaf>

	<Leaf no="02" title="Tags">
		{#snippet aside()}
			<Find lister={tags} placeholder="tag…" />
		{/snippet}

		{#if tags.loaded && tags.rows.length === 0}
			<p class="vacant">No tags. The repository may have been emptied.</p>
		{:else}
			<div class="ledger-scroll">
				<table class="ledger">
					<thead>
						<tr>
							<th>Tag</th>
							<th>Digest</th>
							<th class="num">Size</th>
							<th>Platforms</th>
							<th>Pushed</th>
							<th class="end">&nbsp;</th>
						</tr>
					</thead>
					<tbody>
						{#each tags.rows as tag (tag.name)}
							<tr>
								<td class="mono"><b>{tag.name}</b></td>
								<td class="mono" title={tag.digest}>{digestShort(tag.digest)} <Copy text={tag.digest} /></td>
								<td class="num mono">{fmtBytes(tag.sizeBytes)}</td>
								<td class="mono soft">{platformList(tag) || '—'}</td>
								<td class="mono">{fmtDate(tag.pushedAt)}</td>
								<td class="end">
									<button class="rowact plain" disabled={inspecting === tag.name} onclick={() => inspect(tag)}>
										{inspected?.tag.name === tag.name ? 'close' : inspecting === tag.name ? 'opening…' : 'open'}
									</button>
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
			<Tally lister={tags} unit="tags" />
		{/if}
	</Leaf>

	{#if inspected}
		<Leaf no="03" title="Manifest · {inspected.tag.name}">
			{#snippet aside()}
				<span class="mono faint" style="font-size: 0.72rem">{inspected!.d.mediaType}</span>
			{/snippet}

			<DescriptorTree d={inspected.d} />

			{#if config}
				<div class="panel">
					<p class="panel-title">Image configuration</p>
					<dl class="docket">
						{#if config.created}
							<dt>Created</dt>
							<dd class="mono">{fmtWhen(config.created)}</dd>
						{/if}
						{#if config.author}
							<dt>Author</dt>
							<dd>{config.author}</dd>
						{/if}
						{#if config.os || config.architecture}
							<dt>Platform</dt>
							<dd class="mono">{[config.os, config.architecture].filter(Boolean).join('/')}</dd>
						{/if}
						{#if config.entrypoint.length}
							<dt>Entrypoint</dt>
							<dd class="mono">{config.entrypoint.join(' ')}</dd>
						{/if}
						{#if config.cmd.length}
							<dt>Command</dt>
							<dd class="mono">{config.cmd.join(' ')}</dd>
						{/if}
						{#if config.workingDir}
							<dt>Workdir</dt>
							<dd class="mono">{config.workingDir}</dd>
						{/if}
						{#if config.exposedPorts.length}
							<dt>Ports</dt>
							<dd class="mono">{config.exposedPorts.join(', ')}</dd>
						{/if}
						{#if config.volumes.length}
							<dt>Volumes</dt>
							<dd class="mono">{config.volumes.join(', ')}</dd>
						{/if}
						{#if config.env.length}
							<dt>Environment</dt>
							<dd>
								<details>
									<summary class="caps faint" style="cursor: pointer">{config.env.length} variables</summary>
									<pre class="tract" style="margin-top: 0.4rem">{config.env.join('\n')}</pre>
								</details>
							</dd>
						{/if}
						{#if Object.keys(config.labels).length}
							<dt>Labels</dt>
							<dd>
								<details>
									<summary class="caps faint" style="cursor: pointer"
										>{Object.keys(config.labels).length} labels</summary>
									<pre class="tract" style="margin-top: 0.4rem">{Object.entries(config.labels)
											.map(([k, v]) => `${k}=${v}`)
											.join('\n')}</pre>
								</details>
							</dd>
						{/if}
					</dl>
				</div>

				{#if config.history.length}
					<div class="panel">
						<p class="panel-title">Build history</p>
						<div class="ledger-scroll">
							<table class="ledger">
								<thead>
									<tr>
										<th style="width: 100%">Step</th>
										<th class="num">Layer</th>
										<th>When</th>
									</tr>
								</thead>
								<tbody>
									{#each config.history as h, i (i)}
										<tr>
											<td class="mono" style="white-space: pre-wrap; overflow-wrap: anywhere"
												>{h.createdBy || h.comment || '—'}</td>
											<td class="num mono">{h.emptyLayer ? '·' : fmtBytes(h.sizeBytes)}</td>
											<td class="mono">{fmtDate(h.created)}</td>
										</tr>
									{/each}
								</tbody>
							</table>
						</div>
					</div>
				{/if}
			{/if}

			<div class="gap-top">
				<div class="cmdline">
					docker pull {host}/{pullRef}:{inspected.tag.name}
					<Copy text={`docker pull ${host}/${pullRef}:${inspected.tag.name}`} />
				</div>
			</div>
		</Leaf>
	{/if}

	{#if session.signedIn && !gate.isPortal}
		<Leaf no={inspected ? '04' : '03'} title="Webhooks">
			<WebhookDesk repoId={repo.id} />
		</Leaf>
	{/if}

	{#if mayEdit || mayDelete}
		<Leaf no={inspected ? '05' : '04'} title="Manage">
			{#if mayEdit}
				<form onsubmit={saveMeta}>
					<label class="field">
						<span>Description</span>
						<textarea rows="2" bind:value={editDesc}></textarea>
					</label>
					<label class="field">
						<span>Visibility</span>
						<select bind:value={editVis} style="max-width: 12rem">
							<option value={Visibility.PRIVATE}>private</option>
							<option value={Visibility.PUBLIC}>public</option>
						</select>
						<span class="hint">Public repositories may be pulled without credentials.</span>
					</label>
					<button class="act" type="submit" disabled={savingMeta}>Save</button>
				</form>
			{/if}
			{#if mayDelete}
				<hr />
				<p class="note">
					Deleting a repository removes its tags and manifests from the registry. Blobs are
					reclaimed by the next garbage collection.
				</p>
				<div class="gap-top">
					<Confirm label="delete repository" onconfirm={removeRepo} />
				</div>
			{/if}
		</Leaf>
	{/if}
{:else}
	<p class="working" style="margin-top: 4rem">loading</p>
{/if}

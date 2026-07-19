<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { rpc } from '$lib/rpc';
	import { Lister } from '$lib/list.svelte';
	import { MatchKind } from '$lib/proto/distroface/v1/pagination_pb';
	import type { Artifact, ArtifactRepository } from '$lib/proto/distroface/v1/types_pb';
	import type { ArtifactVersionGroup } from '$lib/proto/distroface/v1/artifact_pb';
	import { fmtBytes, fmtCount, fmtWhen } from '$lib/fmt';
	import { session } from '$lib/state/session.svelte';
	import { errata } from '$lib/state/errata.svelte';
	import { downloadArtifact, uploadChunks } from '$lib/files';
	import Leaf from '$lib/bits/Leaf.svelte';
	import Find from '$lib/bits/Find.svelte';
	import Tally from '$lib/bits/Tally.svelte';
	import Confirm from '$lib/bits/Confirm.svelte';

	const namespace = $derived(page.params.namespace!);
	const repoName = $derived(page.params.repo!);

	let repo = $state<ArtifactRepository | null>(null);
	let missing = $state(false);
	let versionFilter = $state('');

	const versions = new Lister<ArtifactVersionGroup>((p) =>
		rpc.artifact
			.listArtifactVersions({ page: p, repoName, namespace })
			.then((r) => ({ rows: r.versions, page: r.page })),
		{ pageSize: 20 }
	);

	const files = new Lister<Artifact>((p) =>
		rpc.artifact
			.searchArtifacts({ page: p, repoName, namespace })
			.then((r) => ({ rows: r.artifacts, page: r.page }))
	);

	$effect(() => {
		void namespace;
		void repoName;
		missing = false;
		repo = null;
		versionFilter = '';
		rpc.artifact
			.getArtifactRepository({ name: repoName, namespace })
			.then((r) => (repo = r.repository ?? null))
			.catch(() => (missing = true));
		versions.first();
	});

	$effect(() => {
		files.filters = versionFilter
			? [{ field: 'version', match: MatchKind.EQUALS, value: versionFilter }]
			: [];
		files.first();
	});

	function groupSize(g: ArtifactVersionGroup): bigint {
		return g.artifacts.reduce((acc, a) => acc + a.size, 0n);
	}

	// ── File desk ───────────────────────────────────────────────────
	let openFile = $state<Artifact | null>(null);
	let propsText = $state('');
	let metaText = $state('');
	let moveName = $state('');
	let movePath = $state('');
	let moveVersion = $state('');
	let deskBusy = $state(false);

	function openDesk(a: Artifact) {
		if (openFile?.id === a.id) {
			openFile = null;
			return;
		}
		openFile = a;
		propsText = Object.entries(a.properties)
			.map(([k, v]) => `${k}=${v}`)
			.join('\n');
		metaText = a.metadata;
		moveName = a.name;
		movePath = a.path;
		moveVersion = a.version;
	}

	function parseProps(text: string): Record<string, string> {
		const out: Record<string, string> = {};
		for (const line of text.split('\n')) {
			const t = line.trim();
			if (!t) continue;
			const i = t.indexOf('=');
			if (i < 1) continue;
			out[t.slice(0, i).trim()] = t.slice(i + 1).trim();
		}
		return out;
	}

	async function saveDesk(e: Event) {
		e.preventDefault();
		if (!openFile) return;
		if (metaText.trim()) {
			try {
				JSON.parse(metaText);
			} catch {
				errata.report('Metadata must be a valid JSON object.');
				return;
			}
		}
		deskBusy = true;
		try {
			await rpc.artifact.setArtifactProperties({
				repoName,
				id: openFile.id,
				properties: parseProps(propsText),
				namespace
			});
			const r = await rpc.artifact.updateArtifact({
				repoName,
				id: openFile.id,
				name: moveName !== openFile.name ? moveName : undefined,
				path: movePath !== openFile.path ? movePath : undefined,
				version: moveVersion !== openFile.version ? moveVersion : undefined,
				metadata: metaText !== openFile.metadata ? metaText : undefined,
				namespace
			});
			errata.remark('Artifact saved.');
			openFile = r.artifact ?? null;
			await Promise.all([files.fetch(), versions.fetch()]);
		} catch {
			// Interceptor reports
		} finally {
			deskBusy = false;
		}
	}

	async function removeFile(a: Artifact) {
		await rpc.artifact.deleteArtifact({ repoName, id: a.id, version: '', path: '', namespace });
		if (openFile?.id === a.id) openFile = null;
		errata.remark(`${a.path} deleted.`);
		await Promise.all([files.fetch(), versions.fetch()]);
	}

	async function fetchFile(a: Artifact) {
		try {
			await downloadArtifact(namespace, repoName, a.version, a.path, a.name);
		} catch (err) {
			errata.report(err instanceof Error ? err.message : 'Download failed.');
		}
	}

	// ── Upload ──────────────────────────────────────────────────────
	let upFile = $state<File | null>(null);
	let upVersion = $state('');
	let upPath = $state('');
	let upProps = $state('');
	let upBusy = $state(false);
	let upProgress = $state('');

	function pickFile(e: Event) {
		const input = e.currentTarget as HTMLInputElement;
		upFile = input.files?.[0] ?? null;
		if (upFile && !upPath) upPath = upFile.name;
	}

	async function upload(e: Event) {
		e.preventDefault();
		if (!upFile || !upVersion.trim()) return;
		upBusy = true;
		upProgress = 'starting';
		try {
			const init = await rpc.artifact.initiateArtifactUpload({ repoName, namespace });
			await uploadChunks(init.uploadUrl, upFile, (sent) => {
				upProgress = `${Math.min(100, Math.round((sent / Math.max(upFile!.size, 1)) * 100))}%`;
			});
			upProgress = 'finalizing';
			await rpc.artifact.completeArtifactUpload({
				repoName,
				uploadId: init.uploadId,
				version: upVersion.trim(),
				path: upPath.trim() || upFile.name,
				properties: parseProps(upProps),
				metadata: '',
				namespace
			});
			errata.remark(`${upPath.trim() || upFile.name} uploaded at ${upVersion.trim()}.`);
			upFile = null;
			upVersion = '';
			upPath = '';
			upProps = '';
			await Promise.all([files.first(), versions.first()]);
		} catch (err) {
			errata.report(err instanceof Error ? err.message : 'Upload failed.');
		} finally {
			upBusy = false;
			upProgress = '';
		}
	}

	// ── Disposition ─────────────────────────────────────────────────
	let editDesc = $state('');
	let editPrivate = $state(true);
	let metaBusy = $state(false);

	$effect(() => {
		if (repo) {
			editDesc = repo.description;
			editPrivate = repo.isPrivate;
		}
	});

	async function saveRepo(e: Event) {
		e.preventDefault();
		metaBusy = true;
		try {
			const r = await rpc.artifact.updateArtifactRepository({
				name: repoName,
				namespace,
				description: editDesc,
				isPrivate: editPrivate
			});
			repo = r.repository ?? repo;
			errata.remark('Repository saved.');
		} catch {
			// Interceptor reports
		} finally {
			metaBusy = false;
		}
	}

	async function removeRepo() {
		await rpc.artifact.deleteArtifactRepository({ name: repoName, namespace });
		errata.remark(`${namespace}/${repoName} deleted.`);
		goto('/artifacts');
	}
</script>

{#if missing}
	<hgroup class="folio">
		<p class="kicker">Artifacts</p>
		<h1>Not found</h1>
		<p class="sub">
			No artifact repository <span class="mono">{namespace}/{repoName}</span> exists here. Back to
			<a href="/artifacts">artifacts</a>.
		</p>
	</hgroup>
{:else if repo}
	<hgroup class="folio">
		<p class="kicker"><a href="/artifacts">Artifacts</a> / {namespace}</p>
		<h1>{repo.fullName}</h1>
		<p class="sub">
			{repo.isPrivate ? 'private' : 'public'}
			· {fmtCount(repo.artifactCount)} artifacts · {fmtBytes(repo.totalSize)} held
		</p>
		{#if repo.description}
			<p class="sub">{repo.description}</p>
		{/if}
	</hgroup>

	<Leaf no="01" title="Versions">
		{#if versions.loaded && versions.rows.length === 0}
			<p class="vacant">Nothing has been uploaded yet.</p>
		{:else}
			<div class="ledger-scroll">
				<table class="ledger">
					<thead>
						<tr>
							<th>Version</th>
							<th class="num">Files</th>
							<th class="num">Size</th>
						</tr>
					</thead>
					<tbody>
						{#each versions.rows as g (g.version)}
							<tr>
								<td class="mono">
									<button
										class="rowact"
										style:color={versionFilter === g.version ? 'var(--wax)' : undefined}
										onclick={() => (versionFilter = versionFilter === g.version ? '' : g.version)}
										><b>{g.version}</b></button>
								</td>
								<td class="num mono">{g.artifacts.length}</td>
								<td class="num mono">{fmtBytes(groupSize(g))}</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
			<Tally lister={versions} unit="versions" />
		{/if}
	</Leaf>

	<Leaf no="02" title={versionFilter ? `Files · ${versionFilter}` : 'Files'}>
		{#snippet aside()}
			{#if versionFilter}
				<button class="rowact plain" onclick={() => (versionFilter = '')}>× all versions</button>
			{/if}
			<Find lister={files} placeholder="path…" />
		{/snippet}

		{#if files.loaded && files.rows.length === 0}
			<p class="vacant">No files{versionFilter ? ` at ${versionFilter}` : ''}.</p>
		{:else}
			<div class="ledger-scroll">
				<table class="ledger">
					<thead>
						<tr>
							<th>Path</th>
							<th>Version</th>
							<th class="num">Size</th>
							<th>Type</th>
							<th>Uploaded</th>
							<th class="end">&nbsp;</th>
						</tr>
					</thead>
					<tbody>
						{#each files.rows as a (a.id)}
							<tr>
								<td class="mono" style="overflow-wrap: anywhere">{a.path}</td>
								<td class="mono">{a.version}</td>
								<td class="num mono">{fmtBytes(a.size)}</td>
								<td class="mono soft">{a.mimeType || '—'}</td>
								<td class="mono">{fmtWhen(a.createdAt)}</td>
								<td class="end">
									<button class="rowact plain" onclick={() => fetchFile(a)}>download</button>
									{#if session.signedIn}
										<button class="rowact plain" onclick={() => openDesk(a)}>
											{openFile?.id === a.id ? 'close' : 'edit'}
										</button>
										<Confirm label="delete" onconfirm={() => removeFile(a)} />
									{/if}
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
			<Tally lister={files} unit="files" />
		{/if}

		{#if openFile}
			<form class="panel" onsubmit={saveDesk}>
				<p class="panel-title">Edit · {openFile.path} @ {openFile.version}</p>
				<div class="row">
					<label class="field" style="flex: 1; min-width: 12rem">
						<span>Name</span>
						<input type="text" bind:value={moveName} />
					</label>
					<label class="field" style="flex: 2; min-width: 16rem">
						<span>Path</span>
						<input type="text" bind:value={movePath} />
					</label>
					<label class="field" style="flex: 1; min-width: 8rem">
						<span>Version</span>
						<input type="text" bind:value={moveVersion} />
					</label>
				</div>
				<label class="field">
					<span>Properties</span>
					<textarea rows="4" bind:value={propsText} placeholder="key=value, one per line"></textarea>
				</label>
				<label class="field">
					<span>Metadata</span>
					<textarea rows="4" bind:value={metaText} placeholder={'{ "any": "json" }'}></textarea>
				</label>
				<dl class="docket" style="max-width: 34rem">
					<dt>Digest</dt>
					<dd class="mono" style="overflow-wrap: anywhere">{openFile.digest}</dd>
				</dl>
				<div class="row gap-top">
					<button class="act wax" type="submit" disabled={deskBusy}>Save</button>
					<button class="rowact plain" type="button" onclick={() => (openFile = null)}>close</button>
				</div>
			</form>
		{/if}
	</Leaf>

	{#if session.signedIn}
		<Leaf no="03" title="Upload">
			<form onsubmit={upload}>
				<label class="field">
					<span>File</span>
					<input type="file" onchange={pickFile} />
				</label>
				<div class="row">
					<label class="field" style="flex: 1; min-width: 9rem">
						<span>Version</span>
						<input type="text" bind:value={upVersion} placeholder="1.0.0" required />
					</label>
					<label class="field" style="flex: 2; min-width: 14rem">
						<span>Path</span>
						<input type="text" bind:value={upPath} placeholder="defaults to the file name" />
					</label>
				</div>
				<label class="field">
					<span>Properties</span>
					<textarea rows="2" bind:value={upProps} placeholder="key=value, one per line"></textarea>
				</label>
				<div class="row">
					<button class="act wax" type="submit" disabled={upBusy || !upFile || !upVersion.trim()}>
						Upload artifact
					</button>
					{#if upProgress}
						<span class="working">{upProgress}</span>
					{/if}
				</div>
			</form>
		</Leaf>

		<Leaf no="04" title="Manage">
			<form onsubmit={saveRepo}>
				<label class="field">
					<span>Description</span>
					<textarea rows="2" bind:value={editDesc}></textarea>
				</label>
				<label class="tick">
					<input type="checkbox" bind:checked={editPrivate} />
					Private
				</label>
				<div class="gap-top">
					<button class="act" type="submit" disabled={metaBusy}>Save</button>
				</div>
			</form>
			<hr />
			<p class="note">
				Deleting the repository removes every artifact and any blobs nothing else references.
			</p>
			<div class="gap-top">
				<Confirm label="delete repository" onconfirm={removeRepo} />
			</div>
		</Leaf>
	{/if}
{:else}
	<p class="working" style="margin-top: 4rem">loading</p>
{/if}

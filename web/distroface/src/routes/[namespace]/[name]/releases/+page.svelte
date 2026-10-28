<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { Plus, Search, Tag } from '$lib/icons';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import ReleaseList from '$lib/components/git/release-list.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { Pager } from '$lib/pager.svelte';
	import { GitUnit } from '$lib/proto/distroface/v1/git_pb';
	import type { Release } from '$lib/proto/distroface/v1/release_pb';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);
	const canWrite = $derived(gitRepoStore.canWrite(GitUnit.RELEASES) && !repo?.isArchived);

	let releases = $state<Release[]>([]);
	let latestTag = $state('');
	let loading = $state(true);
	let loadedFor = $state('');
	let query = $state('');
	const pager = new Pager(10);
	let debounce: ReturnType<typeof setTimeout> | undefined;

	$effect(() => {
		void gitRepoStore.load(namespace, name);
	});

	$effect(() => {
		if (repo && repo.fullName !== loadedFor) {
			loadedFor = repo.fullName;
			pager.reset();
			void load();
			void loadLatest();
		}
	});

	async function load() {
		loading = true;
		try {
			const resp = await rpcClient.release.listReleases(
				{
					namespace,
					name,
					includeDrafts: canWrite,
					includePrereleases: true,
					page: pager.request({ text: query.trim(), filters: [] })
				},
				silentCallOptions
			);
			releases = resp.releases;
			pager.apply(resp.page);
		} catch {
			releases = [];
			pager.apply();
		} finally {
			loading = false;
		}
	}

	async function loadLatest() {
		try {
			const resp = await rpcClient.release.getLatestRelease({ namespace, name }, silentCallOptions);
			latestTag = resp.release?.tag ?? '';
		} catch {
			latestTag = '';
		}
	}

	function onQuery() {
		clearTimeout(debounce);
		debounce = setTimeout(() => {
			pager.reset();
			void load();
		}, 250);
	}
</script>

<svelte:head>
	<title>Releases - {namespace}/{name}</title>
</svelte:head>

<div class="space-y-4">
	<div class="flex items-center gap-2 flex-wrap">
		<div class="relative w-full sm:w-64">
			<Search class="h-3.5 w-3.5 text-muted-foreground absolute left-2.5 top-1/2 -translate-y-1/2" />
			<Input bind:value={query} oninput={onQuery} placeholder="Search releases" class="h-8 pl-8 text-[13px]" />
		</div>
		<span class="text-[12.5px] text-muted-foreground tabular-nums">
			{pager.totalCount || releases.length} release{(pager.totalCount || releases.length) !== 1 ? 's' : ''}
		</span>
		<div class="ml-auto flex items-center gap-2">
			<Button variant="outline" size="sm" class="h-8" href={resolve('/[namespace]/[name]/tags', { namespace, name })}>
				<Tag class="h-3.5 w-3.5 mr-1.5" />Tags
			</Button>
			<PermissionGate allowed={canWrite}>
				<Button size="sm" class="h-8" href={resolve('/[namespace]/[name]/releases/new', { namespace, name })}>
					<Plus class="h-3.5 w-3.5 mr-1.5" />New Release
				</Button>
			</PermissionGate>
		</div>
	</div>

	<ReleaseList {releases} {namespace} {name} {latestTag} {loading} emptyMessage={query ? 'No releases match' : 'No releases yet'} />

	<DataPagination {pager} onChange={load} pageSizeOptions={[10, 20, 50]} />
</div>

<script lang="ts">
	import CommitList from '$lib/components/git/commit-list.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import { Pager } from '$lib/pager.svelte';
	import type { GitCommit } from '$lib/proto/distroface/v1/git_pb';
	import type { PageInfo } from '$lib/proto/distroface/v1/pagination_pb';
	import { errText } from '$lib/act.svelte';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';

	let {
		namespace,
		name,
		resetKey,
		fetch,
		pageSize = 30,
		emptyMessage = 'No commits'
	}: {
		namespace: string;
		name: string;
		resetKey: string;
		fetch: (
			page: ReturnType<Pager['request']>
		) => Promise<{ commits: GitCommit[]; page?: PageInfo }>;
		pageSize?: number;
		emptyMessage?: string;
	} = $props();

	// svelte-ignore state_referenced_locally
	const pager = new Pager(pageSize);
	let commits = $state<GitCommit[]>([]);
	let loading = $state(true);
	let error = $state('');
	let seq = 0;

	async function load() {
		const mine = ++seq;
		loading = true;
		error = '';
		try {
			const resp = await fetch(pager.request());
			if (mine !== seq) return;
			commits = resp.commits;
			pager.apply(resp.page);
		} catch (err) {
			if (mine !== seq) return;
			commits = [];
			error = errText(err);
			pager.apply(undefined);
		} finally {
			if (mine === seq) loading = false;
		}
	}

	$effect(() => {
		void resetKey;
		pager.reset();
		load();
	});
</script>

<div class="space-y-4">
	{#if error}
		<Alert variant="destructive">
			<AlertDescription>{error}</AlertDescription>
		</Alert>
	{:else}
		<CommitList {commits} {namespace} {name} {loading} {emptyMessage} />
	{/if}
	<DataPagination {pager} onChange={load} pageSizeOptions={[30, 50, 100]} />
</div>

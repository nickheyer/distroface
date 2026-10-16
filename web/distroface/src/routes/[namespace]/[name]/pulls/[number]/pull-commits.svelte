<script lang="ts">
	import PagedCommitList from '$lib/components/git/paged-commit-list.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';

	let {
		namespace,
		name,
		number,
		headSha
	}: {
		namespace: string;
		name: string;
		number: bigint;
		headSha: string;
	} = $props();
</script>

<PagedCommitList
	{namespace}
	{name}
	resetKey="{number}:{headSha}"
	fetch={(page) => rpcClient.pull.listPullRequestCommits({ namespace, name, number, page }, silentCallOptions)}
	emptyMessage="No commits in this pull request"
/>

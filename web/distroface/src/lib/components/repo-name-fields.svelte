<script lang="ts">
	import { untrack } from 'svelte';
	import { Code, ConnectError } from '@connectrpc/connect';
	import { Input } from '$lib/components/ui/input';
	import AsyncSelect from '$lib/components/async-select.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import { rpcClient, backgroundCallOptions } from '$lib/api/rpc-client';
	import { repoNameError, repoNameHelp, type RepoFamily } from '$lib/repo-names';

	let {
		family,
		namespace = $bindable(''),
		name = $bindable(''),
		valid = $bindable(false),
		ownNamespace,
		defaultName = '',
		placeholder,
		idPrefix
	}: {
		family: RepoFamily;
		namespace: string;
		name: string;
		valid: boolean;
		ownNamespace: string;
		defaultName?: string;
		placeholder: string;
		idPrefix: string;
	} = $props();

	// A typed name sticks, an emptied one picks up the next default
	let edited = $state(false);
	$effect(() => {
		const next = defaultName;
		if (!untrack(() => edited)) name = next;
	});

	const owner = $derived(namespace || ownNamespace);
	const ruleError = $derived(name ? repoNameError(family, name) : '');
	let taken = $state(false);
	let checkError = $state('');
	let probe = 0;

	async function exists(ns: string, n: string): Promise<boolean> {
		const req = { namespace: ns, name: n };
		try {
			if (family === 'artifact')
				await rpcClient.artifact.getArtifactRepository(req, backgroundCallOptions);
			else if (family === 'image')
				await rpcClient.repository.getRepository(req, backgroundCallOptions);
			else await rpcClient.git.getGitRepository(req, backgroundCallOptions);
			return true;
		} catch (err) {
			if (err instanceof ConnectError && err.code === Code.NotFound) return false;
			throw err;
		}
	}

	// Debounced lookup under the chosen owner, stale answers are dropped
	$effect(() => {
		const ns = owner;
		const n = name.trim();
		const seq = ++probe;
		taken = false;
		checkError = '';
		if (!ns || !n || ruleError) return;
		const timer = setTimeout(async () => {
			try {
				const found = await exists(ns, n);
				if (seq === probe) taken = found;
			} catch (err) {
				if (seq === probe)
					checkError = `Could not check availability, ${err instanceof ConnectError ? err.rawMessage || err.message : String(err)}`;
			}
		}, 300);
		return () => clearTimeout(timer);
	});

	$effect(() => {
		valid = !!name.trim() && !ruleError && !taken;
	});

	const error = $derived(ruleError || (taken ? `Already exists under ${owner}` : checkError));
	const help = $derived(
		!edited && !!name && name === defaultName
			? 'Matches the source name until edited'
			: repoNameHelp(family)
	);
</script>

<div class="grid items-start gap-3 sm:grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)]">
	<FormField label="Owner" id="{idPrefix}-namespace">
		<AsyncSelect
			bind:selected={namespace}
			placeholder={ownNamespace}
			searchPlaceholder="Search organizations"
			fetchPage={async (query, pageToken) => {
				const resp = await rpcClient.organization.listOrganizations({
					page: { query: { text: query, filters: [] }, pageToken, pageSize: 20 }
				});
				const items = resp.organizations.map((o) => ({
					value: o.name,
					label: o.displayName || o.name
				}));
				if (
					!pageToken &&
					ownNamespace &&
					ownNamespace.toLowerCase().includes(query.toLowerCase())
				) {
					items.unshift({ value: ownNamespace, label: `${ownNamespace} (you)` });
				}
				return { items, nextPageToken: resp.page?.nextPageToken ?? '' };
			}}
		/>
	</FormField>
	<span class="hidden pt-7 text-xl text-muted-foreground/50 sm:block" aria-hidden="true">/</span>
	<FormField label="Repository name" id="{idPrefix}-name" required {help} {error}>
		<Input
			id="{idPrefix}-name"
			bind:value={name}
			oninput={(e) => (edited = e.currentTarget.value !== '')}
			{placeholder}
			class="font-mono"
			aria-invalid={!!ruleError || taken}
			autocomplete="off"
			spellcheck="false"
		/>
	</FormField>
</div>

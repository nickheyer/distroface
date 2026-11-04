<script lang="ts">
	import { BookOpen, Loader } from '$lib/icons';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import FormCard from '$lib/components/form-card.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import MarkdownEditor from './markdown-editor.svelte';
	import { authStore } from '$lib/stores/auth.svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import type { WikiPage } from '$lib/proto/distroface/v1/wiki_pb';
	import { toast } from 'svelte-sonner';

	let {
		namespace,
		name,
		page,
		initialTitle = '',
		onSaved,
		onCancel
	}: {
		namespace: string;
		name: string;
		page?: WikiPage;
		initialTitle?: string;
		onSaved: (page: WikiPage) => void;
		onCancel: () => void;
	} = $props();

	// Parent remounts with key when the page changes
	// svelte-ignore state_referenced_locally
	const seed = { page, initialTitle };

	let title = $state(seed.page?.title ?? seed.initialTitle);
	let content = $state(seed.page?.content ?? '');
	let message = $state('');
	let saving = $state(false);

	const editing = $derived(!!page);
	const base = $derived(`/${namespace}/${name}/wiki`);
	const rawBase = $derived(`/${namespace}/${name}.wiki/-/raw/HEAD/`);
	const user = $derived(authStore.user);
	const titleValid = $derived(title.trim() !== '' && !/[/\\]/.test(title));
	const titleError = $derived(
		title.trim() !== '' && !titleValid ? 'Titles cannot contain slashes' : ''
	);
	const titleHelp = $derived(
		editing && titleValid && title.trim() !== page?.title ? 'Saving renames the page' : undefined
	);
	const dirty = $derived(
		editing
			? content !== page?.content || title.trim() !== page?.title
			: content.trim() !== '' || title.trim() !== ''
	);
	const placeholder = $derived(
		editing ? `Update ${page?.title ?? 'page'}` : `Create ${title.trim() || 'page'}`
	);
	const ready = $derived(titleValid && dirty && !saving);
	const author = $derived(
		user ? `Committed to the wiki as ${user.displayName || user.username}` : undefined
	);

	async function save() {
		if (!ready) return;
		saving = true;
		try {
			const msg = message.trim() || placeholder;
			if (page) {
				const resp = await rpcClient.wiki.updateWikiPage({
					namespace,
					name,
					title: page.title,
					newTitle: title.trim() !== page.title ? title.trim() : undefined,
					content,
					message: msg,
					expectedSha: page.lastCommit?.sha ?? ''
				});
				toast.success('Page saved');
				if (resp.page) onSaved(resp.page);
			} else {
				const resp = await rpcClient.wiki.createWikiPage({
					namespace,
					name,
					title: title.trim(),
					content,
					message: msg
				});
				toast.success('Page created');
				if (resp.page) onSaved(resp.page);
			}
		} catch {
			// Error interceptor already toasted
		} finally {
			saving = false;
		}
	}
</script>

<FormCard title={editing ? 'Page' : 'New page'} description={author} icon={BookOpen}>
	<div class="space-y-4">
		<FormField label="Title" id="wiki-title" required error={titleError} help={titleHelp}>
			<Input
				id="wiki-title"
				bind:value={title}
				placeholder="Page title"
				maxlength={200}
				aria-invalid={!!titleError}
			/>
		</FormField>

		<FormField
			label="Content"
			id="wiki-content"
			help="Markdown with [[Page]] links, #12 issue links, and @mentions. The help button lists the syntax."
		>
			<MarkdownEditor
				bind:value={content}
				id="wiki-content"
				{namespace}
				{name}
				wikiBase="{base}/"
				{rawBase}
				placeholder="Write the page"
			/>
		</FormField>

		<FormField
			label="Commit message"
			id="wiki-message"
			help="Each save is a commit in the wiki repository"
		>
			<Input id="wiki-message" bind:value={message} {placeholder} maxlength={200} />
		</FormField>
	</div>
	{#snippet footer()}
		<Button variant="outline" onclick={onCancel} disabled={saving}>Cancel</Button>
		<Button onclick={save} disabled={!ready}>
			{#if saving}
				<Loader class="animate-spin" />
			{/if}
			{saving ? 'Saving' : editing ? 'Save page' : 'Create page'}
		</Button>
	{/snippet}
</FormCard>

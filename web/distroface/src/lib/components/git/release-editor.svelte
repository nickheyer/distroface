<script lang="ts">
	import { Tag, Sparkles, Eye, Pencil, Loader2, Rocket, Trash2, Download, Package, Container } from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Switch } from '$lib/components/ui/switch';
	import FormCard from '$lib/components/form-card.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import RefPicker from './ref-picker.svelte';
	import Markdown from './markdown.svelte';
	import AssetUploader from './asset-uploader.svelte';
	import ImagePicker from './image-picker.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import type { Release, ReleaseAsset, ReleaseImage } from '$lib/proto/distroface/v1/release_pb';
	import { formatBytes } from '$lib/utils';
	import { toast } from 'svelte-sonner';

	let {
		namespace,
		name,
		defaultBranch = '',
		release,
		initialTag = '',
		onCreated,
		onSaved,
		onCancel
	}: {
		namespace: string;
		name: string;
		defaultBranch?: string;
		release?: Release;
		initialTag?: string;
		onCreated?: (release: Release) => void;
		onSaved?: (release: Release, published: boolean) => void;
		onCancel?: () => void;
	} = $props();

	// Parent remounts with key when the release changes
	// svelte-ignore state_referenced_locally
	const seed = { release, initialTag, defaultBranch };

	let current = $state<Release | undefined>(seed.release);
	let tag = $state(seed.release?.tag ?? seed.initialTag);
	let target = $state(seed.defaultBranch);
	let title = $state(seed.release?.name ?? '');
	let body = $state(seed.release?.body ?? '');
	let draft = $state(seed.release?.draft ?? true);
	let prerelease = $state(seed.release?.prerelease ?? false);
	let assets = $state<ReleaseAsset[]>(seed.release?.assets ?? []);
	let images = $state<ReleaseImage[]>(seed.release?.images ?? []);
	let mode = $state<'write' | 'preview'>('write');
	let saving = $state(false);
	let publishing = $state(false);
	let generating = $state(false);
	let tagExists = $state<boolean | null>(null);
	let detaching = $state('');
	let debounce: ReturnType<typeof setTimeout> | undefined;
	let seq = 0;

	const editing = $derived(!!current);
	const tagValid = $derived(/^[^\s~^:?*[\\]+$/.test(tag.trim()));
	const tagError = $derived(tag.trim() !== '' && !tagValid ? 'Tag names cannot contain spaces or git control characters' : '');
	const ready = $derived(tagValid && !saving && !publishing);
	const canPublish = $derived(!current || current.draft);
	const tagHelp = $derived(
		editing
			? undefined
			: tagExists === true
				? 'Existing tag, the release points at its commit'
				: tagExists === false
					? 'New tag, created at the target below on save'
					: 'Pick an existing tag or type a new one'
	);
	const assetsHelp = $derived(`Files stored in the ${namespace}/${name} artifact package at version ${tag.trim() || '...'}`);
	const imagesHelp = $derived(`Linked images tagged ${tag.trim() || '...'} appear automatically, pin others here`);

	$effect(() => {
		if (!target && defaultBranch) target = defaultBranch;
	});

	$effect(() => {
		if (initialTag && !editing) void checkTag();
	});

	// Existing tags skip the target picker
	async function checkTag() {
		const t = tag.trim();
		if (!t || editing) {
			tagExists = null;
			return;
		}
		const mine = ++seq;
		try {
			const resp = await rpcClient.gitRefs.listGitTags(
				{ namespace, name, page: { pageSize: 10, pageToken: '', query: { text: t, filters: [] }, orderBy: '' } },
				silentCallOptions
			);
			if (mine === seq) tagExists = resp.tags.some((x) => x.name === t);
		} catch {
			if (mine === seq) tagExists = null;
		}
	}

	function onTagInput() {
		clearTimeout(debounce);
		debounce = setTimeout(checkTag, 250);
	}

	function pickTag(ref: string) {
		tag = ref;
		tagExists = true;
	}

	async function generate() {
		if (!tag.trim()) {
			toast.error('Choose a tag first');
			return;
		}
		generating = true;
		try {
			const resp = await rpcClient.release.generateReleaseNotes({
				namespace,
				name,
				tag: tag.trim(),
				target: tagExists ? '' : target.trim(),
				previousTag: ''
			});
			if (resp.title && !title.trim()) title = resp.title;
			body = body.trim() ? `${body.trimEnd()}\n\n${resp.body}` : resp.body;
			mode = 'write';
		} catch {
			// Error interceptor already toasted
		} finally {
			generating = false;
		}
	}

	function adopt(rel: Release) {
		current = rel;
		tag = rel.tag;
		title = rel.name;
		body = rel.body;
		draft = rel.draft;
		prerelease = rel.prerelease;
		assets = rel.assets;
		images = rel.images;
	}

	async function persist(asDraft: boolean): Promise<Release | undefined> {
		if (current) {
			const resp = await rpcClient.release.updateRelease({
				namespace,
				name,
				tag: current.tag,
				title,
				body,
				draft: asDraft,
				prerelease
			});
			return resp.release;
		}
		const resp = await rpcClient.release.createRelease({
			namespace,
			name,
			tag: tag.trim(),
			target: tagExists ? '' : target.trim(),
			title,
			body,
			draft: asDraft,
			prerelease
		});
		return resp.release;
	}

	async function save() {
		if (!ready) return;
		saving = true;
		try {
			const wasNew = !current;
			const rel = await persist(draft);
			if (!rel) return;
			adopt(rel);
			toast.success(draft ? 'Draft saved' : 'Release saved');
			if (wasNew) onCreated?.(rel);
			else onSaved?.(rel, false);
		} catch {
			// Error interceptor already toasted
		} finally {
			saving = false;
		}
	}

	function saveDraft() {
		draft = true;
		void save();
	}

	async function publish() {
		if (!ready) return;
		publishing = true;
		try {
			let rel = await persist(true);
			if (!rel) return;
			const resp = await rpcClient.release.publishRelease({ namespace, name, tag: rel.tag });
			rel = resp.release ?? rel;
			adopt(rel);
			toast.success('Release published');
			onSaved?.(rel, true);
		} catch {
			// Error interceptor already toasted
		} finally {
			publishing = false;
		}
	}

	async function detachAsset(a: ReleaseAsset) {
		if (!current) return;
		detaching = a.artifactId;
		try {
			await rpcClient.release.detachReleaseAsset({ namespace, name, tag: current.tag, artifactId: a.artifactId });
			assets = assets.filter((x) => x.artifactId !== a.artifactId);
			toast.success('Asset removed');
		} catch {
			// Error interceptor already toasted
		} finally {
			detaching = '';
		}
	}

	function sameImage(a: ReleaseImage, b: ReleaseImage): boolean {
		return a.name === b.name && a.tag === b.tag;
	}
</script>

{#snippet saveFirst(what: string)}
	<div class="rounded-xl border border-dashed border-border/60 px-5 py-6 text-center">
		<p class="text-[13px] text-muted-foreground">Save a draft to {what}.</p>
		<Button variant="outline" size="sm" class="h-8 mt-3" onclick={saveDraft} disabled={!ready}>
			{saving ? 'Saving...' : 'Save Draft'}
		</Button>
	</div>
{/snippet}

<div class="space-y-4">
	<FormCard title="Release" description="Tag, title, notes, and visibility" icon={Tag}>
		<div class="space-y-3">
			<FormField label="Tag" id="release-tag" required help={tagHelp} error={tagError}>
				{#if editing}
					<div class="flex items-center gap-2 h-9">
						<Tag class="h-3.5 w-3.5 text-muted-foreground" />
						<code class="font-mono text-sm">{tag}</code>
					</div>
				{:else}
					<div class="flex items-center gap-2 flex-wrap">
						<Input id="release-tag" bind:value={tag} oninput={onTagInput} placeholder="v1.2.0" class="font-mono text-sm w-full sm:w-56" aria-invalid={!!tagError} />
						<RefPicker {namespace} {name} current={tag} kinds={['tag']} label="Existing tags" onSelect={pickTag} />
					</div>
				{/if}
			</FormField>

			{#if !editing && tagExists !== true}
				<FormField label="Target" id="release-target" help="Branch or tag the new tag is created from">
					<RefPicker {namespace} {name} current={target} {defaultBranch} onSelect={(ref) => (target = ref)} />
				</FormField>
			{/if}

			<FormField label="Title" id="release-title">
				<Input id="release-title" bind:value={title} placeholder={tag.trim() || 'Release title'} />
			</FormField>

			<FormField label="Notes" id="release-body" bordered={false}>
				<div class="space-y-2">
					<div class="flex items-center gap-1 border-b border-border/50 flex-wrap">
						<button
							type="button"
							class="px-3 py-1.5 text-[13px] font-medium border-b-2 -mb-px inline-flex items-center gap-1.5 transition-colors {mode === 'write' ? 'border-primary text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'}"
							onclick={() => (mode = 'write')}
						>
							<Pencil class="h-3.5 w-3.5" />Write
						</button>
						<button
							type="button"
							class="px-3 py-1.5 text-[13px] font-medium border-b-2 -mb-px inline-flex items-center gap-1.5 transition-colors {mode === 'preview' ? 'border-primary text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'}"
							onclick={() => (mode = 'preview')}
						>
							<Eye class="h-3.5 w-3.5" />Preview
						</button>
						<Button variant="outline" size="sm" class="h-7 ml-auto mb-1 text-[12px]" onclick={generate} disabled={generating || !tag.trim()}>
							{#if generating}
								<Loader2 class="h-3.5 w-3.5 mr-1.5 animate-spin" />
							{:else}
								<Sparkles class="h-3.5 w-3.5 mr-1.5" />
							{/if}
							Generate notes
						</Button>
					</div>
					{#if mode === 'write'}
						<Textarea id="release-body" bind:value={body} rows={14} placeholder="Describe this release in markdown" class="font-mono text-[13px]" />
					{:else}
						<div class="rounded-xl border border-border/60 px-5 py-4 min-h-[16rem] min-w-0 overflow-x-auto">
							{#if body.trim()}
								<Markdown source={body} />
							{:else}
								<p class="text-[13px] text-muted-foreground">Nothing to preview yet.</p>
							{/if}
						</div>
					{/if}
				</div>
			</FormField>

			<FormField label="Draft" horizontal help="Hidden from readers until published">
				<Switch id="release-draft" bind:checked={draft} />
			</FormField>
			<FormField label="Pre-release" horizontal help="Marks the version as not ready for production">
				<Switch id="release-prerelease" bind:checked={prerelease} />
			</FormField>
		</div>
		{#snippet footer()}
			{#if onCancel}
				<Button variant="outline" onclick={onCancel} disabled={saving || publishing}>Cancel</Button>
			{/if}
			<Button variant={canPublish ? 'outline' : 'default'} onclick={save} disabled={!ready}>
				{saving ? 'Saving...' : draft ? 'Save Draft' : editing ? 'Save Changes' : 'Create Release'}
			</Button>
			{#if canPublish}
				<Button onclick={publish} disabled={!ready}>
					<Rocket class="h-3.5 w-3.5 mr-1.5" />{publishing ? 'Publishing...' : 'Publish Release'}
				</Button>
			{/if}
		{/snippet}
	</FormCard>

	<FormCard title="Assets" description={assetsHelp} icon={Package}>
		{#if current}
			<div class="space-y-3">
				{#if assets.length > 0}
					<ul class="rounded-xl border border-border/60 divide-y divide-border/40 overflow-hidden">
						{#each assets as a (a.artifactId)}
							<li class="flex items-center gap-3 px-4 py-2.5 text-[13px] min-w-0">
								<Download class="h-3.5 w-3.5 text-muted-foreground shrink-0" />
								<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
								<a href={a.downloadUrl} class="font-medium truncate min-w-0 flex-1 hover:text-primary">{a.name}</a>
								<span class="text-[12px] text-muted-foreground tabular-nums shrink-0">{formatBytes(Number(a.size))}</span>
								<Button variant="ghost" size="sm" class="h-7 px-2 text-destructive hover:text-destructive" disabled={detaching === a.artifactId} onclick={() => detachAsset(a)} title="Remove asset">
									<Trash2 class="h-3.5 w-3.5" />
								</Button>
							</li>
						{/each}
					</ul>
				{/if}
				<AssetUploader {namespace} {name} tag={current.tag} onUploaded={(a) => (assets = [...assets.filter((x) => x.artifactId !== a.artifactId), a])} />
			</div>
		{:else}
			{@render saveFirst('attach files')}
		{/if}
	</FormCard>

	<FormCard title="Images" description={imagesHelp} icon={Container}>
		{#if current}
			<ImagePicker
				{namespace}
				{name}
				tag={current.tag}
				{images}
				onAttached={(img) => (images = [...images.filter((x) => !sameImage(x, img)), img])}
				onDetached={(img) => (images = images.filter((x) => !sameImage(x, img)))}
			/>
		{:else}
			{@render saveFirst('pin images')}
		{/if}
	</FormCard>
</div>

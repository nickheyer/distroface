<script lang="ts">
	import {
		Tag,
		Sparkles,
		Eye,
		Pencil,
		Loader2,
		Rocket,
		Trash2,
		Download,
		Package,
		Container
	} from '@lucide/svelte';
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
	const tagError = $derived(
		tag.trim() !== '' && !tagValid
			? 'Tag names cannot contain spaces or git control characters'
			: ''
	);
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
	const assetsHelp = $derived(
		`Files stored in the ${namespace}/${name} artifact package at version ${tag.trim() || 'the tag'}`
	);
	const imagesHelp = $derived(
		`Linked images tagged ${tag.trim() || 'the tag'} appear automatically, pin others here`
	);

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
				{
					namespace,
					name,
					page: { pageSize: 10, pageToken: '', query: { text: t, filters: [] }, orderBy: '' }
				},
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
			await rpcClient.release.detachReleaseAsset({
				namespace,
				name,
				tag: current.tag,
				artifactId: a.artifactId
			});
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
	<div class="rounded-lg border border-dashed px-5 py-6 text-center">
		<p class="text-sm text-muted-foreground">Save a draft to {what}</p>
		<Button variant="outline" class="mt-3" onclick={saveDraft} disabled={!ready}>
			{#if saving}<Loader2 class="animate-spin" />{/if}
			{saving ? 'Saving' : 'Save draft'}
		</Button>
	</div>
{/snippet}

<div class="space-y-4">
	<FormCard title="Release" description="Tag, title, notes, and visibility" icon={Tag}>
		<div class="space-y-4">
			<FormField label="Tag" id="release-tag" required help={tagHelp} error={tagError}>
				{#if editing}
					<div class="flex h-8 items-center gap-2">
						<Tag class="size-3.5 text-muted-foreground" />
						<code class="font-mono text-sm">{tag}</code>
					</div>
				{:else}
					<div class="flex flex-wrap items-center gap-2">
						<Input
							id="release-tag"
							bind:value={tag}
							oninput={onTagInput}
							placeholder="v1.2.0"
							class="w-full font-mono sm:w-56"
							aria-invalid={!!tagError}
						/>
						<RefPicker
							{namespace}
							{name}
							current={tag}
							kinds={['tag']}
							label="Existing tags"
							onSelect={pickTag}
						/>
					</div>
				{/if}
			</FormField>

			{#if !editing && tagExists !== true}
				<FormField
					label="Target"
					id="release-target"
					help="Branch or tag the new tag is created from"
				>
					<RefPicker
						{namespace}
						{name}
						current={target}
						{defaultBranch}
						onSelect={(ref) => (target = ref)}
					/>
				</FormField>
			{/if}

			<FormField label="Title" id="release-title">
				<Input id="release-title" bind:value={title} placeholder={tag.trim() || 'Release title'} />
			</FormField>

			<FormField label="Notes" id="release-body">
				<div class="space-y-2">
					<div class="flex flex-wrap items-center gap-1 border-b">
						<button
							type="button"
							class="-mb-px inline-flex items-center gap-1.5 border-b-2 px-3 py-2 text-sm font-medium transition-colors {mode ===
							'write'
								? 'border-primary text-foreground'
								: 'border-transparent text-muted-foreground hover:text-foreground'}"
							onclick={() => (mode = 'write')}
						>
							<Pencil class="size-3.5" />Write
						</button>
						<button
							type="button"
							class="-mb-px inline-flex items-center gap-1.5 border-b-2 px-3 py-2 text-sm font-medium transition-colors {mode ===
							'preview'
								? 'border-primary text-foreground'
								: 'border-transparent text-muted-foreground hover:text-foreground'}"
							onclick={() => (mode = 'preview')}
						>
							<Eye class="size-3.5" />Preview
						</button>
						<Button
							variant="outline"
							size="sm"
							class="mb-1 ml-auto"
							onclick={generate}
							disabled={generating || !tag.trim()}
						>
							{#if generating}
								<Loader2 class="animate-spin" />
							{:else}
								<Sparkles />
							{/if}
							Generate notes
						</Button>
					</div>
					{#if mode === 'write'}
						<Textarea
							id="release-body"
							bind:value={body}
							rows={14}
							placeholder="Describe this release in markdown"
							class="font-mono"
						/>
					{:else}
						<div class="min-h-64 min-w-0 overflow-x-auto rounded-lg border px-5 py-4">
							{#if body.trim()}
								<Markdown source={body} />
							{:else}
								<p class="text-sm text-muted-foreground">Nothing to preview yet</p>
							{/if}
						</div>
					{/if}
				</div>
			</FormField>

			<FormField label="Draft" layout="toggle" help="Hidden from readers until published">
				<Switch id="release-draft" bind:checked={draft} />
			</FormField>
			<FormField
				label="Pre-release"
				layout="toggle"
				help="Marks the version as not ready for production"
			>
				<Switch id="release-prerelease" bind:checked={prerelease} />
			</FormField>
		</div>
		{#snippet footer()}
			{#if onCancel}
				<Button variant="outline" onclick={onCancel} disabled={saving || publishing}>Cancel</Button>
			{/if}
			<Button variant={canPublish ? 'outline' : 'default'} onclick={save} disabled={!ready}>
				{#if saving}<Loader2 class="animate-spin" />{/if}
				{saving ? 'Saving' : draft ? 'Save draft' : editing ? 'Save changes' : 'Create release'}
			</Button>
			{#if canPublish}
				<Button onclick={publish} disabled={!ready}>
					{#if publishing}<Loader2 class="animate-spin" />{:else}<Rocket />{/if}
					{publishing ? 'Publishing' : 'Publish release'}
				</Button>
			{/if}
		{/snippet}
	</FormCard>

	<FormCard title="Assets" description={assetsHelp} icon={Package}>
		{#if current}
			<div class="space-y-4">
				{#if assets.length > 0}
					<ul class="box divide-y">
						{#each assets as a (a.artifactId)}
							<li class="flex min-w-0 items-center gap-3 px-4 py-2.5 text-sm">
								<Download class="size-3.5 shrink-0 text-muted-foreground" />
								<!-- eslint-disable svelte/no-navigation-without-resolve -->
								<a
									href={a.downloadUrl}
									class="min-w-0 flex-1 truncate font-medium hover:text-primary">{a.name}</a
								>
								<!-- eslint-enable svelte/no-navigation-without-resolve -->
								<span class="shrink-0 text-xs text-muted-foreground tabular-nums"
									>{formatBytes(Number(a.size))}</span
								>
								<Button
									variant="ghost-muted"
									size="icon-sm"
									class="hover:text-destructive"
									disabled={detaching === a.artifactId}
									onclick={() => detachAsset(a)}
									title="Remove asset"
									aria-label="Remove asset"
								>
									{#if detaching === a.artifactId}<Loader2 class="animate-spin" />{:else}<Trash2
										/>{/if}
								</Button>
							</li>
						{/each}
					</ul>
				{/if}
				<AssetUploader
					{namespace}
					{name}
					tag={current.tag}
					onUploaded={(a) => (assets = [...assets.filter((x) => x.artifactId !== a.artifactId), a])}
				/>
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

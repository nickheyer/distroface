<script lang="ts">
	import { resolve } from '$app/paths';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime } from '$lib/utils';
	import UserAvatar from '$lib/components/user-avatar.svelte';
	import { detailOf, kindNoun, subjectHref, targetHref, targetLabel } from '$lib/activity';
	import {
		Archive,
		Building,
		CircleDot,
		Container,
		GitBranch,
		GitCommit,
		GitFork,
		GitMerge,
		GitPullRequest,
		Plus,
		Rocket,
		Star,
		Tag,
		Users,
		XCircle,
		CheckCircle
	} from '$lib/icons';
	import type { ActivityEvent } from '$lib/proto/distroface/v1/activity_pb';
	import type { Component } from 'svelte';

	let { event, compact = false }: { event: ActivityEvent; compact?: boolean } = $props();

	const detail = $derived(detailOf(event));
	const when = $derived(event.createdAt ? relativeTime(timestampDate(event.createdAt)) : '');
	const actorHref = $derived(resolve('/[namespace]', { namespace: event.actor || '' }));
	const target = $derived(targetHref(event));
	const subject = $derived(subjectHref(event));

	type Shape = { icon: Component<{ class?: string }>; tone: string; verb: string; object?: string };

	const shape = $derived.by<Shape>(() => {
		const d = detail;
		switch (event.kind) {
			case 'push': {
				const n = typeof d.commits === 'number' ? d.commits : 0;
				const more = d.more ? '+' : '';
				const count = n > 0 ? `${n}${more} commit${n === 1 && !more ? '' : 's'}` : 'commits';
				return {
					icon: GitCommit,
					tone: 'text-muted-foreground',
					verb: `pushed ${count} to`,
					object: event.subject
				};
			}
			case 'branch':
				return {
					icon: GitBranch,
					tone: 'text-muted-foreground',
					verb: d.action === 'deleted' ? 'deleted branch' : 'created branch',
					object: event.subject
				};
			case 'tag':
				return {
					icon: Tag,
					tone: 'text-muted-foreground',
					verb: 'pushed tag',
					object: event.subject
				};
			case 'image_push':
				return {
					icon: Container,
					tone: 'text-primary',
					verb: 'pushed image tag',
					object: event.subject
				};
			case 'artifact_upload':
				return {
					icon: Archive,
					tone: 'text-primary',
					verb: 'uploaded',
					object: event.summary || event.subject
				};
			case 'release':
				return {
					icon: Rocket,
					tone: 'text-success',
					verb: 'published release',
					object: event.summary || event.subject
				};
			case 'issue_opened':
				return {
					icon: CircleDot,
					tone: 'text-success',
					verb: 'opened issue',
					object: `#${event.subject}`
				};
			case 'issue_closed':
				return {
					icon: CheckCircle,
					tone: 'text-muted-foreground',
					verb: 'closed issue',
					object: `#${event.subject}`
				};
			case 'issue_reopened':
				return {
					icon: CircleDot,
					tone: 'text-success',
					verb: 'reopened issue',
					object: `#${event.subject}`
				};
			case 'pull_opened':
				return {
					icon: GitPullRequest,
					tone: 'text-success',
					verb: 'opened pull request',
					object: `#${event.subject}`
				};
			case 'pull_merged':
				return {
					icon: GitMerge,
					tone: 'text-primary',
					verb: 'merged pull request',
					object: `#${event.subject}`
				};
			case 'pull_closed':
				return {
					icon: XCircle,
					tone: 'text-destructive',
					verb: 'closed pull request',
					object: `#${event.subject}`
				};
			case 'star':
				return { icon: Star, tone: 'text-warning', verb: 'starred' };
			case 'fork':
				return {
					icon: GitFork,
					tone: 'text-muted-foreground',
					verb: 'forked',
					object: event.subject
				};
			case 'created':
				return { icon: Plus, tone: 'text-success', verb: `created ${kindNoun(event)}` };
			case 'joined':
				return { icon: Users, tone: 'text-primary', verb: 'joined' };
			case 'org_created':
				return { icon: Building, tone: 'text-success', verb: 'created organization' };
			default:
				return { icon: GitCommit, tone: 'text-muted-foreground', verb: event.kind };
		}
	});

	// Kinds whose subject line is a title worth a second row
	const showSummary = $derived(
		!compact &&
			!!event.summary &&
			[
				'push',
				'issue_opened',
				'issue_closed',
				'issue_reopened',
				'pull_opened',
				'pull_merged',
				'pull_closed'
			].includes(event.kind)
	);
	const objectIsLink = $derived(
		!['fork', 'artifact_upload', 'release'].includes(event.kind) || true
	);
	const Icon = $derived(shape.icon);
</script>

<!-- eslint-disable svelte/no-navigation-without-resolve -->
<article class="flex gap-3 {compact ? 'py-2' : 'px-4 py-3'}">
	<div class="relative shrink-0">
		{#if event.actor}
			<a href={actorHref} aria-label={event.actor}>
				<UserAvatar
					username={event.actor}
					displayName={event.actorDisplayName}
					avatarUrl={event.actorAvatarUrl}
					class={compact ? 'size-6' : 'size-8'}
				/>
			</a>
		{:else}
			<UserAvatar username="system" displayName="" class={compact ? 'size-6' : 'size-8'} />
		{/if}
		<span
			class="absolute -right-1 -bottom-1 flex size-4 items-center justify-center rounded-full border bg-card {shape.tone}"
		>
			<Icon class="size-2.5" />
		</span>
	</div>
	<div class="min-w-0 flex-1">
		<p class="text-sm leading-5">
			{#if event.actor}
				<a href={actorHref} class="font-semibold hover:text-primary"
					>{event.actorDisplayName || event.actor}</a
				>
			{:else}
				<span class="font-semibold">System</span>
			{/if}
			<span class="text-muted-foreground"> {shape.verb} </span>
			{#if shape.object}
				{#if objectIsLink}
					<a href={subject} class="font-medium hover:text-primary">{shape.object}</a>
				{:else}
					<span class="font-medium">{shape.object}</span>
				{/if}
				{#if event.kind !== 'fork'}
					<span class="text-muted-foreground"> in </span>
				{:else}
					<span class="text-muted-foreground"> from </span>
				{/if}
			{/if}
			<a href={target} class="font-medium hover:text-primary">{targetLabel(event)}</a>
			{#if when}
				<span class="ml-1.5 text-xs whitespace-nowrap text-muted-foreground">{when}</span>
			{/if}
		</p>
		{#if showSummary}
			<a
				href={subject}
				class="mt-0.5 block truncate text-sm text-muted-foreground hover:text-foreground"
			>
				{event.summary}
			</a>
		{/if}
	</div>
</article>
<!-- eslint-enable svelte/no-navigation-without-resolve -->

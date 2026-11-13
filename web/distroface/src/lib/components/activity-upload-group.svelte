<script lang="ts">
	import { resolve } from '$app/paths';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime } from '$lib/utils';
	import UserAvatar from '$lib/components/user-avatar.svelte';
	import BotBadge from '$lib/components/bot-badge.svelte';
	import { subjectHref, targetHref, targetLabel } from '$lib/activity';
	import { Archive, ChevronDown, ChevronRight } from '$lib/icons';
	import type { ActivityEvent } from '$lib/proto/distroface/v1/activity_pb';

	let { events, compact = false }: { events: ActivityEvent[]; compact?: boolean } = $props();

	const previewCount = 3;

	let open = $state(false);

	const first = $derived(events[0]);
	const actorHref = $derived(resolve('/[namespace]', { namespace: first.actor || '' }));
	const names = $derived([...new Set(events.map(fileName))]);
	const preview = $derived(names.slice(0, previewCount).join(', '));
	const hidden = $derived(names.length - Math.min(names.length, previewCount));

	function fileName(ev: ActivityEvent): string {
		return ev.summary || ev.subject;
	}

	function when(ev: ActivityEvent): string {
		return ev.createdAt ? relativeTime(timestampDate(ev.createdAt)) : '';
	}
</script>

<!-- eslint-disable svelte/no-navigation-without-resolve -->
<article class="flex gap-3 {compact ? 'py-2' : 'px-4 py-3'}">
	<div class="relative shrink-0 self-start">
		{#if first.actor}
			<a href={actorHref} aria-label={first.actor}>
				<UserAvatar
					username={first.actor}
					displayName={first.actorDisplayName}
					avatarUrl={first.actorAvatarUrl}
					class={compact ? 'size-6' : 'size-8'}
				/>
			</a>
		{:else}
			<UserAvatar username="system" displayName="" class={compact ? 'size-6' : 'size-8'} />
		{/if}
		<span
			class="absolute -right-1 -bottom-1 flex size-4 items-center justify-center rounded-full border bg-card text-primary"
		>
			<Archive class="size-2.5" />
		</span>
	</div>
	<div class="min-w-0 flex-1">
		<p class="text-sm leading-5">
			{#if first.actor}
				<a href={actorHref} class="font-semibold hover:text-primary"
					>{first.actorDisplayName || first.actor}</a
				>{#if first.actorIsAgent}<BotBadge class="ml-1 align-middle" />{/if}
			{:else}
				<span class="font-semibold">System</span>
			{/if}
			<span class="text-muted-foreground"> uploaded </span>
			<span class="font-medium">{events.length} files</span>
			<span class="text-muted-foreground"> to </span>
			<a href={targetHref(first)} class="font-medium hover:text-primary">{targetLabel(first)}</a>
			{#if when(first)}
				<span class="ml-1.5 text-xs whitespace-nowrap text-muted-foreground">{when(first)}</span>
			{/if}
		</p>
		<button
			type="button"
			class="mt-0.5 flex max-w-full items-center gap-1 text-left text-sm text-muted-foreground hover:text-foreground"
			aria-expanded={open}
			onclick={() => (open = !open)}
		>
			{#if open}
				<ChevronDown class="size-3.5 shrink-0" />
				<span>Hide files</span>
			{:else}
				<ChevronRight class="size-3.5 shrink-0" />
				<span class="truncate">{preview}</span>
				{#if hidden > 0}
					<span class="shrink-0">and {hidden} more</span>
				{/if}
			{/if}
		</button>
		{#if open}
			<ul class="mt-1.5 space-y-1 border-l pl-3">
				{#each events as ev (ev.id)}
					<li class="flex items-baseline gap-2 text-sm">
						<a href={subjectHref(ev)} class="min-w-0 truncate font-medium hover:text-primary"
							>{fileName(ev)}</a
						>
						<span class="shrink-0 text-xs text-muted-foreground">{when(ev)}</span>
					</li>
				{/each}
			</ul>
		{/if}
	</div>
</article>
<!-- eslint-enable svelte/no-navigation-without-resolve -->

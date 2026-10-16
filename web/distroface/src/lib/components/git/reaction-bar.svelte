<script lang="ts">
	import { ThumbsUp, ThumbsDown, Laugh, PartyPopper, Meh, Heart, Rocket, Eye, SmilePlus } from '@lucide/svelte';
	import { Popover, PopoverContent, PopoverTrigger } from '$lib/components/ui/popover';
	import { rpcClient } from '$lib/api/rpc-client';
	import type { ReactionGroup } from '$lib/proto/distroface/v1/issue_pb';
	import { cn } from '$lib/utils';

	let {
		namespace,
		name,
		number,
		commentId = '',
		reactions,
		canReact,
		onChanged
	}: {
		namespace: string;
		name: string;
		number: number;
		commentId?: string;
		reactions: ReactionGroup[];
		canReact: boolean;
		onChanged?: () => void;
	} = $props();

	// Fixed set the server accepts, in picker order
	const emojis = [
		{ key: '+1', label: 'Thumbs up', icon: ThumbsUp },
		{ key: '-1', label: 'Thumbs down', icon: ThumbsDown },
		{ key: 'laugh', label: 'Laugh', icon: Laugh },
		{ key: 'hooray', label: 'Hooray', icon: PartyPopper },
		{ key: 'confused', label: 'Confused', icon: Meh },
		{ key: 'heart', label: 'Heart', icon: Heart },
		{ key: 'rocket', label: 'Rocket', icon: Rocket },
		{ key: 'eyes', label: 'Eyes', icon: Eye }
	];

	let groups = $derived<ReactionGroup[]>(reactions);
	let pending = $state('');
	let open = $state(false);

	const visible = $derived(groups.filter((g) => g.count > 0));

	function meta(key: string) {
		return emojis.find((e) => e.key === key);
	}

	function tooltip(g: ReactionGroup): string {
		const who = g.usernames.slice(0, 6).join(', ');
		const more = g.usernames.length > 6 ? ` and ${g.usernames.length - 6} more` : '';
		return `${who}${more} reacted with ${meta(g.emoji)?.label.toLowerCase() ?? g.emoji}`;
	}

	async function toggle(emoji: string) {
		if (!canReact || pending) return;
		pending = emoji;
		open = false;
		const current = groups.find((g) => g.emoji === emoji);
		const req = { namespace, name, number: BigInt(number), commentId, emoji };
		try {
			const resp = current?.reacted
				? await rpcClient.issue.removeReaction(req)
				: await rpcClient.issue.addReaction(req);
			groups = resp.reactions;
			onChanged?.();
		} catch {
			// Error interceptor already toasted
		} finally {
			pending = '';
		}
	}
</script>

<div class="flex flex-wrap items-center gap-1.5">
	{#each visible as g (g.emoji)}
		{@const m = meta(g.emoji)}
		{@const Icon = m?.icon ?? SmilePlus}
		<button
			type="button"
			class={cn(
				'inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[12px] tabular-nums transition-colors',
				g.reacted ? 'border-primary/40 bg-primary/10 text-primary' : 'border-border/60 text-muted-foreground hover:bg-accent',
				(!canReact || pending) && 'cursor-default'
			)}
			title={tooltip(g)}
			aria-pressed={g.reacted}
			disabled={!canReact || !!pending}
			onclick={() => toggle(g.emoji)}
		>
			<Icon class="h-3.5 w-3.5" />
			<span>{g.count}</span>
		</button>
	{/each}
	{#if canReact}
		<Popover bind:open>
			<PopoverTrigger>
				{#snippet child({ props })}
					<button
						{...props}
						type="button"
						class="inline-flex h-6 w-6 items-center justify-center rounded-full border border-border/60 text-muted-foreground hover:bg-accent hover:text-foreground transition-colors"
						title="Add reaction"
						aria-label="Add reaction"
					>
						<SmilePlus class="h-3.5 w-3.5" />
					</button>
				{/snippet}
			</PopoverTrigger>
			<PopoverContent class="w-auto p-1.5" align="start">
				<div class="flex items-center gap-0.5">
					{#each emojis as e (e.key)}
						{@const Icon = e.icon}
						{@const active = groups.find((g) => g.emoji === e.key)?.reacted}
						<button
							type="button"
							class={cn(
								'h-8 w-8 rounded-md inline-flex items-center justify-center transition-colors hover:bg-accent',
								active ? 'bg-primary/10 text-primary' : 'text-muted-foreground hover:text-foreground'
							)}
							title={e.label}
							aria-label={e.label}
							onclick={() => toggle(e.key)}
						>
							<Icon class="h-4 w-4" />
						</button>
					{/each}
				</div>
			</PopoverContent>
		</Popover>
	{/if}
</div>

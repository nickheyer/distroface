<script lang="ts">
	import {
		ThumbsUp,
		ThumbsDown,
		Laugh,
		PartyPopper,
		Meh,
		Heart,
		Rocket,
		Eye,
		SmilePlus
	} from '$lib/icons';
	import { Popover, PopoverContent, PopoverTrigger } from '$lib/components/ui/popover';
	import { Button } from '$lib/components/ui/button';
	import { badgeVariants } from '$lib/components/ui/badge';
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
				badgeVariants({ variant: g.reacted ? 'default' : 'outline' }),
				'cursor-pointer tabular-nums',
				(!canReact || pending) && 'cursor-default'
			)}
			title={tooltip(g)}
			aria-pressed={g.reacted}
			disabled={!canReact || !!pending}
			onclick={() => toggle(g.emoji)}
		>
			<Icon />
			<span>{g.count}</span>
		</button>
	{/each}
	{#if canReact}
		<Popover bind:open>
			<PopoverTrigger>
				{#snippet child({ props })}
					<Button
						{...props}
						variant="outline"
						size="icon-xs"
						class="rounded-full text-muted-foreground"
						title="Add reaction"
						aria-label="Add reaction"
					>
						<SmilePlus />
					</Button>
				{/snippet}
			</PopoverTrigger>
			<PopoverContent class="w-auto p-1.5" align="start">
				<div class="flex items-center gap-0.5">
					{#each emojis as e (e.key)}
						{@const Icon = e.icon}
						{@const active = groups.find((g) => g.emoji === e.key)?.reacted}
						<Button
							variant="ghost-muted"
							size="icon"
							class={active ? 'bg-muted text-primary' : ''}
							title={e.label}
							aria-label={e.label}
							aria-pressed={active}
							onclick={() => toggle(e.key)}
						>
							<Icon />
						</Button>
					{/each}
				</div>
			</PopoverContent>
		</Popover>
	{/if}
</div>

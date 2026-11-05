<script lang="ts">
	import { resolve } from '$app/paths';
	import type { IssueUser } from '$lib/proto/distroface/v1/issue_pb';
	import { isExternalUser } from '$lib/pulls';
	import { cn } from '$lib/utils';

	let {
		user,
		fallback = 'unknown',
		hover = true,
		class: className
	}: {
		user?: IssueUser;
		fallback?: string;
		hover?: boolean;
		class?: string;
	} = $props();
</script>

{#if !user?.username}
	<span class={className}>{fallback}</span>
{:else if isExternalUser(user)}
	<span class={cn('cursor-default', className)} title="From the original forge"
		>{user.username}</span
	>
{:else}
	<a
		href={resolve('/[namespace]', { namespace: user.username })}
		class={cn('hover:text-primary', className)}
		data-hover={hover ? 'user' : undefined}
		data-user={hover ? user.username : undefined}>{user.username}</a
	>
{/if}

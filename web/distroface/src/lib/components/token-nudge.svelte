<script lang="ts">
	import { resolve } from '$app/paths';
	import { Button } from '$lib/components/ui/button';
	import { Terminal, X } from '$lib/icons';
	import { rpcClient, backgroundCallOptions } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { configStore } from '$lib/stores/config.svelte';

	let show = $state(false);
	let checkedFor = '';

	function storageKey(userId: string): string {
		return `df_cli_prompt_${userId}`;
	}

	// Local accounts use their password, SSO accounts cannot
	$effect(() => {
		const user = authStore.user;
		if (!user || user.id === checkedFor) return;
		checkedFor = user.id;
		show = false;
		if (user.authProvider === 'local' || !authStore.canCreateTokens) return;
		if (localStorage.getItem(storageKey(user.id))) return;
		rpcClient.token
			.listAPITokens({ page: { pageSize: 1 } }, backgroundCallOptions)
			.then((resp) => {
				if (resp.tokens.length === 0) show = true;
				else localStorage.setItem(storageKey(user.id), 'has-tokens');
			})
			.catch(() => {
				// Best effort, the tokens page stays reachable from settings
			});
	});

	function dismiss() {
		if (authStore.user) localStorage.setItem(storageKey(authStore.user.id), 'dismissed');
		show = false;
	}
</script>

{#if show}
	<div class="flex flex-wrap items-center gap-x-4 gap-y-3 rounded-lg border bg-card px-4 py-3">
		<Terminal class="size-5 shrink-0 text-muted-foreground" />
		<div class="min-w-0 flex-1 basis-64 text-sm">
			<p class="font-medium">
				Sign in from {configStore.gitEnabled ? 'docker and git' : 'docker'}
			</p>
			<p class="mt-0.5 text-muted-foreground">
				Your account uses single sign-on, so command line tools take an access token as the
				password.
			</p>
		</div>
		<div class="flex shrink-0 items-center gap-1">
			<Button size="sm" href={resolve('/settings/tokens/new')}>Create token</Button>
			<Button variant="ghost-muted" size="icon-sm" onclick={dismiss} aria-label="Dismiss">
				<X />
			</Button>
		</div>
	</div>
{/if}

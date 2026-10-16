<script lang="ts">
	import { ShieldCheck, ShieldAlert, ShieldQuestion } from '@lucide/svelte';
	import { Badge } from '$lib/components/ui/badge';
	import { GitVerificationState } from '$lib/proto/distroface/v1/git_pb';
	import type { GitVerification } from '$lib/proto/distroface/v1/git_pb';

	let {
		verification,
		compact = false
	}: {
		verification?: GitVerification;
		compact?: boolean;
	} = $props();

	const state = $derived(verification?.state ?? GitVerificationState.UNSPECIFIED);
	const title = $derived.by(() => {
		if (!verification) return '';
		switch (state) {
			case GitVerificationState.VERIFIED:
				return verification.signer
					? `Signed by ${verification.signer}${verification.keyId ? ` (${verification.keyId})` : ''}`
					: 'Signature verified';
			case GitVerificationState.INSTANCE:
				return 'Signed by this instance';
			case GitVerificationState.UNVERIFIED:
				return verification.reason || 'Signature could not be verified';
			default:
				return '';
		}
	});
</script>

{#if state === GitVerificationState.VERIFIED}
	<Badge variant="outline" class="gap-1 border-emerald-500/30 text-emerald-600 dark:text-emerald-400 text-[10px] h-4.5 py-0 px-1.5" {title}>
		<ShieldCheck class="h-3 w-3" />{#if !compact}Verified{/if}
	</Badge>
{:else if state === GitVerificationState.INSTANCE}
	<Badge variant="outline" class="gap-1 border-sky-500/30 text-sky-600 dark:text-sky-400 text-[10px] h-4.5 py-0 px-1.5" {title}>
		<ShieldCheck class="h-3 w-3" />{#if !compact}Instance{/if}
	</Badge>
{:else if state === GitVerificationState.UNVERIFIED}
	<Badge variant="outline" class="gap-1 border-amber-500/30 text-amber-600 dark:text-amber-400 text-[10px] h-4.5 py-0 px-1.5" {title}>
		<ShieldAlert class="h-3 w-3" />{#if !compact}Unverified{/if}
	</Badge>
{:else if state === GitVerificationState.UNSIGNED && !compact}
	<span class="sr-only"><ShieldQuestion class="h-3 w-3" />Unsigned</span>
{/if}

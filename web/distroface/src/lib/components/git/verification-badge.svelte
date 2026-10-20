<script lang="ts">
	import { ShieldCheck, ShieldAlert, ShieldQuestion } from '$lib/icons';
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
	<Badge variant="success" {title}>
		<ShieldCheck />{#if !compact}Verified{/if}
	</Badge>
{:else if state === GitVerificationState.INSTANCE}
	<Badge variant="default" {title}>
		<ShieldCheck />{#if !compact}Instance{/if}
	</Badge>
{:else if state === GitVerificationState.UNVERIFIED}
	<Badge variant="warning" {title}>
		<ShieldAlert />{#if !compact}Unverified{/if}
	</Badge>
{:else if state === GitVerificationState.UNSIGNED && !compact}
	<span class="sr-only"><ShieldQuestion class="size-3" />Unsigned</span>
{/if}

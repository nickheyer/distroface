<script lang="ts">
	import type { TLSMaterialInfo } from '$lib/proto/distroface/v1/certificate_pb';
	import { fmtDate } from '$lib/fmt';

	let { info }: { info: TLSMaterialInfo } = $props();
</script>

<dl class="docket" style="max-width: 40rem">
	<dt>Subject</dt>
	<dd class="mono">{info.subject || '—'}</dd>
	<dt>Issuer</dt>
	<dd class="mono">{info.issuer || '—'}</dd>
	<dt>Valid</dt>
	<dd class="mono">{fmtDate(info.notBefore)} to {fmtDate(info.notAfter)}</dd>
	{#if info.sans.length}
		<dt>Names</dt>
		<dd class="mono">{info.sans.join(', ')}</dd>
	{/if}
	{#if info.isCa}
		<dt>Authority</dt>
		<dd><span class="caps soft">certificate authority</span></dd>
	{/if}
	{#if info.createdBy}
		<dt>Created by</dt>
		<dd>{info.createdBy} <span class="mono faint">· {fmtDate(info.updatedAt)}</span></dd>
	{/if}
	{#if info.orphaned}
		<dt>Status</dt>
		<dd>
			<span class="mark bad">orphaned</span>
			<span class="note">No longer chains to the instance root.</span>
		</dd>
	{/if}
</dl>

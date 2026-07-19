import type { Timestamp } from '@bufbuild/protobuf/wkt';
import { timestampDate } from '@bufbuild/protobuf/wkt';
import { CertSource, CertState } from '$lib/proto/distroface/v1/certificate_pb';
import { OrgRole, Visibility, WebhookEvent } from '$lib/proto/distroface/v1/types_pb';
import { MTLSMode, SettingsTier, TLSMode } from '$lib/proto/distroface/v1/settings_pb';

const EMPTY = '—';

export function fmtBytes(n: bigint | number | undefined): string {
	if (n === undefined) return EMPTY;
	let v = Number(n);
	if (!isFinite(v) || v < 0) return EMPTY;
	if (v < 1000) return `${v} B`;
	const units = ['KB', 'MB', 'GB', 'TB', 'PB'];
	let u = -1;
	while (v >= 1000 && u < units.length - 1) {
		v /= 1000;
		u++;
	}
	return `${v >= 100 ? Math.round(v) : v.toFixed(1)} ${units[u]}`;
}

function real(ts?: Timestamp): Date | null {
	if (!ts || (ts.seconds === 0n && ts.nanos === 0)) return null;
	return timestampDate(ts);
}

const p2 = (n: number) => String(n).padStart(2, '0');

// Ledger dates are absolute, YYYY-MM-DD local
export function fmtDate(ts?: Timestamp): string {
	const d = real(ts);
	if (!d) return EMPTY;
	return `${d.getFullYear()}-${p2(d.getMonth() + 1)}-${p2(d.getDate())}`;
}

export function fmtWhen(ts?: Timestamp): string {
	const d = real(ts);
	if (!d) return EMPTY;
	return `${fmtDate(ts)} ${p2(d.getHours())}:${p2(d.getMinutes())}`;
}

export function fmtCount(n: bigint | number | undefined): string {
	if (n === undefined) return '0';
	return Number(n).toLocaleString('en-US');
}

export function digestShort(digest: string): string {
	const hex = digest.startsWith('sha256:') ? digest.slice(7) : digest;
	return hex.slice(0, 12);
}

export function fmtDuration(ms: bigint | number): string {
	const v = Number(ms);
	if (v < 1000) return `${v} ms`;
	return `${(v / 1000).toFixed(1)} s`;
}

// ── Enum labels ──────────────────────────────────────────────────────

export const certSourceLabel: Record<CertSource, string> = {
	[CertSource.UNSPECIFIED]: EMPTY,
	[CertSource.NONE]: 'cleartext',
	[CertSource.CONFIG]: 'config file',
	[CertSource.MANUAL]: 'uploaded',
	[CertSource.ACME]: 'acme',
	[CertSource.ORG_CA]: 'org ca',
	[CertSource.ORG_CERT]: 'org certificate',
	[CertSource.APP_CA]: 'instance ca'
};

export const certStateLabel: Record<CertState, string> = {
	[CertState.UNSPECIFIED]: EMPTY,
	[CertState.NONE]: 'cleartext',
	[CertState.PENDING]: 'pending',
	[CertState.READY]: 'ready',
	[CertState.ERROR]: 'error'
};

export type MarkKind = 'ok' | 'mid' | 'bad' | 'off';

export const certStateMark: Record<CertState, MarkKind> = {
	[CertState.UNSPECIFIED]: 'off',
	[CertState.NONE]: 'off',
	[CertState.PENDING]: 'mid',
	[CertState.READY]: 'ok',
	[CertState.ERROR]: 'bad'
};

export const orgRoleLabel: Record<OrgRole, string> = {
	[OrgRole.UNSPECIFIED]: EMPTY,
	[OrgRole.OWNER]: 'owner',
	[OrgRole.ADMIN]: 'admin',
	[OrgRole.MEMBER]: 'member'
};

export const visibilityLabel: Record<Visibility, string> = {
	[Visibility.UNSPECIFIED]: EMPTY,
	[Visibility.PUBLIC]: 'public',
	[Visibility.PRIVATE]: 'private'
};

export const webhookEventLabel: Record<WebhookEvent, string> = {
	[WebhookEvent.UNSPECIFIED]: EMPTY,
	[WebhookEvent.PUSH]: 'push',
	[WebhookEvent.PULL]: 'pull',
	[WebhookEvent.DELETE]: 'delete'
};

export const tierLabel: Record<SettingsTier, string> = {
	[SettingsTier.UNSPECIFIED]: EMPTY,
	[SettingsTier.DEFAULT]: 'default',
	[SettingsTier.FILE]: 'config file',
	[SettingsTier.SYSTEM]: 'system',
	[SettingsTier.ORG]: 'org',
	[SettingsTier.PORTAL]: 'portal'
};

export const tlsModeLabel: Record<TLSMode, string> = {
	[TLSMode.TLS_MODE_UNSPECIFIED]: EMPTY,
	[TLSMode.TLS_MODE_DUAL]: 'dual',
	[TLSMode.TLS_MODE_HTTPS_ONLY]: 'https only',
	[TLSMode.TLS_MODE_CLEARTEXT]: 'cleartext'
};

export const mtlsModeLabel: Record<MTLSMode, string> = {
	[MTLSMode.MTLS_MODE_UNSPECIFIED]: EMPTY,
	[MTLSMode.MTLS_MODE_OFF]: 'off',
	[MTLSMode.MTLS_MODE_OPTIONAL]: 'optional',
	[MTLSMode.MTLS_MODE_REQUIRED]: 'required'
};

import { timestampDate, type Timestamp } from '@bufbuild/protobuf/wkt';
import {
	CertSource,
	CertState,
	type CertificateInfo
} from '$lib/proto/distroface/v1/certificate_pb';
import { hostnamePattern } from '$lib/portal-address';
import type { BadgeVariant } from '$lib/components/ui/badge';

export const certSourceLabels: Record<CertSource, string> = {
	[CertSource.UNSPECIFIED]: 'None (cleartext only)',
	[CertSource.NONE]: 'None (cleartext only)',
	[CertSource.CONFIG]: 'Config file pair',
	[CertSource.MANUAL]: 'Uploaded certificate',
	[CertSource.ACME]: 'ACME (automatic)',
	[CertSource.ORG_CA]: 'Organization CA',
	[CertSource.ORG_CERT]: 'Organization certificate',
	[CertSource.APP_CA]: 'Instance CA (self signed)'
};

export type CertStateBadge = { label: string; variant: BadgeVariant };

// Badge presentation for a computed cert state, null hides the badge
export function certStateBadge(state: CertState | undefined): CertStateBadge | null {
	switch (state) {
		case CertState.READY:
			return { label: 'https', variant: 'success' };
		case CertState.PENDING:
			return { label: 'https pending', variant: 'warning' };
		case CertState.ERROR:
			return { label: 'https broken', variant: 'destructive' };
		default:
			return null;
	}
}

export type CertHealth = {
	issued: boolean;
	expired: boolean;
	daysLeft: number;
	label: string;
	tone: 'ok' | 'warn' | 'danger' | 'none';
};

// Autocert renews inside thirty days, warn when overdue
export function certHealth(cert?: CertificateInfo): CertHealth {
	if (!cert?.issued || !cert.notAfter) {
		return { issued: false, expired: false, daysLeft: 0, label: 'Not issued', tone: 'none' };
	}
	const msLeft = timestampDate(cert.notAfter).getTime() - Date.now();
	const daysLeft = Math.floor(msLeft / 86_400_000);
	if (msLeft <= 0) {
		return { issued: true, expired: true, daysLeft, label: 'Expired', tone: 'danger' };
	}
	const label = daysLeft === 0 ? 'Expires today' : `Expires in ${daysLeft}d`;
	if (daysLeft < 7) return { issued: true, expired: false, daysLeft, label, tone: 'danger' };
	if (daysLeft < 21) return { issued: true, expired: false, daysLeft, label, tone: 'warn' };
	return { issued: true, expired: false, daysLeft, label, tone: 'ok' };
}

export function certHealthVariant(tone: CertHealth['tone']): BadgeVariant {
	switch (tone) {
		case 'ok':
			return 'success';
		case 'warn':
			return 'warning';
		case 'danger':
			return 'destructive';
		default:
			return 'outline';
	}
}

export function certDate(cert?: { notAfter?: Timestamp }): string {
	if (!cert?.notAfter) return '';
	return timestampDate(cert.notAfter).toLocaleDateString(undefined, {
		year: 'numeric',
		month: 'short',
		day: 'numeric'
	});
}

// Public dns name acme can plausibly issue for
export function isIssuableHostname(host: string): boolean {
	const h = host.trim().toLowerCase();
	if (!h || h === 'localhost' || !h.includes('.') || h.includes(':')) return false;
	if (/^\d{1,3}(\.\d{1,3}){3}$/.test(h)) return false;
	return hostnamePattern.test(h);
}

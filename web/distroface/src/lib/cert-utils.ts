import { timestampDate } from '@bufbuild/protobuf/wkt';
import type { CertificateInfo } from '$lib/proto/distroface/v1/certificate_pb';
import { hostnamePattern } from '$lib/portal-address';

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

export function certBadgeClass(tone: CertHealth['tone']): string {
	switch (tone) {
		case 'ok':
			return 'border-primary/30 text-primary';
		case 'warn':
			return 'border-amber-500/40 text-amber-600 dark:text-amber-400';
		case 'danger':
			return 'border-destructive/40 text-destructive';
		default:
			return 'text-muted-foreground';
	}
}

export function certDate(cert?: CertificateInfo): string {
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

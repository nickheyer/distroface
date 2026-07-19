import { site } from '$lib/state/site.svelte';
import { CertSource } from '$lib/proto/distroface/v1/certificate_pb';

// Lowercase host, no port
export const hostnamePattern = /^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$/;

// Configured app host and port, the hostname may embed its own port
export function appHostPort(): { host: string; port: string } {
	const hostname = site.publicHostname;
	const idx = hostname.lastIndexOf(':');
	const tail = idx > -1 ? hostname.slice(idx + 1) : '';
	let host = hostname;
	let port = '';
	if (/^\d+$/.test(tail)) {
		host = hostname.slice(0, idx);
		port = tail;
	}
	if (port === '80' || port === '443') port = '';
	return { host: host || 'localhost', port };
}

// Address clients actually dial, inheriting the configured app host and port
export function effectiveAddress(hostname: string, port: number): string {
	const app = appHostPort();
	const host = hostname || app.host;
	const portPart = port > 0 ? `:${port}` : app.port ? `:${app.port}` : '';
	return host + portPart;
}

// Explicit sources answer https, none stays cleartext
export function portalScheme(certSource: CertSource | undefined): 'http' | 'https' {
	return certSource && certSource !== CertSource.NONE ? 'https' : 'http';
}

export function portalUrl(hostname: string, port: number, certSource: CertSource | undefined): string {
	return `${portalScheme(certSource)}://${effectiveAddress(hostname, port)}`;
}

// Validation message for a hostname/port pair, empty when valid
export function placementError(hostname: string, port: string): string {
	const h = hostname.trim().toLowerCase();
	const p = port.trim();
	if (h === '' && p === '') return 'Set a hostname, a port, or both';
	if (p !== '') {
		if (!/^\d+$/.test(p)) return 'Port must be a number';
		const n = Number(p);
		if (n < 1 || n > 65535) return 'Port must be 1-65535';
	}
	if (h !== '' && !hostnamePattern.test(h)) {
		return 'Hostname may contain lowercase letters, digits, dots, and hyphens';
	}
	return '';
}

// Client side blob save, used for pem downloads
export function saveBlob(content: string | Blob, filename: string, type = 'application/x-pem-file') {
	const blob = typeof content === 'string' ? new Blob([content], { type }) : content;
	const url = URL.createObjectURL(blob);
	const a = document.createElement('a');
	a.href = url;
	a.download = filename;
	a.click();
	URL.revokeObjectURL(url);
}

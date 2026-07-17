// Lowercase host, no port
export const hostnamePattern = /^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$/;

// Address clients actually dial, inheriting the app's host and port when unset
export function effectiveAddress(hostname: string, port: number): string {
	const host = hostname || window.location.hostname;
	const portPart = port > 0 ? `:${port}` : window.location.port ? `:${window.location.port}` : '';
	return host + portPart;
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

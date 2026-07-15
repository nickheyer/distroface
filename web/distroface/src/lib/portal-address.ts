export type ParsedAddress = { hostname: string; port: number; error: string };

const hostnamePattern = /^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$/;

export function parseAddress(raw: string): ParsedAddress {
	const text = raw.trim().toLowerCase();
	if (text === '') return { hostname: '', port: 0, error: 'Enter a hostname or port' };

	const idx = text.lastIndexOf(':');
	const hostname = idx === -1 ? text : text.slice(0, idx);
	let port = 0;
	if (idx !== -1) {
		const portPart = text.slice(idx + 1);
		if (!/^\d+$/.test(portPart)) return { hostname, port: 0, error: 'Invalid port' };
		port = Number(portPart);
		if (port < 1 || port > 65535) return { hostname, port: 0, error: 'Port must be 1-65535' };
	}
	if (hostname !== '' && !hostnamePattern.test(hostname)) {
		return { hostname, port, error: 'Invalid hostname' };
	}
	return { hostname, port, error: '' };
}

export function formatAddress(hostname: string, port: number): string {
	if (hostname === '') return `:${port}`;
	return port > 0 ? `${hostname}:${port}` : hostname;
}

import { clsx, type ClassValue } from 'clsx';
import { twMerge } from 'tailwind-merge';
import { OrgRole, Visibility, WebhookEvent } from '$lib/proto/distroface/v1/types_pb';

export function cn(...inputs: ClassValue[]) {
	return twMerge(clsx(inputs));
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export type WithoutChild<T> = T extends { child?: any } ? Omit<T, 'child'> : T;
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export type WithoutChildren<T> = T extends { children?: any } ? Omit<T, 'children'> : T;
export type WithoutChildrenOrChild<T> = WithoutChildren<WithoutChild<T>>;
export type WithElementRef<T, U extends HTMLElement = HTMLElement> = T & { ref?: U | null };

export function formatBytes(bytes: number, decimals = 2): string {
	if (bytes === 0) return '0 Bytes';

	const k = 1024;
	const dm = decimals < 0 ? 0 : decimals;
	const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB', 'PB', 'EB', 'ZB', 'YB'];

	const i = Math.floor(Math.log(bytes) / Math.log(k));

	return parseFloat((bytes / Math.pow(k, i)).toFixed(dm)) + ' ' + sizes[i];
}

export function getStringForEnum(map: any, val: unknown) {
	return Object.keys(map).find((key) => map[key] === val);
}

export function pageToToken(page: number, pageSize: number): string {
	if (page <= 1) return '';
	return btoa(String((page - 1) * pageSize));
}

export function enumToString(map: any, val: unknown): string {
	const enumKey = getStringForEnum(map, val);
	if (!enumKey) return '';
	const parts = enumKey.split('_');
	if (parts.length > 2) {
		return parts.slice(2).join('_').toLowerCase();
	}
	return enumKey.toLowerCase();
}

export function relativeTime(date: Date | string): string {
	const now = new Date();
	const d = typeof date === 'string' ? new Date(date) : date;
	const diffMs = now.getTime() - d.getTime();
	const diffSec = Math.floor(diffMs / 1000);
	const diffMin = Math.floor(diffSec / 60);
	const diffHour = Math.floor(diffMin / 60);
	const diffDay = Math.floor(diffHour / 24);
	const diffWeek = Math.floor(diffDay / 7);
	const diffMonth = Math.floor(diffDay / 30);
	const diffYear = Math.floor(diffDay / 365);

	if (diffSec < 60) return 'just now';
	if (diffMin < 60) return `${diffMin}m ago`;
	if (diffHour < 24) return `${diffHour}h ago`;
	if (diffDay < 7) return `${diffDay}d ago`;
	if (diffWeek < 5) return `${diffWeek}w ago`;
	if (diffMonth < 12) return `${diffMonth}mo ago`;
	return `${diffYear}y ago`;
}

export function truncateDigest(digest: string, len = 16): string {
	if (!digest) return '';
	if (digest.startsWith('sha256:')) {
		return `sha256:${digest.slice(7, 7 + len)}`;
	}
	return digest.slice(0, len);
}

export function visibilityLabel(v: number): string {
	switch (v) {
		case Visibility.PUBLIC:
			return 'Public';
		case Visibility.PRIVATE:
			return 'Private';
		default:
			return 'Public';
	}
}

export function toggleInArray<T>(arr: T[], item: T): T[] {
	return arr.includes(item) ? arr.filter((x) => x !== item) : [...arr, item];
}

export const webhookEventLabels: Record<number, string> = {
	[WebhookEvent.PUSH]: 'push',
	[WebhookEvent.PULL]: 'pull',
	[WebhookEvent.DELETE]: 'delete'
};

export function orgRoleLabel(role: number): string {
	switch (role) {
		case OrgRole.OWNER:
			return 'Owner';
		case OrgRole.ADMIN:
			return 'Admin';
		case OrgRole.MEMBER:
			return 'Member';
		default:
			return 'Unknown';
	}
}

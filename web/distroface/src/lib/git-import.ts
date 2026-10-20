import type { GitImportStatus } from '$lib/proto/distroface/v1/git_pb';

// Badge text for a running, failed, or noted import
export function importLabel(status: GitImportStatus): string {
	if (status.error) return 'Import failed';
	if (status.phase === 'done') return 'Imported';
	return 'Importing';
}

// Hover text, live progress beats the stored phase
export function importTitle(status: GitImportStatus, progress = ''): string {
	const parts: string[] = [];
	if (status.upstream) parts.push(`From ${status.upstream}`);
	if (progress) parts.push(progress);
	else if (status.phase && status.phase !== 'done') parts.push(`Phase: ${status.phase}`);
	if (status.error) parts.push(status.error);
	parts.push(...status.notes);
	return parts.join('\n');
}

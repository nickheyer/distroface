import { ActionsConclusion, ActionsStatus } from '$lib/proto/distroface/v1/actions_pb';
import type { WorkflowRun } from '$lib/proto/distroface/v1/actions_pb';
import { timestampDate, type Timestamp } from '@bufbuild/protobuf/wkt';

export type RunStateKey =
	| 'queued'
	| 'waiting'
	| 'approval'
	| 'running'
	| 'success'
	| 'failure'
	| 'cancelled'
	| 'skipped'
	| 'timed_out'
	| 'neutral'
	| 'unknown';

export const stateLabels: Record<RunStateKey, string> = {
	queued: 'Queued',
	waiting: 'Waiting',
	approval: 'Waiting for approval',
	running: 'In progress',
	success: 'Success',
	failure: 'Failed',
	cancelled: 'Cancelled',
	skipped: 'Skipped',
	timed_out: 'Timed out',
	neutral: 'Neutral',
	unknown: 'Unknown'
};

export function runState(
	status: ActionsStatus,
	conclusion: ActionsConclusion,
	needsApproval = false
): RunStateKey {
	switch (status) {
		case ActionsStatus.WAITING:
			return needsApproval ? 'approval' : 'waiting';
		case ActionsStatus.QUEUED:
			return 'queued';
		case ActionsStatus.RUNNING:
			return 'running';
		case ActionsStatus.COMPLETED:
			switch (conclusion) {
				case ActionsConclusion.SUCCESS:
					return 'success';
				case ActionsConclusion.FAILURE:
					return 'failure';
				case ActionsConclusion.CANCELLED:
					return 'cancelled';
				case ActionsConclusion.SKIPPED:
					return 'skipped';
				case ActionsConclusion.TIMED_OUT:
					return 'timed_out';
				case ActionsConclusion.NEUTRAL:
					return 'neutral';
				default:
					return 'unknown';
			}
		default:
			return 'unknown';
	}
}

export function isActive(status: ActionsStatus): boolean {
	return (
		status === ActionsStatus.WAITING ||
		status === ActionsStatus.QUEUED ||
		status === ActionsStatus.RUNNING
	);
}

export function formatDuration(ms: number): string {
	const s = Math.max(0, Math.floor(ms / 1000));
	if (s < 60) return `${s}s`;
	const m = Math.floor(s / 60);
	if (m < 60) return `${m}m ${s % 60}s`;
	const h = Math.floor(m / 60);
	return `${h}h ${m % 60}m`;
}

// Elapsed milliseconds, open ended while unfinished
export function durationOf(
	started?: Timestamp,
	finished?: Timestamp,
	now = Date.now()
): number | undefined {
	if (!started) return undefined;
	const a = timestampDate(started).getTime();
	const b = finished ? timestampDate(finished).getTime() : now;
	return b - a;
}

export interface Annotation {
	level: string;
	message: string;
	title?: string;
	file?: string;
	line?: number;
	end_line?: number;
	col?: number;
	end_col?: number;
	step?: number;
}

export function parseAnnotations(raw: string): Annotation[] {
	if (!raw) return [];
	try {
		const v = JSON.parse(raw);
		if (!Array.isArray(v)) return [];
		return v
			.filter((a) => a && typeof a === 'object' && typeof a.message === 'string')
			.map((a) => ({ ...a, level: String(a.level || 'notice') }));
	} catch {
		return [];
	}
}

export function parseOutputs(raw: string): Array<[string, string]> {
	if (!raw) return [];
	try {
		const v = JSON.parse(raw);
		if (!v || typeof v !== 'object' || Array.isArray(v)) return [];
		return Object.entries(v).map(([k, val]) => [
			k,
			typeof val === 'string' ? val : JSON.stringify(val)
		]);
	} catch {
		return [];
	}
}

export interface RefInfo {
	kind: 'branch' | 'tag' | 'ref';
	name: string;
}

export function refInfo(ref: string): RefInfo {
	if (ref.startsWith('refs/heads/')) return { kind: 'branch', name: ref.slice(11) };
	if (ref.startsWith('refs/tags/')) return { kind: 'tag', name: ref.slice(10) };
	if (ref.startsWith('refs/pull/')) return { kind: 'ref', name: ref.slice(5) };
	return { kind: ref ? 'branch' : 'ref', name: ref };
}

const eventLabels: Record<string, string> = {
	push: 'Push',
	pull_request: 'Pull request',
	workflow_dispatch: 'Manual',
	schedule: 'Schedule',
	release: 'Release',
	issues: 'Issues',
	issue_comment: 'Issue comment',
	create: 'Create',
	delete: 'Delete'
};

// Paste ready registration for a runner host
export function runnerRegisterCommand(serverUrl: string, token: string | null): string {
	return `dfcli runner register --server ${serverUrl} --token ${token ?? 'REGISTRATION_TOKEN'} --name my-runner --labels ubuntu-latest`;
}

const scopeLabels: Record<string, string> = {
	system: 'Instance',
	org: 'Organization',
	repo: 'Repository'
};

export function scopeLabel(kind?: string): string {
	return scopeLabels[kind ?? ''] ?? 'Repository';
}

export function eventLabel(event: string): string {
	return eventLabels[event] ?? (event ? event.replace(/_/g, ' ') : 'Unknown');
}

export function runTitle(run: WorkflowRun): string {
	const r = refInfo(run.ref);
	switch (run.event) {
		case 'push':
			return r.kind === 'tag' ? `Tag ${r.name} pushed` : `Push to ${r.name}`;
		case 'pull_request':
			return run.pullRequestNumber ? `Pull request !${run.pullRequestNumber}` : 'Pull request';
		case 'workflow_dispatch':
			return r.name ? `Manual run on ${r.name}` : 'Manual run';
		case 'schedule':
			return 'Scheduled run';
		case 'release':
			return r.name ? `Release ${r.name}` : 'Release';
		default:
			return r.name ? `${eventLabel(run.event)} on ${r.name}` : eventLabel(run.event);
	}
}

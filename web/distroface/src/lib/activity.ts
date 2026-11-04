import { resolve } from '$app/paths';
import { ActivityTargetKind, type ActivityEvent } from '$lib/proto/distroface/v1/activity_pb';

// Chip groups the feed filters by
export type ActivityGroup = {
	key: string;
	label: string;
	kinds: string[];
};

export const activityGroups: ActivityGroup[] = [
	{ key: 'code', label: 'Code', kinds: ['push', 'branch', 'tag'] },
	{
		key: 'work',
		label: 'Issues and pulls',
		kinds: [
			'issue_opened',
			'issue_closed',
			'issue_reopened',
			'pull_opened',
			'pull_merged',
			'pull_closed'
		]
	},
	{ key: 'releases', label: 'Releases', kinds: ['release'] },
	{ key: 'images', label: 'Images', kinds: ['image_push'] },
	{ key: 'artifacts', label: 'Artifacts', kinds: ['artifact_upload'] },
	{ key: 'social', label: 'Stars and forks', kinds: ['star', 'fork', 'created'] },
	{ key: 'people', label: 'People', kinds: ['joined', 'org_created'] }
];

// Page of the thing an event touched
export function targetHref(ev: ActivityEvent): string {
	switch (ev.targetKind) {
		case ActivityTargetKind.IMAGE:
			return resolve('/images/[namespace]/[...name]', { namespace: ev.namespace, name: ev.name });
		case ActivityTargetKind.ARTIFACT:
			return resolve('/artifacts/[namespace]/[repo]', { namespace: ev.namespace, repo: ev.name });
		case ActivityTargetKind.ORGANIZATION:
			return resolve('/orgs/[name]', { name: ev.namespace });
		default:
			return resolve('/[namespace]/[name]', { namespace: ev.namespace, name: ev.name });
	}
}

// Page of the subject when the kind has one, the target page otherwise
export function subjectHref(ev: ActivityEvent): string {
	const p = { namespace: ev.namespace, name: ev.name };
	switch (ev.kind) {
		case 'push':
			return resolve('/[namespace]/[name]/commits/[ref]', { ...p, ref: ev.subject });
		case 'branch':
			return resolve('/[namespace]/[name]/branches', p);
		case 'tag':
			return resolve('/[namespace]/[name]/tags', p);
		case 'release':
			return resolve('/[namespace]/[name]/releases/[tag]', { ...p, tag: ev.subject });
		case 'issue_opened':
		case 'issue_closed':
		case 'issue_reopened':
			return resolve('/[namespace]/[name]/issues/[number]', { ...p, number: ev.subject });
		case 'pull_opened':
		case 'pull_merged':
		case 'pull_closed':
			return resolve('/[namespace]/[name]/pulls/[number]', { ...p, number: ev.subject });
		case 'fork': {
			const [ns = '', n = ''] = ev.subject.split('/');
			return resolve('/[namespace]/[name]', { namespace: ns, name: n });
		}
		default:
			return targetHref(ev);
	}
}

export function targetLabel(ev: ActivityEvent): string {
	return ev.targetKind === ActivityTargetKind.ORGANIZATION
		? ev.namespace
		: `${ev.namespace}/${ev.name}`;
}

// Decoded detail object, empty when malformed
export function detailOf(ev: ActivityEvent): Record<string, unknown> {
	try {
		const d = JSON.parse(ev.detail || '{}');
		return d && typeof d === 'object' ? (d as Record<string, unknown>) : {};
	} catch {
		return {};
	}
}

// Noun for the repository kind an event touched
export function kindNoun(ev: ActivityEvent): string {
	switch (ev.targetKind) {
		case ActivityTargetKind.IMAGE:
			return 'image repository';
		case ActivityTargetKind.ARTIFACT:
			return 'artifact repository';
		case ActivityTargetKind.ORGANIZATION:
			return 'organization';
		default:
			return 'repository';
	}
}

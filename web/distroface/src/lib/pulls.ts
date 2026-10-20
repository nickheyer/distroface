import { GitMergeMethod, CommitStatusState } from '$lib/proto/distroface/v1/git_pb';
import { ReviewState } from '$lib/proto/distroface/v1/pull_pb';
import type { PullRequest, Review, ReviewComment } from '$lib/proto/distroface/v1/pull_pb';
import { IssueState } from '$lib/proto/distroface/v1/issue_pb';

export type PullStatus = 'open' | 'draft' | 'merged' | 'closed';

export function pullStatus(p: PullRequest): PullStatus {
	if (p.mergedAt) return 'merged';
	if (p.issue?.state === IssueState.CLOSED) return 'closed';
	if (p.draft) return 'draft';
	return 'open';
}

export function pullStatusLabel(s: PullStatus): string {
	switch (s) {
		case 'merged':
			return 'Merged';
		case 'closed':
			return 'Closed';
		case 'draft':
			return 'Draft';
		default:
			return 'Open';
	}
}

export function mergeMethodLabel(m: GitMergeMethod): string {
	switch (m) {
		case GitMergeMethod.MERGE:
			return 'Create a merge commit';
		case GitMergeMethod.SQUASH:
			return 'Squash and merge';
		case GitMergeMethod.REBASE:
			return 'Rebase and merge';
		case GitMergeMethod.REBASE_MERGE:
			return 'Rebase then merge commit';
		case GitMergeMethod.FAST_FORWARD:
			return 'Fast forward only';
		default:
			return 'Merge';
	}
}

export function mergeMethodShort(m: GitMergeMethod): string {
	switch (m) {
		case GitMergeMethod.SQUASH:
			return 'squash';
		case GitMergeMethod.REBASE:
			return 'rebase';
		case GitMergeMethod.REBASE_MERGE:
			return 'rebase and merge';
		case GitMergeMethod.FAST_FORWARD:
			return 'fast forward';
		default:
			return 'merge commit';
	}
}

export function reviewStateLabel(s: ReviewState): string {
	switch (s) {
		case ReviewState.APPROVED:
			return 'Approved';
		case ReviewState.CHANGES_REQUESTED:
			return 'Changes requested';
		case ReviewState.COMMENTED:
			return 'Commented';
		case ReviewState.PENDING:
			return 'Pending';
		default:
			return 'Reviewed';
	}
}

const blockerLabels: Record<string, string> = {
	approvals: 'Required approvals',
	code_owners: 'Code owner review',
	status_checks: 'Required status checks',
	changes_requested: 'Changes requested',
	signed_commits: 'Signed commits',
	linear_history: 'Linear history',
	up_to_date: 'Branch up to date',
	merger: 'Allowed mergers',
	conflict: 'Merge conflicts',
	draft: 'Draft',
	closed: 'Closed',
	archived: 'Archived'
};

export function blockerLabel(rule: string): string {
	return blockerLabels[rule] ?? rule.replace(/_/g, ' ');
}

export function statusLabel(s: CommitStatusState): string {
	switch (s) {
		case CommitStatusState.SUCCESS:
			return 'All checks passed';
		case CommitStatusState.FAILURE:
			return 'Some checks failed';
		case CommitStatusState.ERROR:
			return 'Some checks errored';
		case CommitStatusState.PENDING:
			return 'Checks pending';
		default:
			return 'No checks';
	}
}

// Fork heads carry their repo prefix
export function headLabel(p: PullRequest, repoFullName: string): string {
	if (p.headRepoFullName && p.headRepoFullName !== repoFullName)
		return `${p.headRepoFullName}:${p.headBranch}`;
	return p.headBranch;
}

export function headIsFork(p: PullRequest, repoFullName: string): boolean {
	return !!p.headRepoFullName && p.headRepoFullName !== repoFullName;
}

// Newest submitted review per reviewer, dismissed ones included
export function latestReviews(reviews: Review[]): Review[] {
	const byUser = new Map<string, Review>();
	for (const r of reviews) {
		if (r.state === ReviewState.PENDING || !r.reviewer) continue;
		const prev = byUser.get(r.reviewer.id);
		if (!prev || toMillis(r) >= toMillis(prev)) byUser.set(r.reviewer.id, r);
	}
	return [...byUser.values()];
}

function toMillis(r: Review): number {
	const ts = r.submittedAt ?? r.createdAt;
	return ts ? Number(ts.seconds) * 1000 + ts.nanos / 1e6 : 0;
}

export interface ReviewSummary {
	approved: number;
	changesRequested: number;
	commented: number;
	awaiting: number;
}

export function reviewSummary(p: PullRequest): ReviewSummary {
	const out: ReviewSummary = { approved: 0, changesRequested: 0, commented: 0, awaiting: 0 };
	for (const r of latestReviews(p.reviews)) {
		if (r.dismissed) continue;
		if (r.state === ReviewState.APPROVED) out.approved++;
		else if (r.state === ReviewState.CHANGES_REQUESTED) out.changesRequested++;
		else out.commented++;
	}
	out.awaiting = p.reviewRequests.filter((r) => !r.fulfilled).length;
	return out;
}

export type BodySegment = { kind: 'text'; text: string } | { kind: 'suggestion'; code: string };

// Splits a comment body around suggestion fences
export function parseSuggestions(body: string): BodySegment[] {
	const out: BodySegment[] = [];
	const re = /```suggestion[^\n]*\n([\s\S]*?)```/g;
	let last = 0;
	let m: RegExpExecArray | null;
	while ((m = re.exec(body)) !== null) {
		const before = body.slice(last, m.index);
		if (before.trim()) out.push({ kind: 'text', text: before });
		out.push({ kind: 'suggestion', code: m[1].replace(/\n$/, '') });
		last = m.index + m[0].length;
	}
	const rest = body.slice(last);
	if (rest.trim() || out.length === 0) out.push({ kind: 'text', text: rest });
	return out;
}

export function hasSuggestion(body: string): boolean {
	return /```suggestion/.test(body);
}

// Threads keyed by root id, root first
export function groupThreads(comments: ReviewComment[]): Map<string, ReviewComment[]> {
	const threads = new Map<string, ReviewComment[]>();
	for (const c of comments) {
		const key = c.threadId || c.id;
		const list = threads.get(key) ?? [];
		if (c.id === key) list.unshift(c);
		else list.push(c);
		threads.set(key, list);
	}
	return threads;
}

export function threadKey(path: string, side: string, line: number): string {
	return `${path}\n${side}\n${line}`;
}

export function reviewAnchor(id: string): string {
	return `review-comment-${id}`;
}

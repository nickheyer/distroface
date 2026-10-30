import type { Component } from 'svelte';
import { Code, MessageSquare, SearchCheck, Tags, Wrench } from '$lib/icons';
import {
	AgentApprovalPolicy,
	AgentKind,
	AgentProviderKind,
	AgentSessionConclusion,
	AgentSessionStatus,
	type AgentSession,
	type AgentTriggers
} from '$lib/proto/distroface/v1/agents_pb';

export type SessionStateKey =
	| 'queued'
	| 'running'
	| 'approval'
	| 'success'
	| 'failure'
	| 'cancelled'
	| 'timed_out'
	| 'rejected'
	| 'budget'
	| 'unknown';

export const sessionStateLabels: Record<SessionStateKey, string> = {
	queued: 'Queued',
	running: 'Running',
	approval: 'Waiting for approval',
	success: 'Completed',
	failure: 'Failed',
	cancelled: 'Cancelled',
	timed_out: 'Timed out',
	rejected: 'Rejected',
	budget: 'Budget exhausted',
	unknown: 'Unknown'
};

export function sessionState(
	status: AgentSessionStatus,
	conclusion: AgentSessionConclusion
): SessionStateKey {
	switch (status) {
		case AgentSessionStatus.QUEUED:
			return 'queued';
		case AgentSessionStatus.RUNNING:
			return 'running';
		case AgentSessionStatus.WAITING_APPROVAL:
			return 'approval';
		case AgentSessionStatus.COMPLETED:
			switch (conclusion) {
				case AgentSessionConclusion.SUCCESS:
					return 'success';
				case AgentSessionConclusion.FAILURE:
					return 'failure';
				case AgentSessionConclusion.CANCELLED:
					return 'cancelled';
				case AgentSessionConclusion.TIMED_OUT:
					return 'timed_out';
				case AgentSessionConclusion.REJECTED:
					return 'rejected';
				case AgentSessionConclusion.BUDGET_EXCEEDED:
					return 'budget';
				default:
					return 'unknown';
			}
		default:
			return 'unknown';
	}
}

export function isSessionActive(status: AgentSessionStatus): boolean {
	return (
		status === AgentSessionStatus.QUEUED ||
		status === AgentSessionStatus.RUNNING ||
		status === AgentSessionStatus.WAITING_APPROVAL
	);
}

export const kindLabels: Record<number, string> = {
	[AgentKind.REVIEW]: 'Reviewer',
	[AgentKind.CODE]: 'Coder',
	[AgentKind.CHAT]: 'Assistant',
	[AgentKind.TRIAGE]: 'Triage',
	[AgentKind.CUSTOM]: 'Custom'
};

export const kindDescriptions: Record<number, string> = {
	[AgentKind.REVIEW]: 'Reviews pull requests with inline comments',
	[AgentKind.CODE]: 'Implements issues on a branch and opens pull requests',
	[AgentKind.CHAT]: 'Answers questions in issue and pull request threads',
	[AgentKind.TRIAGE]: 'Labels, assigns, and deduplicates issues',
	[AgentKind.CUSTOM]: 'Behavior comes entirely from the instructions'
};

export const kindIcons: Record<number, Component<{ class?: string }>> = {
	[AgentKind.REVIEW]: SearchCheck,
	[AgentKind.CODE]: Code,
	[AgentKind.CHAT]: MessageSquare,
	[AgentKind.TRIAGE]: Tags,
	[AgentKind.CUSTOM]: Wrench
};

export type TriggerFlag = keyof Pick<
	AgentTriggers,
	| 'pullRequestOpened'
	| 'pullRequestSynchronize'
	| 'pullRequestReadyForReview'
	| 'reviewRequested'
	| 'issueAssigned'
	| 'mention'
	| 'reviewFeedback'
	| 'workflowFailure'
	| 'includeDrafts'
	| 'respondToBots'
>;

export const triggerFlags: TriggerFlag[] = [
	'pullRequestOpened',
	'pullRequestSynchronize',
	'pullRequestReadyForReview',
	'reviewRequested',
	'issueAssigned',
	'mention',
	'reviewFeedback',
	'workflowFailure',
	'includeDrafts',
	'respondToBots'
];

// Triggers switched on when a kind is picked
export const kindTriggers: Record<number, TriggerFlag[]> = {
	[AgentKind.REVIEW]: [
		'pullRequestOpened',
		'pullRequestReadyForReview',
		'reviewRequested',
		'mention'
	],
	[AgentKind.CODE]: ['issueAssigned', 'mention', 'reviewFeedback', 'workflowFailure'],
	[AgentKind.CHAT]: ['mention'],
	[AgentKind.TRIAGE]: ['issueAssigned', 'mention'],
	[AgentKind.CUSTOM]: ['mention']
};

// Kinds that commit to a branch and open pull requests
export function opensPullRequests(kind: AgentKind): boolean {
	return kind === AgentKind.CODE || kind === AgentKind.CUSTOM;
}

export const kindOptions = [
	AgentKind.REVIEW,
	AgentKind.CODE,
	AgentKind.CHAT,
	AgentKind.TRIAGE,
	AgentKind.CUSTOM
];

export const approvalLabels: Record<number, string> = {
	[AgentApprovalPolicy.NONE]: 'Never pause',
	[AgentApprovalPolicy.COMMITS]: 'Pause before commits and commands',
	[AgentApprovalPolicy.ALL_WRITES]: 'Pause before every write'
};

export const approvalDescriptions: Record<number, string> = {
	[AgentApprovalPolicy.NONE]: 'Every action runs without review',
	[AgentApprovalPolicy.COMMITS]: 'Commits, pull requests, and commands wait, comments post freely',
	[AgentApprovalPolicy.ALL_WRITES]: 'Comments and reviews wait too'
};

export const approvalOptions = [
	AgentApprovalPolicy.NONE,
	AgentApprovalPolicy.COMMITS,
	AgentApprovalPolicy.ALL_WRITES
];

export interface ProviderKindInfo {
	label: string;
	baseUrl: string;
	help: string;
	model: string;
	needsKey: boolean;
	apiVersion?: boolean;
}

export const providerKinds: Record<number, ProviderKindInfo> = {
	[AgentProviderKind.OPENAI]: {
		label: 'OpenAI compatible',
		baseUrl: 'https://api.openai.com/v1',
		help: 'Chat completions, also vLLM, LiteLLM, OpenRouter, Groq',
		model: 'gpt-4.1',
		needsKey: false
	},
	[AgentProviderKind.ANTHROPIC]: {
		label: 'Anthropic',
		baseUrl: 'https://api.anthropic.com',
		help: 'Messages API',
		model: 'claude-sonnet-5-5',
		needsKey: true
	},
	[AgentProviderKind.AZURE_OPENAI]: {
		label: 'Azure OpenAI',
		baseUrl: 'https://your-resource.openai.azure.com',
		help: 'Deployment name as the model',
		model: 'my-deployment',
		needsKey: true,
		apiVersion: true
	},
	[AgentProviderKind.OLLAMA]: {
		label: 'Ollama',
		baseUrl: 'http://localhost:11434',
		help: 'Local daemon, lists its own models',
		model: 'qwen2.5-coder:32b',
		needsKey: false
	},
	[AgentProviderKind.GEMINI]: {
		label: 'Google Gemini',
		baseUrl: 'https://generativelanguage.googleapis.com/v1beta/openai',
		help: 'OpenAI compatible endpoint',
		model: 'gemini-2.5-pro',
		needsKey: true
	},
	[AgentProviderKind.BEDROCK]: {
		label: 'Amazon Bedrock',
		baseUrl: 'https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1',
		help: 'OpenAI compatible endpoint, region in the url',
		model: 'model id',
		needsKey: true
	}
};

export const providerKindOptions = [
	AgentProviderKind.OPENAI,
	AgentProviderKind.OLLAMA,
	AgentProviderKind.ANTHROPIC,
	AgentProviderKind.AZURE_OPENAI,
	AgentProviderKind.GEMINI,
	AgentProviderKind.BEDROCK
];

export const triggerLabels: Record<string, string> = {
	manual: 'Manual',
	pull_request: 'Pull request',
	issue: 'Assigned',
	label: 'Label',
	mention: 'Mention',
	review: 'Review feedback',
	workflow_run: 'Failed workflow',
	continue: 'Follow up',
	rerun: 'Rerun'
};

export function triggerLabel(t: string): string {
	return triggerLabels[t] ?? (t ? t.replace(/_/g, ' ') : 'Unknown');
}

export interface ToolInfo {
	name: string;
	description: string;
	write?: boolean;
	commit?: boolean;
}

// Mirrors the server registry, grouped for the agent form
export const toolGroups: { label: string; tools: ToolInfo[] }[] = [
	{
		label: 'Read',
		tools: [
			{ name: 'get_repository_info', description: 'Repository summary and instruction files' },
			{ name: 'list_files', description: 'List a directory' },
			{ name: 'read_file', description: 'Read a file with line numbers' },
			{ name: 'search_code', description: 'Regular expression search' },
			{ name: 'get_diff', description: 'Pull request or session diff' },
			{ name: 'get_pull_request', description: 'Pull request details and threads' },
			{ name: 'get_issue', description: 'Issue details and comments' },
			{ name: 'list_commits', description: 'Recent commits' },
			{ name: 'get_checks', description: 'Statuses and workflow runs' },
			{ name: 'get_workflow_run_log', description: 'Tail of a job log' },
			{ name: 'search_issues', description: 'Search issues and pull requests' }
		]
	},
	{
		label: 'Discuss',
		tools: [
			{ name: 'create_issue', description: 'Open a new issue', write: true },
			{ name: 'post_comment', description: 'Comment on an issue or pull request', write: true },
			{ name: 'submit_review', description: 'Submit a review with inline comments', write: true },
			{ name: 'update_issue', description: 'Labels, assignees, title, state', write: true },
			{ name: 'request_review', description: 'Request reviewers', write: true }
		]
	},
	{
		label: 'Change code',
		tools: [
			{ name: 'write_file', description: 'Create or replace a file' },
			{ name: 'edit_file', description: 'Replace an exact text span' },
			{ name: 'delete_file', description: 'Delete a file' },
			{ name: 'rename_file', description: 'Move a file' },
			{ name: 'commit', description: 'Commit edits to the working branch', commit: true },
			{ name: 'open_pull_request', description: 'Open a pull request', commit: true },
			{ name: 'run_command', description: 'Run a script on a workflow runner', commit: true }
		]
	}
];

export const defaultTools: Record<number, string[]> = {
	[AgentKind.REVIEW]: [
		'get_repository_info',
		'list_files',
		'read_file',
		'search_code',
		'get_diff',
		'get_pull_request',
		'list_commits',
		'get_checks',
		'get_workflow_run_log',
		'post_comment',
		'submit_review'
	],
	[AgentKind.CODE]: [
		'get_repository_info',
		'list_files',
		'read_file',
		'search_code',
		'get_diff',
		'get_pull_request',
		'get_issue',
		'list_commits',
		'get_checks',
		'get_workflow_run_log',
		'search_issues',
		'write_file',
		'edit_file',
		'delete_file',
		'rename_file',
		'commit',
		'open_pull_request',
		'run_command',
		'post_comment',
		'request_review',
		'update_issue',
		'create_issue'
	],
	[AgentKind.CHAT]: [
		'get_repository_info',
		'list_files',
		'read_file',
		'search_code',
		'get_diff',
		'get_pull_request',
		'get_issue',
		'list_commits',
		'get_checks',
		'get_workflow_run_log',
		'search_issues',
		'post_comment'
	],
	[AgentKind.TRIAGE]: [
		'get_repository_info',
		'read_file',
		'search_code',
		'get_issue',
		'search_issues',
		'list_commits',
		'update_issue',
		'create_issue',
		'post_comment'
	],
	[AgentKind.CUSTOM]: [
		'get_repository_info',
		'list_files',
		'read_file',
		'search_code',
		'get_diff',
		'get_pull_request',
		'get_issue',
		'list_commits',
		'search_issues',
		'post_comment',
		'create_issue'
	]
};

export function sessionTitle(s: AgentSession): string {
	if (s.title) return s.title;
	if (s.issueNumber) return `${s.isPull ? '!' : '#'}${s.issueNumber}`;
	return s.prompt.split('\n')[0]?.slice(0, 80) || 'Session';
}

export function parseEventData(raw: string): Record<string, unknown> {
	if (!raw) return {};
	try {
		const v = JSON.parse(raw);
		return v && typeof v === 'object' && !Array.isArray(v) ? v : {};
	} catch {
		return {};
	}
}

// Share of prompt tokens served from cache
export function cacheShare(read: bigint | number, prompt: bigint | number): string {
	const r = Number(read);
	const p = Number(prompt);
	if (r <= 0 || p <= 0) return '';
	return `${Math.round((100 * r) / p)}%`;
}

export function formatTokens(n: bigint | number): string {
	const v = typeof n === 'bigint' ? Number(n) : n;
	if (v >= 1_000_000) return `${(v / 1_000_000).toFixed(1)}M`;
	if (v >= 10_000) return `${Math.round(v / 1000)}k`;
	if (v >= 1000) return `${(v / 1000).toFixed(1)}k`;
	return String(v);
}

// Short argument summary for a tool call row
export function summarizeArgs(tool: string, args: Record<string, unknown>): string {
	const str = (k: string) => (typeof args[k] === 'string' ? (args[k] as string) : '');
	switch (tool) {
		case 'read_file':
		case 'write_file':
		case 'delete_file':
		case 'list_files':
			return str('path') || (tool === 'list_files' ? '/' : '');
		case 'edit_file':
			return str('path');
		case 'rename_file':
			return `${str('from')} -> ${str('to')}`;
		case 'search_code':
			return str('pattern');
		case 'get_diff':
			return str('path') || 'all files';
		case 'commit':
			return str('message').split('\n')[0] ?? '';
		case 'open_pull_request':
		case 'create_issue':
			return str('title');
		case 'run_command':
			return str('command').split('\n')[0] ?? '';
		case 'submit_review':
			return str('event');
		case 'post_comment':
			return (str('body').split('\n')[0] ?? '').slice(0, 80);
		case 'get_workflow_run_log':
			return str('run_id');
		case 'search_issues':
			return str('query');
		default: {
			const keys = Object.keys(args);
			return keys.length ? keys.map((k) => `${k}=${JSON.stringify(args[k])}`).join(' ') : '';
		}
	}
}

export const slugPattern = /^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$/;

export function slugify(name: string): string {
	return name
		.toLowerCase()
		.replace(/[^a-z0-9]+/g, '-')
		.replace(/-{2,}/g, '-')
		.replace(/^-+|-+$/g, '')
		.slice(0, 40);
}

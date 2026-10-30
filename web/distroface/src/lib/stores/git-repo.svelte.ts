import { rpcClient, silentCallOptions, backgroundCallOptions } from '$lib/api/rpc-client';
import { ConnectError } from '@connectrpc/connect';
import type { GitRepository } from '$lib/proto/distroface/v1/git_pb';
import { GitUnit, GitAccessLevel } from '$lib/proto/distroface/v1/git_pb';
import { GitRepoType } from '$lib/proto/distroface/v1/types_pb';

// Repository the project pages are currently showing
class GitRepoStore {
	repo = $state<GitRepository | undefined>();
	loading = $state(false);
	error = $state('');
	notFound = $state(false);
	loadedFor = $state(''); // Wont be the same as repo after rename
	private inflight: { key: string; promise: Promise<GitRepository | undefined> } | null = null;
	private seq = 0;

	// Loads once per namespace and name, force refetches
	async load(namespace: string, name: string, force = false): Promise<GitRepository | undefined> {
		if (!force && this.repo && this.repo.namespace === namespace && this.repo.name === name) {
			return this.repo;
		}
		const key = `${namespace}/${name}`;
		// Callers racing for one address share the request
		if (this.inflight?.key === key) return this.inflight.promise;
		const promise = this.fetch(namespace, name, key);
		this.inflight = { key, promise };
		try {
			return await promise;
		} finally {
			if (this.inflight?.promise === promise) this.inflight = null;
		}
	}

	// Stale answers never overwrite a newer address
	private async fetch(
		namespace: string,
		name: string,
		key: string
	): Promise<GitRepository | undefined> {
		const mine = ++this.seq;
		this.loading = true;
		this.error = '';
		this.notFound = false;
		this.loadedFor = key;
		try {
			const resp = await rpcClient.git.getGitRepository({ namespace, name }, silentCallOptions);
			if (mine !== this.seq) return undefined;
			this.repo = resp.repository;
			return this.repo;
		} catch (err) {
			if (mine !== this.seq) return undefined;
			this.repo = undefined;
			if (err instanceof ConnectError) {
				this.notFound = err.code === 5;
				this.error = err.rawMessage || err.message;
			} else {
				this.error = String(err);
			}
			return undefined;
		} finally {
			if (mine === this.seq) this.loading = false;
		}
	}

	async reload(): Promise<GitRepository | undefined> {
		if (!this.repo) return undefined;
		return this.load(this.repo.namespace, this.repo.name, true);
	}

	// Background refetch, a failure keeps what is shown
	async refresh(): Promise<void> {
		const r = this.repo;
		if (!r) return;
		const key = `${r.namespace}/${r.name}`;
		try {
			const resp = await rpcClient.git.getGitRepository(
				{ namespace: r.namespace, name: r.name },
				backgroundCallOptions
			);
			if (resp.repository && this.loadedFor === key) this.repo = resp.repository;
		} catch {
			// Next refresh retries
		}
	}

	clear() {
		this.repo = undefined;
		this.error = '';
		this.notFound = false;
	}

	get fullName(): string {
		return this.repo ? `${this.repo.namespace}/${this.repo.name}` : '';
	}

	// Caller's level on a unit, none when the repo is unloaded
	level(unit: GitUnit): GitAccessLevel {
		const row = this.repo?.viewer?.units.find((u) => u.unit === unit);
		return row?.level ?? GitAccessLevel.NONE;
	}

	canRead(unit: GitUnit): boolean {
		return this.level(unit) >= GitAccessLevel.READ;
	}

	canWrite(unit: GitUnit): boolean {
		return this.level(unit) >= GitAccessLevel.WRITE;
	}

	get canAdmin(): boolean {
		return this.repo?.viewer?.canAdmin === true;
	}

	get isOwner(): boolean {
		return this.repo?.viewer?.isOwner === true;
	}

	get isMirror(): boolean {
		return this.repo?.type === GitRepoType.MIRROR;
	}

	// Archived or mirror, issues and wiki still take changes
	get readOnly(): boolean {
		return !!this.repo && (this.repo.isArchived || this.isMirror);
	}

	// Why a change is refused, empty when allowed
	get readOnlyReason(): string {
		if (this.repo?.isArchived) return 'This repository is archived and read only.';
		if (this.isMirror) {
			return 'This repository is a mirror and only takes changes from its upstream. Detach it in mirror settings to edit it here.';
		}
		return '';
	}

	// Manual time entries and time open, off unless the repo opts in
	get timeTracking(): boolean {
		return this.repo?.units?.timeTracking === true;
	}

	unitEnabled(unit: GitUnit): boolean {
		const u = this.repo?.units;
		if (!u) return unit === GitUnit.CODE;
		switch (unit) {
			case GitUnit.CODE:
				return true;
			case GitUnit.ISSUES:
				return u.issues && !u.externalIssuesUrl;
			case GitUnit.PULLS:
				return u.pulls;
			case GitUnit.RELEASES:
				return u.releases;
			case GitUnit.WIKI:
				return u.wiki && !u.externalWikiUrl;
			case GitUnit.PROJECTS:
				return u.projects;
			case GitUnit.PACKAGES:
				return u.packages;
			case GitUnit.ACTIONS:
				return u.actions;
			case GitUnit.AGENTS:
				return u.agents;
			default:
				return false;
		}
	}
}

export const gitRepoStore = new GitRepoStore();

import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
import { ConnectError } from '@connectrpc/connect';
import type { GitRepository } from '$lib/proto/distroface/v1/git_pb';
import { GitUnit, GitAccessLevel } from '$lib/proto/distroface/v1/git_pb';

// Repository the project pages are currently showing
class GitRepoStore {
	repo = $state<GitRepository | undefined>();
	loading = $state(false);
	error = $state('');
	notFound = $state(false);

	// Loads once per namespace and name, force refetches
	async load(namespace: string, name: string, force = false): Promise<GitRepository | undefined> {
		if (!force && this.repo && this.repo.namespace === namespace && this.repo.name === name) {
			return this.repo;
		}
		this.loading = true;
		this.error = '';
		this.notFound = false;
		try {
			const resp = await rpcClient.git.getGitRepository({ namespace, name }, silentCallOptions);
			this.repo = resp.repository;
			return this.repo;
		} catch (err) {
			this.repo = undefined;
			if (err instanceof ConnectError) {
				this.notFound = err.code === 5;
				this.error = err.rawMessage || err.message;
			} else {
				this.error = String(err);
			}
			return undefined;
		} finally {
			this.loading = false;
		}
	}

	async reload(): Promise<GitRepository | undefined> {
		if (!this.repo) return undefined;
		return this.load(this.repo.namespace, this.repo.name, true);
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
			default:
				return false;
		}
	}
}

export const gitRepoStore = new GitRepoStore();

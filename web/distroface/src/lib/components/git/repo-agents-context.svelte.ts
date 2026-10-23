import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
import { repoParentScope, type ScopeInit } from '$lib/settings-utils';
import type { GetRepoAgentsResponse } from '$lib/proto/distroface/v1/agents_pb';

export const REPO_AGENTS_CONTEXT_KEY = Symbol('repo-agents-settings');

type RepoRef = { namespace: string; name: string; isOrgNamespace: boolean };

// Agents settings layout loads it once, tabs read it
export class RepoAgentsContext {
	namespace = $state('');
	name = $state('');
	parentScope = $state<ScopeInit | undefined>();
	orgId = $state('');
	overview = $state<GetRepoAgentsResponse | undefined>();

	async load(repo: RepoRef) {
		this.namespace = repo.namespace;
		this.name = repo.name;
		this.parentScope = undefined;
		void this.reload();
		const parent = await repoParentScope(repo.namespace, repo.isOrgNamespace);
		if (this.namespace !== repo.namespace || this.name !== repo.name) return;
		this.orgId = parent.orgId;
		this.parentScope = parent.scope;
	}

	async reload() {
		try {
			this.overview = await rpcClient.agents.getRepoAgents(
				{ namespace: this.namespace, name: this.name },
				silentCallOptions
			);
		} catch {
			this.overview = undefined;
		}
	}
}

import { ArtifactRepoType } from '$lib/proto/distroface/v1/types_pb';
import type { MirrorKind } from '$lib/components/mirror-config-fields.svelte';
import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
import { systemScope } from '$lib/settings-utils';

export type MirrorLimits = {
	defaultIntervalMinutes: number;
	minIntervalMinutes: number;
	maxSyncDepth: number;
};

// Admin clamps applied server side, surfaced in mirror forms
export async function fetchMirrorLimits(): Promise<MirrorLimits | null> {
	try {
		const resp = await rpcClient.settings.getEffectiveSettings(
			{ scope: systemScope },
			silentCallOptions
		);
		const m = resp.settings?.mirror;
		if (!m) return null;
		return {
			defaultIntervalMinutes: m.defaultIntervalMinutes ?? 60,
			minIntervalMinutes: m.minIntervalMinutes ?? 0,
			maxSyncDepth: m.maxSyncDepth ?? 0
		};
	} catch {
		return null;
	}
}

export const artifactRepoTypeOptions = [
	{
		value: ArtifactRepoType.FILE,
		label: 'File repository',
		description: 'Files uploaded by you or CI'
	},
	{
		value: ArtifactRepoType.GITHUB_RELEASES,
		label: 'GitHub releases mirror',
		description: 'Watches a GitHub repo and mirrors its release assets'
	},
	{
		value: ArtifactRepoType.GITLAB_RELEASES,
		label: 'GitLab releases mirror',
		description: 'Watches a GitLab project and mirrors its release assets'
	},
	{
		value: ArtifactRepoType.GITEA_RELEASES,
		label: 'Gitea / Forgejo releases mirror',
		description: 'Watches a Gitea, Forgejo, or Codeberg repo and mirrors its release assets'
	}
];

export function artifactMirrorKind(t: ArtifactRepoType): MirrorKind {
	switch (t) {
		case ArtifactRepoType.GITLAB_RELEASES:
			return 'gitlab';
		case ArtifactRepoType.GITEA_RELEASES:
			return 'gitea';
		default:
			return 'github';
	}
}

export function artifactMirrorLabel(t: ArtifactRepoType): string {
	switch (t) {
		case ArtifactRepoType.GITHUB_RELEASES:
			return 'GitHub';
		case ArtifactRepoType.GITLAB_RELEASES:
			return 'GitLab';
		case ArtifactRepoType.GITEA_RELEASES:
			return 'Gitea';
		default:
			return '';
	}
}

export function isMirrorArtifactType(t: ArtifactRepoType): boolean {
	return t !== ArtifactRepoType.FILE && t !== ArtifactRepoType.UNSPECIFIED;
}

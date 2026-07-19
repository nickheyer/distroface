import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
import {
	SettingsScopeType,
	SettingsTier,
	type FieldProvenance,
	type Settings,
	type SettingsScope
} from '$lib/proto/distroface/v1/settings_pb';
import type { MessageInitShape } from '@bufbuild/protobuf';
import type { SettingsSchema } from '$lib/proto/distroface/v1/settings_pb';

export type SettingsPatch = MessageInitShape<typeof SettingsSchema>;
export type ScopeInit = Pick<SettingsScope, 'type' | 'scopeId'>;

export const systemScope: ScopeInit = { type: SettingsScopeType.SYSTEM, scopeId: '' };

export const orgScope = (orgId: string): ScopeInit => ({
	type: SettingsScopeType.ORG,
	scopeId: orgId
});

export const portalScope = (portalId: string): ScopeInit => ({
	type: SettingsScopeType.PORTAL,
	scopeId: portalId
});

// Tier that supplied one resolved field
export function tierOf(prov: FieldProvenance[], path: string): SettingsTier {
	return prov.find((p) => p.path === path)?.tier ?? SettingsTier.UNSPECIFIED;
}

// Config file pins render as locked controls
export function isLocked(prov: FieldProvenance[], path: string): boolean {
	return tierOf(prov, path) === SettingsTier.FILE;
}

// Masked patch, paths absent from settings are cleared to inherit
export async function patchSettings(
	scope: ScopeInit,
	settings: SettingsPatch,
	paths: string[]
): Promise<{ effective?: Settings; provenance: FieldProvenance[] }> {
	const resp = await rpcClient.settings.updateSettings(
		{ scope, settings, updateMask: { paths } },
		silentCallOptions
	);
	return {
		effective: resp.effective?.settings,
		provenance: resp.effective?.provenance ?? []
	};
}

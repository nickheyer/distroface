<script lang="ts">
	import { create } from '@bufbuild/protobuf';
	import { FieldMaskSchema } from '@bufbuild/protobuf/wkt';
	import { rpc } from '$lib/rpc';
	import {
		SettingsSchema,
		SettingsTier,
		type SettingsScopeType,
		type Settings
	} from '$lib/proto/distroface/v1/settings_pb';
	import { tierLabel } from '$lib/fmt';
	import { errata } from '$lib/state/errata.svelte';
	import type { FieldSpec, GroupSpec } from '$lib/settings-specs';

	let {
		scopeType,
		scopeId = '',
		groups
	}: { scopeType: SettingsScopeType; scopeId?: string; groups: GroupSpec[] } = $props();

	let form = $state<Record<string, string>>({});
	let locked = $state<Set<string>>(new Set());
	let prov = $state<Record<string, SettingsTier>>({});
	let effective = $state<Settings | undefined>();
	let loaded = $state(false);
	let busyGroup = $state('');
	let secretOnFile = $state<Record<string, boolean>>({});

	function camelSegs(path: string): string[] {
		return path.split('.').map((seg) => seg.replace(/_([a-z])/g, (_, c: string) => c.toUpperCase()));
	}

	function getAt(obj: unknown, path: string): unknown {
		let cur = obj;
		for (const seg of camelSegs(path)) {
			if (cur === undefined || cur === null) return undefined;
			cur = (cur as Record<string, unknown>)[seg];
		}
		return cur;
	}

	function toForm(spec: FieldSpec, v: unknown): string {
		if (v === undefined || v === null) return '';
		switch (spec.kind) {
			case 'bool':
				return v ? 'true' : 'false';
			case 'int':
			case 'enum':
				return String(v);
			case 'bytesmb':
				return String(Number(v) / 1e6);
			case 'strlist':
				return (v as string[]).join('\n');
			case 'map':
				return Object.entries(v as Record<string, string>)
					.map(([k, val]) => `${k}=${val}`)
					.join('\n');
			default:
				return String(v);
		}
	}

	function fromForm(spec: FieldSpec, raw: string): unknown {
		const t = raw.trim();
		if (t === '') return undefined;
		switch (spec.kind) {
			case 'bool':
				return t === 'true';
			case 'int':
			case 'enum':
				return Number(t);
			case 'bytesmb':
				return BigInt(Math.round(Number(t) * 1e6));
			case 'strlist':
				return t.split('\n').map((l) => l.trim()).filter(Boolean);
			case 'map': {
				const out: Record<string, string> = {};
				for (const line of t.split('\n')) {
					const i = line.indexOf('=');
					if (i > 0) out[line.slice(0, i).trim()] = line.slice(i + 1).trim();
				}
				return out;
			}
			default:
				return raw;
		}
	}

	function setAt(obj: Record<string, unknown>, path: string, value: unknown) {
		const segs = camelSegs(path);
		let cur = obj;
		for (const seg of segs.slice(0, -1)) {
			cur[seg] = cur[seg] ?? {};
			cur = cur[seg] as Record<string, unknown>;
		}
		cur[segs[segs.length - 1]] = value;
	}

	function fmtEffective(spec: FieldSpec, v: unknown): string {
		if (spec.kind === 'secret') {
			return secretOnFile[spec.path] ? 'on file' : 'not set';
		}
		if (v === undefined || v === null) return 'unset';
		switch (spec.kind) {
			case 'bool':
				return v ? 'yes' : 'no';
			case 'enum':
				return spec.options?.find((o) => o.value === Number(v))?.label ?? String(v);
			case 'bytesmb':
				return `${Number(v) / 1e6} MB`;
			case 'strlist': {
				const arr = v as string[];
				return arr.length ? arr.join(', ') : 'none';
			}
			case 'map': {
				const n = Object.keys(v as object).length;
				return n ? `${n} mapped` : 'none';
			}
			default:
				return String(v) || 'empty';
		}
	}

	async function load() {
		loaded = false;
		try {
			const [stored, eff] = await Promise.all([
				rpc.settings.getSettings({ scope: { type: scopeType, scopeId } }),
				rpc.settings.getEffectiveSettings({ scope: { type: scopeType, scopeId } })
			]);
			const next: Record<string, string> = {};
			const secrets: Record<string, boolean> = {};
			for (const g of groups) {
				for (const f of g.fields) {
					next[f.path] = f.kind === 'secret' ? '' : toForm(f, getAt(stored.settings, f.path));
					if (f.kind === 'secret') {
						secrets[f.path] = Boolean(getAt(eff.settings, f.path + '_set'));
					}
				}
			}
			form = next;
			secretOnFile = secrets;
			locked = new Set(stored.lockedPaths);
			const p: Record<string, SettingsTier> = {};
			for (const entry of eff.provenance) p[entry.path] = entry.tier;
			prov = p;
			effective = eff.settings;
		} finally {
			loaded = true;
		}
	}

	$effect(() => {
		void scopeType;
		void scopeId;
		load();
	});

	async function saveGroup(group: GroupSpec) {
		busyGroup = group.title;
		try {
			const init: Record<string, unknown> = {};
			const paths: string[] = [];
			for (const f of group.fields) {
				if (locked.has(f.path)) continue;
				const value = fromForm(f, form[f.path] ?? '');
				if (f.kind === 'secret' && value === undefined) continue;
				paths.push(f.path);
				if (value !== undefined) setAt(init, f.path, value);
			}
			await rpc.settings.updateSettings({
				scope: { type: scopeType, scopeId },
				settings: create(SettingsSchema, init),
				updateMask: create(FieldMaskSchema, { paths })
			});
			errata.remark(`${group.title} settings saved.`);
			await load();
		} catch {
			// Interceptor reports
		} finally {
			busyGroup = '';
		}
	}

	function tierOf(path: string): string {
		const t = prov[path];
		return t === undefined ? 'default' : tierLabel[t];
	}
</script>

{#if !loaded}
	<p class="working">loading</p>
{:else}
	{#each groups as group (group.title)}
		<form
			class="panel"
			onsubmit={(e) => {
				e.preventDefault();
				saveGroup(group);
			}}
		>
			<p class="panel-title">{group.title}</p>
			{#if group.note}
				<p class="note" style="margin-bottom: 0.9rem">{group.note}</p>
			{/if}

			{#each group.fields as f (f.path)}
				{@const isLocked = locked.has(f.path)}
				<label class="field">
					<span>
						{f.label}
						{#if isLocked}
							<em class="pin">set in the config file</em>
						{/if}
					</span>
					{#if f.kind === 'bool'}
						<select bind:value={form[f.path]} disabled={isLocked} style="max-width: 12rem">
							<option value="">inherit</option>
							<option value="true">yes</option>
							<option value="false">no</option>
						</select>
					{:else if f.kind === 'enum'}
						<select bind:value={form[f.path]} disabled={isLocked} style="max-width: 22rem">
							<option value="">inherit</option>
							{#each f.options ?? [] as o (o.value)}
								<option value={String(o.value)}>{o.label}</option>
							{/each}
						</select>
					{:else if f.kind === 'strlist' || f.kind === 'map'}
						<textarea rows="3" bind:value={form[f.path]} disabled={isLocked}></textarea>
					{:else if f.kind === 'secret'}
						<input
							type="password"
							bind:value={form[f.path]}
							disabled={isLocked}
							placeholder={secretOnFile[f.path] ? 'on file, empty keeps it' : ''}
						/>
					{:else}
						<input type="text" bind:value={form[f.path]} disabled={isLocked} />
					{/if}
					<span class="hint">
						{#if f.hint}{f.hint}&ensp;{/if}
						now {fmtEffective(f, getAt(effective, f.path))} · {tierOf(f.path)}
					</span>
				</label>
			{/each}

			<button class="act" type="submit" disabled={busyGroup === group.title}>
				Save {group.title.toLowerCase()}
			</button>
		</form>
	{/each}
{/if}

<style>
	.pin {
		font-family: var(--serif);
		font-style: italic;
		text-transform: none;
		letter-spacing: 0;
		color: var(--wax);
		margin-left: 0.5rem;
	}
</style>

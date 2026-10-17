<script module lang="ts">
	import { GitUnit } from '$lib/proto/distroface/v1/git_pb';
	import { TeamUnitLevel, type TeamUnitPermission } from '$lib/proto/distroface/v1/organization_pb';

	export type UnitLevels = Record<string, TeamUnitLevel>;

	// Unit keys match the strings the backend stores
	export const UNITS: { key: string; label: string; unit: GitUnit; help: string }[] = [
		{ key: 'code', label: 'Code', unit: GitUnit.CODE, help: 'Clone, push, branches, tags' },
		{ key: 'issues', label: 'Issues', unit: GitUnit.ISSUES, help: 'Issues, labels, milestones' },
		{ key: 'pulls', label: 'Pull requests', unit: GitUnit.PULLS, help: 'Reviews and merges' },
		{ key: 'releases', label: 'Releases', unit: GitUnit.RELEASES, help: 'Releases and assets' },
		{ key: 'wiki', label: 'Wiki', unit: GitUnit.WIKI, help: 'Wiki pages' },
		{ key: 'projects', label: 'Projects', unit: GitUnit.PROJECTS, help: 'Kanban boards' },
		{ key: 'packages', label: 'Packages', unit: GitUnit.PACKAGES, help: 'Linked image and artifact repos' },
		{ key: 'actions', label: 'Actions', unit: GitUnit.ACTIONS, help: 'Workflows, runs, secrets' }
	];

	export const LEVELS: { value: TeamUnitLevel; label: string }[] = [
		{ value: TeamUnitLevel.NONE, label: 'None' },
		{ value: TeamUnitLevel.READ, label: 'Read' },
		{ value: TeamUnitLevel.WRITE, label: 'Write' }
	];

	export function defaultUnitLevels(level: TeamUnitLevel = TeamUnitLevel.READ): UnitLevels {
		const out: UnitLevels = {};
		for (const u of UNITS) out[u.key] = level;
		return out;
	}

	export function toUnitLevels(units: TeamUnitPermission[], fallback = TeamUnitLevel.NONE): UnitLevels {
		const out = defaultUnitLevels(fallback);
		for (const p of units) {
			if (p.unit in out && p.level !== TeamUnitLevel.UNSPECIFIED) out[p.unit] = p.level;
		}
		return out;
	}

	export function fromUnitLevels(levels: UnitLevels): { unit: string; level: TeamUnitLevel }[] {
		return UNITS.map((u) => ({ unit: u.key, level: levels[u.key] ?? TeamUnitLevel.NONE }));
	}

	export function unitLevelLabel(level: TeamUnitLevel): string {
		return LEVELS.find((l) => l.value === level)?.label ?? 'None';
	}
</script>

<script lang="ts">
	import * as RadioGroup from '$lib/components/ui/radio-group';
	import { cn } from '$lib/utils';

	let {
		value = $bindable(defaultUnitLevels()),
		disabled = false,
		idPrefix = 'unit',
		class: className
	}: {
		value: UnitLevels;
		disabled?: boolean;
		idPrefix?: string;
		class?: string;
	} = $props();

	function set(key: string, v: string) {
		value = { ...value, [key]: Number(v) as TeamUnitLevel };
	}

	function setAll(level: TeamUnitLevel) {
		value = defaultUnitLevels(level);
	}
</script>

<div class={cn('rounded-lg border border-border/60 overflow-x-auto', className)}>
	<div class="min-w-88">
		<div class="grid grid-cols-[1fr_repeat(3,4.5rem)] items-center bg-muted/30 px-4 py-2 text-xs font-medium text-muted-foreground">
			<span>Unit</span>
			{#each LEVELS as l (l.value)}
				<button
					type="button"
					class="text-center hover:text-foreground transition-colors disabled:pointer-events-none"
					title="Set every unit to {l.label.toLowerCase()}"
					{disabled}
					onclick={() => setAll(l.value)}
				>
					{l.label}
				</button>
			{/each}
		</div>
		<div class="divide-y divide-border/40">
			{#each UNITS as u (u.key)}
				<RadioGroup.Root
					value={String(value[u.key] ?? TeamUnitLevel.NONE)}
					onValueChange={(v) => set(u.key, v)}
					{disabled}
					class="grid grid-cols-[1fr_repeat(3,4.5rem)] items-center gap-0 px-4 py-2.5"
				>
					<div class="min-w-0">
						<p class="text-sm font-medium leading-none">{u.label}</p>
						<p class="text-[12px] text-muted-foreground mt-1">{u.help}</p>
					</div>
					{#each LEVELS as l (l.value)}
						<div class="flex justify-center">
							<RadioGroup.Item
								value={String(l.value)}
								id="{idPrefix}-{u.key}-{l.value}"
								aria-label="{u.label} {l.label}"
							/>
						</div>
					{/each}
				</RadioGroup.Root>
			{/each}
		</div>
	</div>
</div>

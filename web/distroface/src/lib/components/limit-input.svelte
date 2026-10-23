<script lang="ts">
	import { tick, untrack } from 'svelte';
	import { Input } from '$lib/components/ui/input';
	import { Switch } from '$lib/components/ui/switch';
	import UnitInput from '$lib/components/unit-input.svelte';
	import DurationInput, {
		isDurationUnit,
		type DurationUnit
	} from '$lib/components/duration-input.svelte';
	import { cn } from '$lib/utils';
	import type { HTMLInputAttributes } from 'svelte/elements';

	type InputEvent<E> = E & { currentTarget: EventTarget & HTMLInputElement };

	// Off binds undefined, callers store that as zero
	let {
		value = $bindable(),
		unit = '',
		unset = 'Unlimited',
		seed,
		allowZero = false,
		min,
		max,
		step,
		interval = false,
		id,
		label,
		disabled = false,
		class: className,
		inputClass,
		onblur,
		onkeydown,
		onToggle
	}: {
		value?: number | null;
		unit?: string;
		// Placeholder while off
		unset?: string;
		// First value after switching on
		seed?: number;
		allowZero?: boolean;
		min?: number;
		max?: number;
		step?: number | string;
		interval?: boolean;
		id?: string;
		label?: string;
		disabled?: boolean;
		class?: string;
		inputClass?: string;
		onblur?: HTMLInputAttributes['onblur'];
		onkeydown?: HTMLInputAttributes['onkeydown'];
		onToggle?: (on: boolean) => void;
	} = $props();

	const duration = $derived(isDurationUnit(unit));
	const floor = $derived(min ?? (allowZero ? 0 : 1));

	let on = $state(false);
	let inner = $state<number | null | undefined>(undefined);
	let emitted: number | null | undefined = Number.NaN;

	function num(v: unknown): number | undefined {
		if (v === undefined || v === null || v === '') return undefined;
		const n = Number(v);
		return Number.isFinite(n) ? n : undefined;
	}

	function active(n: number | undefined): boolean {
		return n !== undefined && (allowZero || n > 0);
	}

	function emit(v: number | undefined) {
		emitted = v;
		value = v;
	}

	// Outside changes reset the switch, own edits keep it
	$effect(() => {
		const v = value;
		if (v === emitted) return;
		emitted = v;
		untrack(() => {
			const n = num(v);
			on = active(n);
			inner = on ? n : undefined;
		});
	});

	$effect(() => {
		const n = num(inner);
		const o = on;
		untrack(() => {
			if (!o) return;
			const next = active(n) ? n : undefined;
			if (next !== value) emit(next);
		});
	});

	async function toggle(next: boolean) {
		on = next;
		if (next) {
			if (!active(num(inner))) inner = seed ?? floor;
			await tick();
			if (id) document.getElementById(id)?.focus();
		} else {
			emit(undefined);
		}
		onToggle?.(next);
	}

	function blur(e: InputEvent<FocusEvent>) {
		if (on && !active(num(inner))) void toggle(false);
		onblur?.(e);
	}
</script>

<div class={cn('flex items-center gap-2.5', className)}>
	<Switch
		checked={on}
		onCheckedChange={(v) => void toggle(v)}
		{disabled}
		aria-label={label ? `Limit ${label}` : 'Set a limit'}
		aria-controls={id}
	/>
	{#if duration}
		<DurationInput
			{id}
			unit={unit as DurationUnit}
			{interval}
			bind:value={inner}
			min={floor}
			{max}
			placeholder={unset}
			disabled={disabled || !on}
			class={inputClass}
			onblur={blur}
			{onkeydown}
		/>
	{:else if unit}
		<UnitInput
			{id}
			{unit}
			bind:value={inner}
			min={floor}
			{max}
			{step}
			placeholder={unset}
			disabled={disabled || !on}
			class={cn('w-32', inputClass)}
			onblur={blur}
			{onkeydown}
		/>
	{:else}
		<Input
			{id}
			type="number"
			bind:value={inner}
			min={floor}
			{max}
			{step}
			placeholder={unset}
			disabled={disabled || !on}
			class={cn('w-24 tabular-nums', inputClass)}
			onblur={blur}
			{onkeydown}
		/>
	{/if}
</div>

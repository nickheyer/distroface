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
		fallback,
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
		// Value in force while off, shown disabled and edited from
		fallback?: number;
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

	// Label targets the switch, the field gets its own id
	const inputId = $derived(id ? `${id}-value` : undefined);

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
			inner = on ? n : fallback;
		});
	});

	// Off shows whatever is in force, it arrives after the first paint
	$effect(() => {
		const f = fallback;
		untrack(() => {
			if (!on) inner = f;
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
			if (inputId) document.getElementById(inputId)?.focus();
		} else {
			emit(undefined);
			inner = fallback;
		}
		onToggle?.(next);
	}

	function blur(e: InputEvent<FocusEvent>) {
		if (on && !active(num(inner))) void toggle(false);
		onblur?.(e);
	}
</script>

<div data-slot="limit-input" class={cn('flex items-center gap-2.5', className)}>
	<Switch
		{id}
		checked={on}
		onCheckedChange={(v) => void toggle(v)}
		{disabled}
		aria-label={label ? `Limit ${label}` : 'Set a limit'}
		aria-controls={inputId}
	/>
	{#if duration}
		<DurationInput
			id={inputId}
			aria-label={label}
			unit={unit as DurationUnit}
			{interval}
			bind:value={inner}
			min={floor}
			{max}
			disabled={disabled || !on}
			class={inputClass}
			onblur={blur}
			{onkeydown}
		/>
	{:else if unit}
		<UnitInput
			id={inputId}
			aria-label={label}
			{unit}
			bind:value={inner}
			min={floor}
			{max}
			{step}
			disabled={disabled || !on}
			class={cn('w-44', inputClass)}
			onblur={blur}
			{onkeydown}
		/>
	{:else}
		<Input
			id={inputId}
			aria-label={label}
			type="number"
			bind:value={inner}
			min={floor}
			{max}
			{step}
			disabled={disabled || !on}
			class={cn('w-44 tabular-nums', inputClass)}
			onblur={blur}
			{onkeydown}
		/>
	{/if}
</div>

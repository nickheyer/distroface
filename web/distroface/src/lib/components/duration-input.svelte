<script lang="ts" module>
	export type DurationUnit = 'ms' | 'seconds' | 'minutes' | 'hours' | 'days' | 'weeks';

	// Milliseconds per unit
	const FACTOR: Record<DurationUnit, number> = {
		ms: 1,
		seconds: 1_000,
		minutes: 60_000,
		hours: 3_600_000,
		days: 86_400_000,
		weeks: 604_800_000
	};
	const ORDER: DurationUnit[] = ['ms', 'seconds', 'minutes', 'hours', 'days', 'weeks'];
	const LABELS: Record<DurationUnit, [string, string]> = {
		ms: ['ms', 'ms'],
		seconds: ['second', 'seconds'],
		minutes: ['minute', 'minutes'],
		hours: ['hour', 'hours'],
		days: ['day', 'days'],
		weeks: ['week', 'weeks']
	};

	export function isDurationUnit(unit: string): unit is DurationUnit {
		return (ORDER as string[]).includes(unit);
	}

	// Largest unit the value divides into evenly
	export function fitUnit(
		value: number,
		unit: DurationUnit,
		choices: DurationUnit[]
	): DurationUnit {
		if (!value) return unit;
		const ms = value * FACTOR[unit];
		let best = unit;
		for (const c of choices) {
			if (FACTOR[c] > FACTOR[best] && ms % FACTOR[c] === 0) best = c;
		}
		return best;
	}
</script>

<script lang="ts">
	import { untrack } from 'svelte';
	import { Select as SelectPrimitive } from 'bits-ui';
	import { SelectContent, SelectItem } from '$lib/components/ui/select';
	import { ChevronDown } from '$lib/icons';
	import { cn } from '$lib/utils';
	import type { HTMLInputAttributes } from 'svelte/elements';

	type InputEvent<E> = E & { currentTarget: EventTarget & HTMLInputElement };

	let {
		value = $bindable(),
		unit,
		min,
		max,
		interval = false,
		id,
		disabled = false,
		placeholder,
		class: className,
		onblur,
		onkeydown,
		...restProps
	}: {
		// Stored in the given unit, the menu only changes how it reads
		value?: number | null;
		unit: DurationUnit;
		min?: number;
		max?: number;
		// Reads as a recurrence
		interval?: boolean;
		id?: string;
		disabled?: boolean;
		placeholder?: string;
		class?: string;
		onblur?: HTMLInputAttributes['onblur'];
		onkeydown?: HTMLInputAttributes['onkeydown'];
	} & Omit<
		HTMLInputAttributes,
		| 'type'
		| 'value'
		| 'class'
		| 'files'
		| 'min'
		| 'max'
		| 'id'
		| 'disabled'
		| 'placeholder'
		| 'onblur'
		| 'onkeydown'
		| 'oninput'
	> = $props();

	// Units from the stored one upward that still fit under max
	const choices = $derived.by(() => {
		const start = ORDER.indexOf(unit);
		return ORDER.filter(
			(u, i) => i >= start && (max === undefined || max * FACTOR[unit] >= FACTOR[u])
		);
	});

	let displayUnit = $state<DurationUnit>(untrack(() => unit));
	let text = $state('');
	let emitted: number | null | undefined;
	let primed = false;

	function render(v: number | null | undefined, u: DurationUnit): string {
		if (v === null || v === undefined || Number.isNaN(Number(v))) return '';
		return String(Math.round((Number(v) * FACTOR[unit] * 100) / FACTOR[u]) / 100);
	}

	// Outside changes refit the unit, own edits keep it
	$effect(() => {
		const v = value;
		const c = choices;
		if (primed && v === emitted) return;
		primed = true;
		emitted = v;
		untrack(() => {
			displayUnit = fitUnit(Number(v) || 0, unit, c);
			text = render(v, displayUnit);
		});
	});

	function toStored(n: number): number {
		return Math.round((n * FACTOR[displayUnit]) / FACTOR[unit]);
	}

	function clampStored(n: number): number {
		if (min !== undefined) n = Math.max(min, n);
		if (max !== undefined) n = Math.min(max, n);
		return n;
	}

	function emit(v: number | undefined) {
		emitted = v;
		value = v;
	}

	function input(raw: string) {
		text = raw;
		const t = raw.trim();
		if (t === '') {
			emit(undefined);
			return;
		}
		const n = Number(t);
		if (Number.isFinite(n)) emit(toStored(n));
	}

	function blur(e: InputEvent<FocusEvent>) {
		if (value !== null && value !== undefined) {
			const v = clampStored(Number(value) || 0);
			if (v !== value) emit(v);
			text = render(v, displayUnit);
		}
		onblur?.(e);
	}

	function keydown(e: InputEvent<KeyboardEvent>) {
		if (e.key === 'ArrowUp' || e.key === 'ArrowDown') {
			e.preventDefault();
			const step = e.key === 'ArrowUp' ? 1 : -1;
			const v = clampStored(toStored((Number(text) || 0) + step));
			emit(v);
			text = render(v, displayUnit);
		}
		onkeydown?.(e);
	}

	function changeUnit(u: string) {
		if (!isDurationUnit(u)) return;
		displayUnit = u;
		text = render(value, u);
	}

	const unitLabel = $derived(LABELS[displayUnit][Math.abs(Number(text)) === 1 ? 0 : 1]);
</script>

<div
	data-slot="duration-input"
	data-invalid={restProps['aria-invalid'] ? '' : undefined}
	class={cn(
		'flex h-8 items-stretch overflow-hidden rounded-md border border-input bg-field text-foreground shadow-[inset_0_1px_1px_0_rgb(0_0_0/0.04)] transition-[border-color,box-shadow] duration-150 focus-within:border-ring focus-within:ring-2 focus-within:ring-ring/30 hover:border-border-strong has-[input:disabled]:cursor-not-allowed has-[input:disabled]:opacity-50 data-invalid:border-destructive data-invalid:ring-2 data-invalid:ring-destructive/25 dark:shadow-[inset_0_1px_1px_0_rgb(0_0_0/0.2)]',
		interval ? 'w-52' : 'w-44',
		className
	)}
>
	{#if interval}
		<span class="flex shrink-0 items-center pl-2.5 text-xs text-muted-foreground select-none"
			>Every</span
		>
	{/if}
	<input
		{id}
		type="text"
		inputmode="decimal"
		autocomplete="off"
		class="min-w-0 flex-1 bg-transparent px-2.5 text-base tabular-nums outline-none placeholder:text-muted-foreground disabled:cursor-not-allowed md:text-sm"
		value={text}
		{placeholder}
		{disabled}
		oninput={(e) => input(e.currentTarget.value)}
		onblur={blur}
		onkeydown={keydown}
		{...restProps}
	/>
	{#if choices.length > 1}
		<SelectPrimitive.Root type="single" value={displayUnit} onValueChange={changeUnit} {disabled}>
			<SelectPrimitive.Trigger
				class="flex shrink-0 cursor-pointer items-center gap-1 border-l border-input/70 px-2 text-xs text-muted-foreground transition-colors outline-none hover:bg-muted hover:text-foreground focus-visible:bg-muted focus-visible:text-foreground disabled:pointer-events-none"
				aria-label="Unit"
			>
				{unitLabel}
				<ChevronDown class="size-3 opacity-70" />
			</SelectPrimitive.Trigger>
			<SelectContent class="min-w-28">
				{#each choices as u (u)}
					<SelectItem value={u} label={LABELS[u][1]} />
				{/each}
			</SelectContent>
		</SelectPrimitive.Root>
	{:else}
		<span class="flex shrink-0 items-center pr-2.5 text-xs text-muted-foreground select-none"
			>{unitLabel}</span
		>
	{/if}
</div>

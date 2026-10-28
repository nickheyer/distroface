<script lang="ts">
	import { Calendar } from '$lib/components/ui/calendar';
	import { Popover, PopoverContent, PopoverTrigger } from '$lib/components/ui/popover';
	import { Button } from '$lib/components/ui/button';
	import { inputClass } from '$lib/components/ui/input/input.svelte';
	import { Calendar as CalendarDays, X } from '$lib/icons';
	import { cn } from '$lib/utils';
	import {
		DateFormatter,
		getLocalTimeZone,
		parseDate,
		type CalendarDate,
		type DateValue
	} from '@internationalized/date';

	let {
		value = $bindable(''),
		id,
		placeholder = 'Pick a date',
		clearable = true,
		minValue,
		maxValue,
		disabled = false,
		invalid = false,
		class: className,
		onValueChange
	}: {
		// ISO calendar date, empty for none
		value?: string;
		id?: string;
		placeholder?: string;
		clearable?: boolean;
		minValue?: string;
		maxValue?: string;
		disabled?: boolean;
		invalid?: boolean;
		class?: string;
		onValueChange?: (value: string) => void;
	} = $props();

	let open = $state(false);
	const formatter = new DateFormatter('en-US', { dateStyle: 'medium' });

	function parse(s?: string): CalendarDate | undefined {
		if (!s) return undefined;
		try {
			return parseDate(s);
		} catch {
			return undefined;
		}
	}

	const selected = $derived(parse(value));
	const label = $derived(selected ? formatter.format(selected.toDate(getLocalTimeZone())) : '');

	function pick(d: DateValue | undefined) {
		const next = d ? d.toString() : '';
		value = next;
		onValueChange?.(next);
		if (d) open = false;
	}
</script>

<div class={cn('relative w-full', className)}>
	<Popover bind:open>
		<PopoverTrigger>
			{#snippet child({ props })}
				<button
					{...props}
					id={id ?? (props.id as string | undefined)}
					type="button"
					{disabled}
					aria-invalid={invalid || undefined}
					class={cn(
						inputClass,
						'cursor-pointer items-center gap-2 text-left',
						clearable && selected && 'pr-8'
					)}
				>
					<CalendarDays class="size-3.5 shrink-0 text-muted-foreground" />
					{#if selected}
						<span class="truncate tabular-nums">{label}</span>
					{:else}
						<span class="truncate text-muted-foreground">{placeholder}</span>
					{/if}
				</button>
			{/snippet}
		</PopoverTrigger>
		<PopoverContent class="w-auto p-0" align="start">
			<Calendar
				type="single"
				value={selected}
				onValueChange={pick}
				minValue={parse(minValue)}
				maxValue={parse(maxValue)}
				captionLayout="dropdown"
				initialFocus
			/>
		</PopoverContent>
	</Popover>
	{#if clearable && selected && !disabled}
		<Button
			variant="ghost-muted"
			size="icon-xs"
			class="absolute top-1 right-1"
			aria-label="Clear date"
			onclick={() => pick(undefined)}
		>
			<X />
		</Button>
	{/if}
</div>

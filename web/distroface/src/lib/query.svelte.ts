import { MatchKind } from '$lib/proto/distroface/v1/pagination_pb';

// One filterable field a table offers
export interface QueryField {
	key: string;
	label: string;
}

export interface ActiveFilter {
	field: string;
	match: MatchKind;
	value: string;
}

// Query message init for a PageRequest
export interface QueryRequest {
	text: string;
	filters: ActiveFilter[];
}

// Structured query state, the filter twin of Pager
export class QueryFilter {
	readonly fields: QueryField[];
	text = $state('');
	filters = $state<ActiveFilter[]>([]);

	constructor(fields: QueryField[] = []) {
		this.fields = fields;
	}

	get active(): boolean {
		return this.text.trim() !== '' || this.filters.length > 0;
	}

	request(): QueryRequest {
		return {
			text: this.text.trim(),
			filters: this.filters.map((f) => ({ ...f, value: f.value.trim() }))
		};
	}

	label(key: string): string {
		return this.fields.find((f) => f.key === key)?.label ?? key;
	}

	add(field: string, match: MatchKind, value: string): boolean {
		if (!field || !value.trim()) return false;
		this.filters = [...this.filters, { field, match, value: value.trim() }];
		return true;
	}

	remove(index: number) {
		this.filters = this.filters.filter((_, i) => i !== index);
	}

	reset() {
		this.text = '';
		this.filters = [];
	}
}

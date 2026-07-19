import type { PageInfo } from '$lib/proto/distroface/v1/pagination_pb';
import type { QueryRequest } from '$lib/query.svelte';

// Token stack pager, tokens[i] is the opaque cursor for page i+1
export class Pager {
	pageSize: number;
	page = $state(1);
	totalCount = $state(0);
	#tokens: string[] = [''];
	#nextToken = $state('');

	constructor(pageSize = 20) {
		this.pageSize = pageSize;
	}

	// PageRequest fields for the current page
	request(query?: QueryRequest, orderBy = ''): { pageSize: number; pageToken: string; query?: QueryRequest; orderBy: string } {
		return {
			pageSize: this.pageSize,
			pageToken: this.#tokens[this.page - 1] ?? '',
			query,
			orderBy
		};
	}

	// Records the response cursor and total
	apply(info?: PageInfo) {
		this.totalCount = Number(info?.totalCount ?? 0n);
		this.#nextToken = info?.nextPageToken ?? '';
	}

	get hasNext(): boolean {
		return this.#nextToken !== '';
	}

	get hasPrev(): boolean {
		return this.page > 1;
	}

	// Advances a page, false when already on the last page
	next(): boolean {
		if (!this.#nextToken) return false;
		this.#tokens[this.page] = this.#nextToken;
		this.page += 1;
		return true;
	}

	// Steps back a page, false when already on the first page
	prev(): boolean {
		if (this.page <= 1) return false;
		this.page -= 1;
		return true;
	}

	// Back to page one, forgetting all cursors
	reset() {
		this.page = 1;
		this.totalCount = 0;
		this.#tokens = [''];
		this.#nextToken = '';
	}
}

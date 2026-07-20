import type { PageInfo } from '$lib/proto/distroface/v1/pagination_pb';
import type { QueryRequest } from '$lib/query.svelte';

// Offset pager, tokens mirror the server's base64 offset cursors
export class Pager {
	pageSize = $state(20);
	page = $state(1);
	totalCount = $state(0);
	#nextToken = $state('');

	constructor(pageSize = 20) {
		this.pageSize = pageSize;
	}

	get totalPages(): number {
		return Math.max(1, Math.ceil(this.totalCount / this.pageSize));
	}

	// PageRequest fields for the current page
	request(query?: QueryRequest, orderBy = ''): { pageSize: number; pageToken: string; query?: QueryRequest; orderBy: string } {
		const offset = (this.page - 1) * this.pageSize;
		return {
			pageSize: this.pageSize,
			pageToken: offset > 0 ? btoa(String(offset)) : '',
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
		if (this.page >= this.totalPages && !this.#nextToken) return false;
		this.page += 1;
		return true;
	}

	// Steps back a page, false when already on the first page
	prev(): boolean {
		if (this.page <= 1) return false;
		this.page -= 1;
		return true;
	}

	// Jumps to a page, false when clamped to the current one
	goTo(target: number): boolean {
		const clamped = Math.min(Math.max(1, Math.trunc(target) || 1), this.totalPages);
		if (clamped === this.page) return false;
		this.page = clamped;
		return true;
	}

	// Resizes pages keeping the first visible row in view
	setPageSize(size: number): boolean {
		if (size < 1 || size === this.pageSize) return false;
		const firstRow = (this.page - 1) * this.pageSize;
		this.pageSize = size;
		this.page = Math.floor(firstRow / size) + 1;
		return true;
	}

	// Back to page one, forgetting the response cursor
	reset() {
		this.page = 1;
		this.totalCount = 0;
		this.#nextToken = '';
	}
}

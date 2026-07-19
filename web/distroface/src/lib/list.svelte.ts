import { create } from '@bufbuild/protobuf';
import {
	PageRequestSchema,
	type FieldFilter,
	type PageInfo,
	type PageRequest
} from '$lib/proto/distroface/v1/pagination_pb';

export interface Fetched<T> {
	rows: T[];
	page?: PageInfo;
}

// Cursor pager over one list rpc, tracks visited tokens for backing up
export class Lister<T> {
	rows = $state<T[]>([]);
	total = $state(0);
	busy = $state(false);
	loaded = $state(false);
	text = $state('');
	filters: Pick<FieldFilter, 'field' | 'match' | 'value'>[] = [];
	orderBy: string;
	pageSize: number;

	private nextTok = $state('');
	private hist: string[] = [''];
	private pos = $state(0);

	hasNext = $derived(this.nextTok !== '');
	hasBack = $derived(this.pos > 0);

	private fetcher: (page: PageRequest) => Promise<Fetched<T>>;

	constructor(
		fetcher: (page: PageRequest) => Promise<Fetched<T>>,
		opts: { pageSize?: number; orderBy?: string } = {}
	) {
		this.fetcher = fetcher;
		this.pageSize = opts.pageSize ?? 50;
		this.orderBy = opts.orderBy ?? '';
	}

	async fetch() {
		this.busy = true;
		try {
			const page = create(PageRequestSchema, {
				pageSize: this.pageSize,
				pageToken: this.hist[this.pos],
				orderBy: this.orderBy,
				query: { text: this.text, filters: this.filters }
			});
			const r = await this.fetcher(page);
			this.rows = r.rows;
			this.nextTok = r.page?.nextPageToken ?? '';
			this.total = Number(r.page?.totalCount ?? 0n);
			this.loaded = true;
		} finally {
			this.busy = false;
		}
	}

	async first() {
		this.hist = [''];
		this.pos = 0;
		await this.fetch();
	}

	async forward() {
		if (!this.nextTok) return;
		this.hist = [...this.hist.slice(0, this.pos + 1), this.nextTok];
		this.pos += 1;
		await this.fetch();
	}

	async back() {
		if (this.pos === 0) return;
		this.pos -= 1;
		await this.fetch();
	}
}

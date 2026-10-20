<script lang="ts">
	import { tick, type Component } from 'svelte';
	import { CircleDot, CheckCircle, GitPullRequest, GitMerge, Users, Loader } from '$lib/icons';
	import { Avatar, AvatarFallback } from '$lib/components/ui/avatar';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { issueRef } from '$lib/utils';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { IssueState, type Issue } from '$lib/proto/distroface/v1/issue_pb';
	import { SvelteSet } from 'svelte/reactivity';

	let {
		textarea,
		value = $bindable(''),
		namespace,
		name
	}: {
		textarea: HTMLTextAreaElement | null | undefined;
		value: string;
		// Repository that #refs resolve against, the current one when omitted
		namespace?: string;
		name?: string;
	} = $props();

	type Item = {
		key: string;
		insert: string;
		title: string;
		detail?: string;
		icon?: Component<{ class?: string }>;
		tone?: string;
		initials?: string;
	};

	const LIMIT = 8;
	const WIDTH = 288;
	const ROW = 32;

	const ns = $derived(namespace ?? gitRepoStore.repo?.namespace ?? '');
	const nm = $derived(name ?? gitRepoStore.repo?.name ?? '');
	const orgId = $derived(gitRepoStore.repo?.isOrgNamespace ? gitRepoStore.repo.ownerId : '');

	let trigger = $state<'@' | '#' | '!' | null>(null);
	let query = $state('');
	let start = 0;
	let items = $state<Item[]>([]);
	let active = $state(0);
	let loading = $state(false);
	let caret = $state({ top: 0, bottom: 0, left: 0 });
	let seq = 0;
	let debounce: ReturnType<typeof setTimeout> | undefined;

	const open = $derived(trigger !== null && (items.length > 0 || loading));

	const style = $derived.by(() => {
		const height = Math.max(1, items.length) * ROW + 8;
		const below = caret.bottom + 4;
		const top =
			below + height > window.innerHeight - 8 ? Math.max(8, caret.top - 4 - height) : below;
		const left = Math.max(8, Math.min(caret.left, window.innerWidth - WIDTH - 8));
		return `top:${top}px;left:${left}px;width:${WIDTH}px`;
	});

	function close() {
		clearTimeout(debounce);
		seq++;
		trigger = null;
		query = '';
		items = [];
		loading = false;
	}

	// Token under the caret, trigger char through the caret
	function detect() {
		const el = textarea;
		if (!el || el.selectionStart !== el.selectionEnd) return close();
		const end = el.selectionStart;
		const text = el.value;
		let i = end - 1;
		while (i >= 0 && !/\s/.test(text[i])) i--;
		const tokenStart = i + 1;
		const token = text.slice(tokenStart, end);
		const ch = token[0];
		if (ch !== '@' && ch !== '#' && ch !== '!') return close();
		if (tokenStart > 0 && !/[\s(]/.test(text[tokenStart - 1])) return close();
		const q = token.slice(1);
		if (!/^[\w./-]*$/.test(q)) return close();
		const fresh = trigger !== ch || start !== tokenStart;
		trigger = ch;
		start = tokenStart;
		query = q;
		if (fresh) active = 0;
		position(tokenStart);
		clearTimeout(debounce);
		debounce = setTimeout(() => void search(), fresh ? 0 : 150);
	}

	function onKeyup(e: KeyboardEvent) {
		if (trigger && /^(Arrow(Left|Right)|Home|End)$/.test(e.key)) detect();
	}

	async function search() {
		const mine = ++seq;
		const kind = trigger;
		const q = query;
		if (!kind) return;
		loading = true;
		try {
			const found = kind === '@' ? await searchPeople(q) : await searchIssues(q, kind === '!');
			if (mine !== seq) return;
			items = found.slice(0, LIMIT);
			if (active >= items.length) active = 0;
		} catch {
			if (mine === seq) items = [];
		} finally {
			if (mine === seq) loading = false;
		}
	}

	async function searchPeople(q: string): Promise<Item[]> {
		const out: Item[] = [];
		const users = await rpcClient.user.searchUsers(
			{ page: { pageSize: LIMIT, query: { text: q, filters: [] } } },
			silentCallOptions
		);
		for (const u of users.users) {
			out.push({
				key: `user:${u.id}`,
				insert: `@${u.username}`,
				title: u.username,
				detail: u.displayName && u.displayName !== u.username ? u.displayName : undefined,
				initials: u.username.slice(0, 2).toUpperCase()
			});
		}
		if (orgId) {
			try {
				const teams = await rpcClient.organization.listTeams(
					{ orgId, page: { pageSize: LIMIT, query: { text: q, filters: [] } } },
					silentCallOptions
				);
				for (const t of teams.teams) {
					out.push({
						key: `team:${t.id}`,
						insert: `@${ns}/${t.name}`,
						title: `${ns}/${t.name}`,
						detail: 'Team',
						icon: Users
					});
				}
			} catch {
				// Teams are optional here
			}
		}
		return out;
	}

	function issueIcon(i: Issue): Pick<Item, 'icon' | 'tone'> {
		if (i.isPull) {
			if (i.pull?.merged) return { icon: GitMerge, tone: 'text-done' };
			return {
				icon: GitPullRequest,
				tone: i.state === IssueState.CLOSED ? 'text-destructive' : 'text-success'
			};
		}
		return i.state === IssueState.CLOSED
			? { icon: CheckCircle, tone: 'text-done' }
			: { icon: CircleDot, tone: 'text-success' };
	}

	// Hash completes issues, bang completes pull requests
	async function searchIssues(q: string, pulls: boolean): Promise<Item[]> {
		const out: Item[] = [];
		const seen = new SvelteSet<string>();
		const add = (i: Issue) => {
			if (seen.has(i.id)) return;
			seen.add(i.id);
			out.push({
				key: `issue:${i.id}`,
				insert: issueRef(i.isPull, i.number),
				title: i.title,
				detail: issueRef(i.isPull, i.number),
				...issueIcon(i)
			});
		};
		const numeric = /^\d+$/.test(q);
		if (numeric) {
			try {
				const exact = await rpcClient.issue.getIssue(
					{ namespace: ns, name: nm, number: BigInt(q), isPull: pulls },
					silentCallOptions
				);
				if (exact.issue) add(exact.issue);
			} catch {
				// No issue with that number
			}
		}
		const resp = await rpcClient.issue.listIssues(
			{
				namespace: ns,
				name: nm,
				state: IssueState.OPEN,
				isPull: pulls,
				page: { pageSize: numeric ? 40 : LIMIT, query: { text: numeric ? '' : q, filters: [] } }
			},
			silentCallOptions
		);
		for (const i of resp.issues) {
			if (!numeric || String(i.number).startsWith(q)) add(i);
		}
		return out;
	}

	const MIRROR_PROPS = [
		'box-sizing',
		'width',
		'padding-top',
		'padding-right',
		'padding-bottom',
		'padding-left',
		'border-top-width',
		'border-right-width',
		'border-bottom-width',
		'border-left-width',
		'font-family',
		'font-size',
		'font-weight',
		'font-style',
		'letter-spacing',
		'line-height',
		'text-transform',
		'text-indent',
		'word-spacing',
		'tab-size',
		'white-space',
		'overflow-wrap'
	];

	// Caret pixels from a hidden mirror of the textarea
	function position(index: number) {
		const el = textarea;
		if (!el) return;
		const computed = getComputedStyle(el);
		const mirror = document.createElement('div');
		for (const p of MIRROR_PROPS) mirror.style.setProperty(p, computed.getPropertyValue(p));
		Object.assign(mirror.style, {
			position: 'absolute',
			top: '0',
			left: '-9999px',
			height: 'auto',
			overflow: 'hidden',
			visibility: 'hidden',
			whiteSpace: 'pre-wrap',
			overflowWrap: 'break-word'
		});
		mirror.textContent = el.value.slice(0, index);
		const marker = document.createElement('span');
		marker.textContent = el.value.slice(index) || '.';
		mirror.appendChild(marker);
		document.body.appendChild(mirror);
		const rect = el.getBoundingClientRect();
		const line = parseFloat(computed.lineHeight) || marker.offsetHeight || 20;
		const top = rect.top + marker.offsetTop - el.scrollTop;
		const left = rect.left + marker.offsetLeft - el.scrollLeft;
		mirror.remove();
		caret = { top, bottom: top + line, left };
	}

	function onKeydown(e: KeyboardEvent) {
		if (!open) return;
		switch (e.key) {
			case 'ArrowDown':
				if (items.length) active = (active + 1) % items.length;
				break;
			case 'ArrowUp':
				if (items.length) active = (active - 1 + items.length) % items.length;
				break;
			case 'Enter':
			case 'Tab': {
				const it = items[active];
				if (!it) return;
				pick(it);
				break;
			}
			case 'Escape':
				close();
				break;
			default:
				return;
		}
		e.preventDefault();
		e.stopPropagation();
	}

	// Replaces the token with the pick and a trailing space
	function pick(item: Item) {
		const el = textarea;
		if (!el) return;
		const head = el.value.slice(0, start);
		const tail = el.value.slice(el.selectionStart);
		const text = item.insert + (tail.startsWith(' ') ? '' : ' ');
		value = head + text + tail;
		close();
		void tick().then(() => {
			el.focus();
			const c = head.length + text.length;
			el.setSelectionRange(c, c);
		});
	}

	$effect(() => {
		const el = textarea;
		if (!el) return;
		el.addEventListener('input', detect);
		el.addEventListener('keyup', onKeyup);
		el.addEventListener('keydown', onKeydown, true);
		el.addEventListener('click', close);
		el.addEventListener('blur', close);
		return () => {
			el.removeEventListener('input', detect);
			el.removeEventListener('keyup', onKeyup);
			el.removeEventListener('keydown', onKeydown, true);
			el.removeEventListener('click', close);
			el.removeEventListener('blur', close);
		};
	});

	$effect(() => {
		if (!open) return;
		const move = () => position(start);
		window.addEventListener('scroll', move, true);
		window.addEventListener('resize', move);
		return () => {
			window.removeEventListener('scroll', move, true);
			window.removeEventListener('resize', move);
		};
	});
</script>

{#if open}
	<div
		class="fixed z-50 overflow-hidden rounded-md border bg-popover py-1 text-popover-foreground shadow-lg dark:ring-1 dark:ring-white/5"
		{style}
		role="listbox"
		aria-label="Suggestions"
	>
		{#if items.length === 0}
			<div class="flex h-8 items-center gap-2 px-2.5 text-xs text-muted-foreground">
				<Loader class="size-3.5 animate-spin" />Searching
			</div>
		{/if}
		{#each items as it, i (it.key)}
			<button
				type="button"
				role="option"
				aria-selected={i === active}
				class="flex h-8 w-full items-center gap-2 px-2.5 text-left text-sm {i === active
					? 'bg-muted'
					: ''}"
				onmousedown={(e) => e.preventDefault()}
				onclick={() => pick(it)}
				onmouseenter={() => (active = i)}
			>
				{#if it.icon}
					{@const Icon = it.icon}
					<Icon class="size-3.5 shrink-0 {it.tone ?? 'text-muted-foreground'}" />
				{:else}
					<Avatar class="size-5 shrink-0">
						<AvatarFallback class="text-[10px]">{it.initials}</AvatarFallback>
					</Avatar>
				{/if}
				<span class="truncate font-medium">{it.title}</span>
				{#if it.detail}<span class="truncate text-xs text-muted-foreground">{it.detail}</span>{/if}
			</button>
		{/each}
	</div>
{/if}

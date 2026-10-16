<script lang="ts">
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { CircleDot, FileText, ClipboardList, ArrowLeft, SquareKanban, Check, Settings2 } from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Checkbox } from '$lib/components/ui/checkbox';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Popover, PopoverContent, PopoverTrigger } from '$lib/components/ui/popover';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import EmptyState from '$lib/components/empty-state.svelte';
	import FormCard from '$lib/components/form-card.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import CommentBox from '$lib/components/git/comment-box.svelte';
	import Markdown from '$lib/components/git/markdown.svelte';
	import LabelPicker from '$lib/components/git/label-picker.svelte';
	import AssigneePicker from '$lib/components/git/assignee-picker.svelte';
	import MilestonePicker from '$lib/components/git/milestone-picker.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { authStore } from '$lib/stores/auth.svelte';
	import { GitUnit, type GitIssueTemplate } from '$lib/proto/distroface/v1/git_pb';
	import type { Project } from '$lib/proto/distroface/v1/project_pb';
	import { parseIssueForm, initialFormValues, missingRequired, serializeForm, type IssueForm, type FormValues } from '$lib/issue-template';
	import { timestampFromDate } from '@bufbuild/protobuf/wkt';
	import { cn } from '$lib/utils';
	import { toast } from 'svelte-sonner';

	const repo = $derived(gitRepoStore.repo!);
	const namespace = $derived(repo.namespace);
	const name = $derived(repo.name);
	const base = $derived(`/${namespace}/${name}`);
	const canCreate = $derived(authStore.isAuthenticated && gitRepoStore.canRead(GitUnit.ISSUES) && !repo.isArchived);
	const canTriage = $derived(gitRepoStore.canWrite(GitUnit.ISSUES));

	let templates = $state<GitIssueTemplate[]>([]);
	let templatesLoaded = $state(false);
	let chosen = $state<GitIssueTemplate | null>(null);
	let choosing = $state(false);

	let title = $state('');
	let body = $state('');
	let form = $state<IssueForm | null>(null);
	let values = $state<FormValues>({});
	let missing = $state<string[]>([]);
	let labelIds = $state<string[]>([]);
	let assigneeIds = $state<string[]>([]);
	let milestoneId = $state('');
	let projectId = $state('');
	let dueDate = $state('');
	let projects = $state<Project[]>([]);
	let projectOpen = $state(false);
	let submitting = $state(false);

	async function loadTemplates() {
		try {
			const resp = await rpcClient.gitFiles.getTemplates({ namespace, name }, silentCallOptions);
			templates = resp.issueTemplates;
			const wanted = page.url.searchParams.get('template');
			const match = wanted ? templates.find((t) => t.filename === wanted || t.name === wanted) : undefined;
			if (match) pick(match);
			else choosing = templates.length > 0;
		} catch {
			templates = [];
		} finally {
			templatesLoaded = true;
		}
	}

	async function loadProjects() {
		try {
			const resp = await rpcClient.project.listProjects({ namespace, name, state: 'open', page: { pageSize: 100, pageToken: '', orderBy: '' } }, silentCallOptions);
			projects = resp.projects;
		} catch {
			projects = [];
		}
	}

	onMount(() => {
		gitRepoStore.load(namespace, name);
		loadTemplates();
		loadProjects();
		const t = page.url.searchParams.get('title');
		if (t) title = t;
	});

	// Template label names and assignee usernames resolve to ids
	async function applyDefaults(t: GitIssueTemplate) {
		if (t.labels.length) {
			try {
				const resp = await rpcClient.issue.listLabels({ namespace, name, page: { pageSize: 500, pageToken: '', orderBy: '' } }, silentCallOptions);
				const wanted = new Set(t.labels.map((l) => l.toLowerCase()));
				labelIds = resp.labels.filter((l) => wanted.has(l.name.toLowerCase())).map((l) => l.id);
			} catch {
				// Labels stay empty
			}
		}
		if (t.assignees.length) {
			const ids: string[] = [];
			for (const username of t.assignees) {
				try {
					const resp = await rpcClient.user.getUser({ username }, silentCallOptions);
					if (resp.user) ids.push(resp.user.id);
				} catch {
					// Unknown username in the template
				}
			}
			assigneeIds = ids;
		}
	}

	function pick(t: GitIssueTemplate | null) {
		chosen = t;
		choosing = false;
		form = null;
		values = {};
		missing = [];
		if (!t) return;
		if (t.title && !title) title = t.title;
		if (t.isForm) {
			form = parseIssueForm(t.body);
			values = initialFormValues(form);
			if (form.title && !title) title = form.title;
		} else {
			body = t.body;
		}
		applyDefaults(t);
	}

	function toggleCheck(id: string, index: number, checked: boolean) {
		const arr = [...((values[id] as boolean[]) ?? [])];
		arr[index] = checked;
		values = { ...values, [id]: arr };
	}

	function setDropdown(id: string, option: string, multiple: boolean) {
		const current = (values[id] as string[]) ?? [];
		let next: string[];
		if (multiple) next = current.includes(option) ? current.filter((o) => o !== option) : [...current, option];
		else next = current.includes(option) ? [] : [option];
		values = { ...values, [id]: next };
	}

	async function submit() {
		if (submitting) return;
		if (!title.trim()) {
			toast.error('Add a title');
			return;
		}
		let finalBody = body;
		if (form) {
			missing = missingRequired(form, values);
			if (missing.length) {
				toast.error('Fill in the required fields');
				return;
			}
			finalBody = serializeForm(form, values);
		}
		submitting = true;
		try {
			const due = dueDate ? new Date(`${dueDate}T12:00:00`) : null;
			const resp = await rpcClient.issue.createIssue({
				namespace,
				name,
				title: title.trim(),
				body: finalBody,
				labelIds,
				assigneeIds,
				milestoneId,
				projectId,
				template: chosen?.filename ?? '',
				dueDate: due && !Number.isNaN(due.getTime()) ? timestampFromDate(due) : undefined
			});
			toast.success('Issue opened');
			const opened = resp.issue?.number;
			if (opened !== undefined) await goto(resolve('/[namespace]/[name]/issues/[number]', { namespace, name, number: String(opened) }));
			else await goto(resolve('/[namespace]/[name]/issues', { namespace, name }));
		} catch {
			// Error interceptor already toasted
		} finally {
			submitting = false;
		}
	}

	const currentProject = $derived(projects.find((p) => p.id === projectId));
</script>

{#if !canCreate}
	<EmptyState icon={CircleDot} message={authStore.isAuthenticated ? 'You cannot open issues here' : 'Sign in to open an issue'} description={authStore.isAuthenticated ? 'Read access on issues is required, or the repository is archived.' : ''}>
		{#snippet actions()}
			{#if !authStore.isAuthenticated}
				<Button size="sm" href="/login">Sign in</Button>
			{:else}
				<Button variant="outline" size="sm" href="{base}/issues">Back to issues</Button>
			{/if}
		{/snippet}
	</EmptyState>
{:else if !templatesLoaded}
	<div class="space-y-3">
		<Skeleton class="h-8 w-48" />
		<Skeleton class="h-64 w-full rounded-xl" />
	</div>
{:else if choosing}
	<div class="space-y-4 max-w-3xl">
		<div class="flex items-center gap-2 flex-wrap">
			<Button variant="ghost" size="sm" class="h-8" href="{base}/issues"><ArrowLeft class="h-3.5 w-3.5 mr-1" />Issues</Button>
			<h2 class="section-title">Choose a template</h2>
		</div>
		<div class="rounded-xl border border-border/60 bg-card divide-y divide-border/40">
			{#each templates as t (t.filename)}
				<div class="flex items-center gap-3 px-4 py-3">
					<div class="h-9 w-9 rounded-lg bg-primary/10 flex items-center justify-center shrink-0">
						{#if t.isForm}<ClipboardList class="h-4 w-4 text-primary" />{:else}<FileText class="h-4 w-4 text-primary" />{/if}
					</div>
					<div class="min-w-0 flex-1">
						<p class="font-medium text-[14px]">{t.name}</p>
						<p class="text-[12.5px] text-muted-foreground truncate">{t.about || t.filename}</p>
					</div>
					<Button size="sm" variant="outline" class="h-8 shrink-0" onclick={() => pick(t)}>Get started</Button>
				</div>
			{/each}
			<div class="flex items-center gap-3 px-4 py-3">
				<div class="h-9 w-9 rounded-lg bg-muted flex items-center justify-center shrink-0"><CircleDot class="h-4 w-4 text-muted-foreground" /></div>
				<div class="min-w-0 flex-1">
					<p class="font-medium text-[14px]">Blank issue</p>
					<p class="text-[12.5px] text-muted-foreground">Start from scratch.</p>
				</div>
				<Button size="sm" variant="outline" class="h-8 shrink-0" onclick={() => pick(null)}>Open</Button>
			</div>
		</div>
	</div>
{:else}
	<div class="space-y-4">
		<div class="flex items-center gap-2 flex-wrap">
			<Button variant="ghost" size="sm" class="h-8" href="{base}/issues"><ArrowLeft class="h-3.5 w-3.5 mr-1" />Issues</Button>
			<h2 class="section-title">New issue</h2>
			{#if chosen}
				<span class="text-[12.5px] text-muted-foreground">from {chosen.name}</span>
				{#if templates.length}
					<button type="button" class="text-[12.5px] text-primary hover:underline" onclick={() => (choosing = true)}>change</button>
				{/if}
			{:else if templates.length}
				<button type="button" class="text-[12.5px] text-primary hover:underline" onclick={() => (choosing = true)}>use a template</button>
			{/if}
		</div>

		<div class="grid gap-6 lg:grid-cols-[minmax(0,1fr)_16rem]">
			<div class="min-w-0">
				<FormCard title={chosen ? chosen.name : 'Describe the issue'} description={chosen?.about || 'A clear title and the steps to reproduce help others act on it.'} icon={CircleDot}>
					<div class="space-y-4">
						<FormField label="Title" id="issue-title" required>
							<Input id="issue-title" bind:value={title} placeholder="Short summary of the problem" autofocus />
						</FormField>
						{#if form}
							{#each form.fields as f (f.id)}
								{@const bad = missing.includes(f.id)}
								{#if f.type === 'markdown'}
									<div class="text-sm rounded-lg border border-border/60 bg-muted/20 px-4 py-3"><Markdown source={f.value} /></div>
								{:else}
									<FormField label={f.label} id="form-{f.id}" required={f.required || f.options.some((o) => o.required)} error={bad ? 'This field is required' : ''}>
										{#if f.description}<div class="text-[13px] text-muted-foreground"><Markdown source={f.description} /></div>{/if}
										{#if f.type === 'input'}
											<Input id="form-{f.id}" value={String(values[f.id] ?? '')} oninput={(e) => (values = { ...values, [f.id]: (e.currentTarget as HTMLInputElement).value })} placeholder={f.placeholder} aria-invalid={bad} />
										{:else if f.type === 'textarea'}
											<Textarea id="form-{f.id}" value={String(values[f.id] ?? '')} oninput={(e) => (values = { ...values, [f.id]: (e.currentTarget as HTMLTextAreaElement).value })} placeholder={f.placeholder} rows={5} class={cn(f.render && 'font-mono')} aria-invalid={bad} />
										{:else if f.type === 'dropdown'}
											{@const picked = (values[f.id] as string[]) ?? []}
											{#if f.multiple}
												<div class="space-y-1.5">
													{#each f.options as o, oi (oi)}
														<label class="flex items-center gap-2 text-[13px]">
															<Checkbox checked={picked.includes(o.label)} onCheckedChange={() => setDropdown(f.id, o.label, true)} />
															{o.label}
														</label>
													{/each}
												</div>
											{:else}
												<Select type="single" value={picked[0] ?? ''} onValueChange={(v) => (values = { ...values, [f.id]: v ? [v] : [] })}>
													<SelectTrigger id="form-{f.id}" class="w-full" aria-invalid={bad}>{picked[0] ?? 'Select one'}</SelectTrigger>
													<SelectContent>
														{#each f.options as o, oi (oi)}
															<SelectItem value={o.label}>{o.label}</SelectItem>
														{/each}
													</SelectContent>
												</Select>
											{/if}
										{:else if f.type === 'checkboxes'}
											{@const checks = (values[f.id] as boolean[]) ?? []}
											<div class="space-y-1.5">
												{#each f.options as o, oi (oi)}
													<label class="flex items-center gap-2 text-[13px]">
														<Checkbox checked={!!checks[oi]} onCheckedChange={(v) => toggleCheck(f.id, oi, !!v)} />
														<span class="flex-1"><Markdown source={o.label} class="inline" /></span>
														{#if o.required}<span class="text-destructive">*</span>{/if}
													</label>
												{/each}
											</div>
										{/if}
									</FormField>
								{/if}
							{/each}
						{:else}
							<FormField label="Description" id="issue-body">
								<CommentBox bind:value={body} placeholder="Describe the issue" submitLabel="Open Issue" {submitting} onSubmit={submit} hideSubmit />
							</FormField>
						{/if}
					</div>
					{#snippet footer()}
						<Button variant="outline" href="{base}/issues">Cancel</Button>
						<Button onclick={submit} disabled={submitting || !title.trim()}>{submitting ? 'Opening...' : 'Open Issue'}</Button>
					{/snippet}
				</FormCard>
			</div>

			<aside class="grid gap-5 sm:grid-cols-2 lg:block lg:space-y-5 text-sm">
				<section class="space-y-2">
					<AssigneePicker {namespace} {name} bind:selected={assigneeIds} canEdit={canTriage} />
				</section>
				<section class="space-y-2 lg:pt-5 lg:border-t lg:border-border/50">
					<LabelPicker {namespace} {name} bind:selected={labelIds} canEdit={canTriage} />
				</section>
				<section class="space-y-2 lg:pt-5 lg:border-t lg:border-border/50">
					<MilestonePicker {namespace} {name} bind:selected={milestoneId} canEdit={canTriage} />
				</section>
				<section class="space-y-2 lg:pt-5 lg:border-t lg:border-border/50">
					<div class="flex items-center justify-between">
						<span class="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">Project</span>
						{#if canTriage && projects.length}
							<Popover bind:open={projectOpen}>
								<PopoverTrigger>
									{#snippet child({ props })}
										<button {...props} type="button" class="h-6 w-6 inline-flex items-center justify-center rounded-md text-muted-foreground hover:text-foreground hover:bg-accent" title="Choose project" aria-label="Choose project"><Settings2 class="h-3.5 w-3.5" /></button>
									{/snippet}
								</PopoverTrigger>
								<PopoverContent class="w-64 p-0" align="end">
									<div class="max-h-64 overflow-y-auto py-1">
										{#if projectId}
											<button type="button" class="w-full px-2.5 py-1.5 text-left text-[12.5px] text-muted-foreground hover:bg-accent" onclick={() => { projectId = ''; projectOpen = false; }}>No project</button>
										{/if}
										{#each projects as p (p.id)}
											<button type="button" class={cn('w-full flex items-center gap-2 px-2.5 py-1.5 text-left text-[13px] hover:bg-accent', p.id === projectId && 'bg-accent/50')} onclick={() => { projectId = p.id; projectOpen = false; }}>
												<span class="h-4 w-4 inline-flex items-center justify-center">{#if p.id === projectId}<Check class="h-3.5 w-3.5 text-primary" />{/if}</span>
												<span class="truncate">{p.title}</span>
											</button>
										{/each}
									</div>
								</PopoverContent>
							</Popover>
						{/if}
					</div>
					{#if currentProject}
						<span class="flex items-center gap-1.5 text-[13px]"><SquareKanban class="h-3.5 w-3.5 text-muted-foreground" />{currentProject.title}</span>
					{:else}
						<span class="text-[12.5px] text-muted-foreground">None</span>
					{/if}
				</section>
				{#if canTriage}
					<section class="space-y-2 lg:pt-5 lg:border-t lg:border-border/50">
						<FormField label="Due date" id="issue-due" bordered={false}>
							<Input id="issue-due" type="date" bind:value={dueDate} />
						</FormField>
					</section>
				{/if}
			</aside>
		</div>
	</div>
{/if}

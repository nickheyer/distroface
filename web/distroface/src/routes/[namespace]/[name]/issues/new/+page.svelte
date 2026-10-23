<script lang="ts">
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import {
		CircleDot,
		FileText,
		ClipboardList,
		ArrowLeft,
		SquareKanban,
		Check,
		Settings2,
		Loader2
	} from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import DateInput from '$lib/components/date-input.svelte';
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
	import {
		parseIssueForm,
		initialFormValues,
		missingRequired,
		serializeForm,
		type IssueForm,
		type FormValues
	} from '$lib/issue-template';
	import { timestampFromDate } from '@bufbuild/protobuf/wkt';
	import { cn } from '$lib/utils';
	import { toast } from 'svelte-sonner';

	const repo = $derived(gitRepoStore.repo!);
	const namespace = $derived(repo.namespace);
	const name = $derived(repo.name);
	const base = $derived(`/${namespace}/${name}`);
	const canCreate = $derived(
		authStore.isAuthenticated && gitRepoStore.canRead(GitUnit.ISSUES) && !repo.isArchived
	);
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
			const match = wanted
				? templates.find((t) => t.filename === wanted || t.name === wanted)
				: undefined;
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
			const resp = await rpcClient.project.listProjects(
				{ namespace, name, state: 'open', page: { pageSize: 100, pageToken: '', orderBy: '' } },
				silentCallOptions
			);
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
				const resp = await rpcClient.issue.listLabels(
					{ namespace, name, page: { pageSize: 500, pageToken: '', orderBy: '' } },
					silentCallOptions
				);
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
		if (multiple)
			next = current.includes(option) ? current.filter((o) => o !== option) : [...current, option];
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
			if (opened !== undefined)
				await goto(
					resolve('/[namespace]/[name]/issues/[number]', {
						namespace,
						name,
						number: String(opened)
					})
				);
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
	<EmptyState
		icon={CircleDot}
		message={authStore.isAuthenticated ? 'You cannot open issues here' : 'Sign in to open an issue'}
		description={authStore.isAuthenticated
			? 'Read access on issues is required, or the repository is archived.'
			: ''}
	>
		{#snippet actions()}
			{#if !authStore.isAuthenticated}
				<Button href="/login">Sign in</Button>
			{:else}
				<Button variant="outline" href="{base}/issues">Back to issues</Button>
			{/if}
		{/snippet}
	</EmptyState>
{:else if !templatesLoaded}
	<div class="space-y-3">
		<Skeleton class="h-8 w-48" />
		<Skeleton class="h-64 w-full rounded-lg" />
	</div>
{:else if choosing}
	<div class="max-w-3xl space-y-4">
		<div class="flex flex-wrap items-center gap-2">
			<Button variant="ghost" href="{base}/issues"><ArrowLeft />Issues</Button>
			<h2 class="section-title">Choose a template</h2>
		</div>
		<div class="box divide-y">
			{#each templates as t (t.filename)}
				<div class="flex items-center gap-3 px-4 py-3">
					{#if t.isForm}<ClipboardList
							class="size-4 shrink-0 text-muted-foreground"
						/>{:else}<FileText class="size-4 shrink-0 text-muted-foreground" />{/if}
					<div class="min-w-0 flex-1">
						<p class="text-sm font-medium">{t.name}</p>
						<p class="truncate text-xs text-muted-foreground">{t.about || t.filename}</p>
					</div>
					<Button variant="outline" class="shrink-0" onclick={() => pick(t)}>Get started</Button>
				</div>
			{/each}
			<div class="flex items-center gap-3 px-4 py-3">
				<CircleDot class="size-4 shrink-0 text-muted-foreground" />
				<div class="min-w-0 flex-1">
					<p class="text-sm font-medium">Blank issue</p>
					<p class="text-xs text-muted-foreground">Start from scratch</p>
				</div>
				<Button variant="outline" class="shrink-0" onclick={() => pick(null)}>Open</Button>
			</div>
		</div>
	</div>
{:else}
	<div class="space-y-4">
		<div class="flex flex-wrap items-center gap-2">
			<Button variant="ghost" href="{base}/issues"><ArrowLeft />Issues</Button>
			<h2 class="section-title">New issue</h2>
			{#if chosen}
				<span class="text-xs text-muted-foreground">from {chosen.name}</span>
				{#if templates.length}
					<button type="button" class="link text-xs" onclick={() => (choosing = true)}
						>change</button
					>
				{/if}
			{:else if templates.length}
				<button type="button" class="link text-xs" onclick={() => (choosing = true)}
					>use a template</button
				>
			{/if}
		</div>

		<div class="grid gap-6 lg:grid-cols-[minmax(0,1fr)_16rem]">
			<div class="min-w-0">
				<FormCard
					title={chosen ? chosen.name : 'Describe the issue'}
					description={chosen?.about ||
						'A clear title and the steps to reproduce help others act on it.'}
					icon={CircleDot}
				>
					<div class="space-y-4">
						<FormField label="Title" id="issue-title" required>
							<Input
								id="issue-title"
								bind:value={title}
								placeholder="Short summary of the problem"
								autofocus
							/>
						</FormField>
						{#if form}
							{#each form.fields as f (f.id)}
								{@const bad = missing.includes(f.id)}
								{#if f.type === 'markdown'}
									<div class="rounded-lg border bg-muted/40 px-4 py-3 text-sm">
										<Markdown source={f.value} />
									</div>
								{:else}
									<FormField
										label={f.label}
										id="form-{f.id}"
										required={f.required || f.options.some((o) => o.required)}
										error={bad ? 'This field is required' : ''}
									>
										{#if f.description}<div class="text-sm text-muted-foreground">
												<Markdown source={f.description} />
											</div>{/if}
										{#if f.type === 'input'}
											<Input
												id="form-{f.id}"
												value={String(values[f.id] ?? '')}
												oninput={(e) =>
													(values = {
														...values,
														[f.id]: (e.currentTarget as HTMLInputElement).value
													})}
												placeholder={f.placeholder}
												aria-invalid={bad}
											/>
										{:else if f.type === 'textarea'}
											<Textarea
												id="form-{f.id}"
												value={String(values[f.id] ?? '')}
												oninput={(e) =>
													(values = {
														...values,
														[f.id]: (e.currentTarget as HTMLTextAreaElement).value
													})}
												placeholder={f.placeholder}
												rows={5}
												class={cn(f.render && 'font-mono')}
												aria-invalid={bad}
											/>
										{:else if f.type === 'dropdown'}
											{@const picked = (values[f.id] as string[]) ?? []}
											{#if f.multiple}
												<div class="space-y-1.5">
													{#each f.options as o, oi (oi)}
														<label class="flex items-center gap-2 text-sm">
															<Checkbox
																checked={picked.includes(o.label)}
																onCheckedChange={() => setDropdown(f.id, o.label, true)}
															/>
															{o.label}
														</label>
													{/each}
												</div>
											{:else}
												<Select
													type="single"
													value={picked[0] ?? ''}
													onValueChange={(v) => (values = { ...values, [f.id]: v ? [v] : [] })}
												>
													<SelectTrigger id="form-{f.id}" class="w-full" aria-invalid={bad}
														>{picked[0] ?? 'Select one'}</SelectTrigger
													>
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
													<label class="flex items-center gap-2 text-sm">
														<Checkbox
															checked={!!checks[oi]}
															onCheckedChange={(v) => toggleCheck(f.id, oi, !!v)}
														/>
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
								<CommentBox
									bind:value={body}
									placeholder="Describe the issue"
									submitLabel="Open issue"
									{submitting}
									onSubmit={submit}
									hideSubmit
								/>
							</FormField>
						{/if}
					</div>
					{#snippet footer()}
						<Button variant="outline" href="{base}/issues">Cancel</Button>
						<Button onclick={submit} disabled={submitting || !title.trim()}>
							{#if submitting}
								<Loader2 class="animate-spin" />
							{/if}
							{submitting ? 'Opening' : 'Open issue'}
						</Button>
					{/snippet}
				</FormCard>
			</div>

			<aside class="grid gap-5 text-sm sm:grid-cols-2 lg:block lg:space-y-5">
				<section class="space-y-2">
					<AssigneePicker {namespace} {name} bind:selected={assigneeIds} canEdit={canTriage} />
				</section>
				<section class="space-y-2 lg:border-t lg:pt-5">
					<LabelPicker {namespace} {name} bind:selected={labelIds} canEdit={canTriage} />
				</section>
				<section class="space-y-2 lg:border-t lg:pt-5">
					<MilestonePicker {namespace} {name} bind:selected={milestoneId} canEdit={canTriage} />
				</section>
				<section class="space-y-2 lg:border-t lg:pt-5">
					<div class="flex items-center justify-between">
						<span class="eyebrow">Project</span>
						{#if canTriage && projects.length}
							<Popover bind:open={projectOpen}>
								<PopoverTrigger>
									{#snippet child({ props })}
										<Button
											{...props}
											variant="ghost-muted"
											size="icon-xs"
											title="Choose project"
											aria-label="Choose project"><Settings2 /></Button
										>
									{/snippet}
								</PopoverTrigger>
								<PopoverContent class="w-64 p-0" align="end">
									<div class="max-h-64 overflow-y-auto py-1">
										{#if projectId}
											<button
												type="button"
												class="w-full px-2.5 py-1.5 text-left text-sm text-muted-foreground hover:bg-accent"
												onclick={() => {
													projectId = '';
													projectOpen = false;
												}}>No project</button
											>
										{/if}
										{#each projects as p (p.id)}
											<button
												type="button"
												class={cn(
													'flex w-full items-center gap-2 px-2.5 py-1.5 text-left text-sm hover:bg-accent',
													p.id === projectId && 'bg-accent/50'
												)}
												onclick={() => {
													projectId = p.id;
													projectOpen = false;
												}}
											>
												<span class="inline-flex size-4 items-center justify-center"
													>{#if p.id === projectId}<Check
															class="size-3.5 text-primary"
														/>{/if}</span
												>
												<span class="truncate">{p.title}</span>
											</button>
										{/each}
									</div>
								</PopoverContent>
							</Popover>
						{/if}
					</div>
					{#if currentProject}
						<span class="flex items-center gap-1.5 text-sm"
							><SquareKanban class="size-3.5 text-muted-foreground" />{currentProject.title}</span
						>
					{:else}
						<span class="text-xs text-muted-foreground">None</span>
					{/if}
				</section>
				{#if canTriage}
					<section class="space-y-2 lg:border-t lg:pt-5">
						<FormField label="Due date" id="issue-due">
							<DateInput id="issue-due" bind:value={dueDate} />
						</FormField>
					</section>
				{/if}
			</aside>
		</div>
	</div>
{/if}

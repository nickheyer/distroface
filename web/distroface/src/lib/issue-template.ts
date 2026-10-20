// Minimal yaml reader for issue forms plus markdown output

export type FormFieldType = 'markdown' | 'input' | 'textarea' | 'dropdown' | 'checkboxes';

export interface FormOption {
	label: string;
	required: boolean;
}

export interface FormField {
	type: FormFieldType;
	id: string;
	label: string;
	description: string;
	placeholder: string;
	value: string;
	render: string;
	options: FormOption[];
	multiple: boolean;
	defaultIndex: number;
	required: boolean;
}

export interface IssueForm {
	title: string;
	fields: FormField[];
}

type YamlValue = string | number | boolean | null | YamlValue[] | { [key: string]: YamlValue };

interface Line {
	indent: number;
	text: string;
	raw: string;
}

function stripComment(text: string): string {
	let quote = '';
	for (let i = 0; i < text.length; i++) {
		const ch = text[i];
		if (quote) {
			if (ch === quote && text[i - 1] !== '\\') quote = '';
			continue;
		}
		if (ch === '"' || ch === "'") quote = ch;
		else if (ch === '#' && (i === 0 || /\s/.test(text[i - 1]))) return text.slice(0, i).trimEnd();
	}
	return text.trimEnd();
}

function parseScalar(raw: string): YamlValue {
	const text = raw.trim();
	if (text === '' || text === '~' || text === 'null') return null;
	if (text === 'true') return true;
	if (text === 'false') return false;
	if (/^-?\d+(\.\d+)?$/.test(text)) return Number(text);
	if (text.startsWith('"') && text.endsWith('"') && text.length >= 2) {
		return text
			.slice(1, -1)
			.replace(/\\(["\\nt])/g, (_, c: string) => (c === 'n' ? '\n' : c === 't' ? '\t' : c));
	}
	if (text.startsWith("'") && text.endsWith("'") && text.length >= 2) {
		return text.slice(1, -1).replace(/''/g, "'");
	}
	if (text.startsWith('[') && text.endsWith(']')) {
		const inner = text.slice(1, -1).trim();
		if (!inner) return [];
		return splitFlow(inner).map((item) => parseScalar(item));
	}
	return text;
}

function splitFlow(inner: string): string[] {
	const out: string[] = [];
	let cur = '';
	let quote = '';
	for (const ch of inner) {
		if (quote) {
			cur += ch;
			if (ch === quote) quote = '';
			continue;
		}
		if (ch === '"' || ch === "'") {
			quote = ch;
			cur += ch;
		} else if (ch === ',') {
			out.push(cur);
			cur = '';
		} else cur += ch;
	}
	if (cur.trim()) out.push(cur);
	return out;
}

class Reader {
	lines: Line[];
	pos = 0;

	constructor(source: string) {
		this.lines = [];
		for (const raw of source.replace(/\r\n?/g, '\n').split('\n')) {
			const text = stripComment(raw);
			const indent = raw.length - raw.trimStart().length;
			this.lines.push({ indent, text: text.trim(), raw });
		}
	}

	peek(): Line | undefined {
		while (this.pos < this.lines.length && this.lines[this.pos].text === '') this.pos++;
		return this.lines[this.pos];
	}

	block(indent: number, chomp: string): string {
		const out: string[] = [];
		let blockIndent = -1;
		while (this.pos < this.lines.length) {
			const line = this.lines[this.pos];
			const blank = line.raw.trim() === '';
			if (!blank && line.indent <= indent) break;
			if (!blank && blockIndent < 0) blockIndent = line.indent;
			out.push(blank ? '' : line.raw.slice(blockIndent));
			this.pos++;
		}
		while (out.length && out[out.length - 1] === '') out.pop();
		const text = out.join('\n');
		if (chomp === '-') return text;
		return text ? `${text}\n` : '';
	}

	value(indent: number, inline: string): YamlValue {
		const text = inline.trim();
		if (
			text === '|' ||
			text === '|-' ||
			text === '|+' ||
			text === '>' ||
			text === '>-' ||
			text === '>+'
		) {
			const folded = text.startsWith('>');
			const raw = this.block(indent, text.slice(1));
			return folded ? raw.replace(/([^\n])\n(?!\n)/g, '$1 ') : raw;
		}
		if (text !== '') return parseScalar(text);
		const next = this.peek();
		if (!next || next.indent <= indent) return null;
		return this.node(next.indent);
	}

	node(indent: number): YamlValue {
		const first = this.peek();
		if (!first) return null;
		if (first.text.startsWith('- ') || first.text === '-') return this.sequence(indent);
		return this.mapping(indent);
	}

	sequence(indent: number): YamlValue[] {
		const out: YamlValue[] = [];
		for (;;) {
			const line = this.peek();
			if (!line || line.indent !== indent || !(line.text.startsWith('- ') || line.text === '-'))
				break;
			this.pos++;
			const rest = line.text.slice(1).trim();
			if (rest === '') {
				const next = this.peek();
				out.push(next && next.indent > indent ? this.node(next.indent) : null);
				continue;
			}
			const m = /^([^\s"'[{][^:]*?):(\s+|$)(.*)$/.exec(rest);
			if (m) {
				const childIndent = indent + 2;
				const obj: { [key: string]: YamlValue } = {};
				obj[m[1].trim()] = this.value(childIndent, m[3]);
				const next = this.peek();
				if (next && next.indent === childIndent && !next.text.startsWith('- ')) {
					Object.assign(obj, this.mapping(childIndent));
				} else if (
					next &&
					next.indent > indent &&
					next.indent !== childIndent &&
					!next.text.startsWith('- ')
				) {
					Object.assign(obj, this.mapping(next.indent));
				}
				out.push(obj);
			} else {
				out.push(this.value(indent, rest));
			}
		}
		return out;
	}

	mapping(indent: number): { [key: string]: YamlValue } {
		const out: { [key: string]: YamlValue } = {};
		for (;;) {
			const line = this.peek();
			if (!line || line.indent !== indent || line.text.startsWith('- ')) break;
			const m = /^("[^"]*"|'[^']*'|[^:]+):(\s+|$)(.*)$/.exec(line.text);
			if (!m) {
				this.pos++;
				continue;
			}
			this.pos++;
			const key = m[1].trim().replace(/^["']|["']$/g, '');
			out[key] = this.value(indent, m[3]);
		}
		return out;
	}
}

export function parseYaml(source: string): YamlValue {
	const reader = new Reader(source);
	const first = reader.peek();
	if (!first) return null;
	return reader.node(first.indent);
}

function str(v: YamlValue | undefined): string {
	if (v === null || v === undefined) return '';
	if (typeof v === 'string') return v;
	if (typeof v === 'number' || typeof v === 'boolean') return String(v);
	return '';
}

function obj(v: YamlValue | undefined): { [key: string]: YamlValue } {
	return v && typeof v === 'object' && !Array.isArray(v) ? v : {};
}

export function parseIssueForm(source: string): IssueForm {
	const root = obj(parseYaml(source));
	const body = Array.isArray(root.body) ? root.body : [];
	const fields: FormField[] = [];
	body.forEach((entry, i) => {
		const e = obj(entry);
		const type = str(e.type) as FormFieldType;
		if (!['markdown', 'input', 'textarea', 'dropdown', 'checkboxes'].includes(type)) return;
		const attrs = obj(e.attributes);
		const validations = obj(e.validations);
		const rawOptions = Array.isArray(attrs.options) ? attrs.options : [];
		const options: FormOption[] = rawOptions.map((o) => {
			const oo = obj(o);
			return typeof o === 'object' && o !== null && !Array.isArray(o)
				? { label: str(oo.label), required: oo.required === true }
				: { label: str(o), required: false };
		});
		fields.push({
			type,
			id: str(e.id) || `field-${i}`,
			label: str(attrs.label),
			description: str(attrs.description),
			placeholder: str(attrs.placeholder),
			value: str(attrs.value),
			render: str(attrs.render),
			options,
			multiple: attrs.multiple === true,
			defaultIndex: typeof attrs.default === 'number' ? attrs.default : -1,
			required: validations.required === true
		});
	});
	return { title: str(root.title), fields };
}

export type FormValues = Record<string, string | string[] | boolean[]>;

export function initialFormValues(form: IssueForm): FormValues {
	const out: FormValues = {};
	for (const f of form.fields) {
		if (f.type === 'input' || f.type === 'textarea') out[f.id] = f.value;
		else if (f.type === 'dropdown')
			out[f.id] = f.multiple
				? []
				: f.defaultIndex >= 0 && f.options[f.defaultIndex]
					? [f.options[f.defaultIndex].label]
					: [];
		else if (f.type === 'checkboxes') out[f.id] = f.options.map(() => false);
	}
	return out;
}

// Required field ids the user has not filled
export function missingRequired(form: IssueForm, values: FormValues): string[] {
	const missing: string[] = [];
	for (const f of form.fields) {
		const v = values[f.id];
		if (f.type === 'input' || f.type === 'textarea') {
			if (f.required && !String(v ?? '').trim()) missing.push(f.id);
		} else if (f.type === 'dropdown') {
			if (f.required && (!Array.isArray(v) || v.length === 0)) missing.push(f.id);
		} else if (f.type === 'checkboxes') {
			const checks = Array.isArray(v) ? (v as boolean[]) : [];
			if (f.options.some((o, i) => o.required && !checks[i])) missing.push(f.id);
		}
	}
	return missing;
}

export function serializeForm(form: IssueForm, values: FormValues): string {
	const parts: string[] = [];
	for (const f of form.fields) {
		if (f.type === 'markdown') continue;
		const v = values[f.id];
		let body = '';
		if (f.type === 'input' || f.type === 'textarea') {
			const text = String(v ?? '').trim();
			if (!text) body = '_No response_';
			else if (f.type === 'textarea' && f.render) body = `\`\`\`${f.render}\n${text}\n\`\`\``;
			else body = text;
		} else if (f.type === 'dropdown') {
			const picked = Array.isArray(v) ? (v as string[]) : [];
			body = picked.length ? picked.join(', ') : '_No response_';
		} else if (f.type === 'checkboxes') {
			const checks = Array.isArray(v) ? (v as boolean[]) : [];
			body = f.options.map((o, i) => `- [${checks[i] ? 'x' : ' '}] ${o.label}`).join('\n');
		}
		parts.push(`### ${f.label || f.id}\n\n${body}`);
	}
	return parts.join('\n\n');
}

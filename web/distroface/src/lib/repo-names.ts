export type RepoFamily = 'artifact' | 'image' | 'git';

type NameRule = { pattern: RegExp; max: number; lower: boolean; help: string };

// Server rules from services/artifact.go, services/repository.go, and gitrepo/engine.go
const rules: Record<RepoFamily, NameRule> = {
	artifact: {
		pattern: /^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$/,
		max: 128,
		lower: false,
		help: 'Letters, digits, dots, dashes, underscores'
	},
	image: {
		pattern: /^[a-z0-9]+(?:[._-][a-z0-9]+)*$/,
		max: 255,
		lower: true,
		help: 'Lowercase letters, digits, dots, dashes, underscores'
	},
	git: {
		pattern: /^[a-z0-9][a-z0-9._-]{0,99}$/,
		max: 100,
		lower: true,
		help: 'Lowercase letters, digits, dots, dashes, underscores'
	}
};

export function repoNameHelp(family: RepoFamily): string {
	return rules[family].help;
}

// Empty when the server accepts the name for this family
export function repoNameError(family: RepoFamily, name: string): string {
	if (!rules[family].pattern.test(name)) return rules[family].help;
	if (family === 'git' && /\.(git|wiki)$/.test(name)) return 'Cannot end in .git or .wiki';
	return '';
}

// Last path segment of an upstream url, slug, or image reference, reshaped to the family rule
export function upstreamRepoName(upstream: string, family: RepoFamily): string {
	let s = upstream.trim().replace(/[?#].*$/, '');
	s = s.replace(/^[a-z][a-z0-9+.-]*:\/\//i, '');
	s = s.replace(/^[^/@:]+@[^/:]+:/, '');
	const segments = s.split('/').filter(Boolean);
	const dash = segments.indexOf('-');
	if (dash > 0) segments.length = dash;
	const last = (segments.at(-1) ?? '').replace(/[@:].*$/, '').replace(/\.(git|wiki)$/i, '');
	return normalize(family, last);
}

// Closest name the family rule accepts, empty when nothing usable remains
function normalize(family: RepoFamily, raw: string): string {
	const rule = rules[family];
	let s = rule.lower ? raw.toLowerCase() : raw;
	s = s.replace(/[^a-zA-Z0-9._-]+/g, '-');
	s = s.replace(/([._-])[._-]+/g, '$1');
	s = s.replace(/^[._-]+|[._-]+$/g, '');
	s = s.slice(0, rule.max).replace(/[._-]+$/, '');
	if (family === 'git') s = s.replace(/\.(git|wiki)$/, '');
	return s;
}

import { readdir, readFile, writeFile, mkdir, rm } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { base, repo } from '../site.config.mjs'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..')
const docsDir = path.join(root, 'docs')
const outDir = path.join(root, 'website', 'src', 'content', 'docs')

// Sidebar order follows the link order of the README docs index
async function readmeOrder() {
  const order = new Map()
  const readme = await readFile(path.join(root, 'README.md'), 'utf8')
  for (const m of readme.matchAll(/\]\(docs\/([^)#]+\.md)\)/g)) {
    if (!order.has(m[1])) order.set(m[1], order.size + 1)
  }
  return order
}

async function walk(dir) {
  const out = []
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name)
    if (entry.isDirectory()) out.push(...(await walk(full)))
    else if (entry.name.endsWith('.md')) out.push(full)
  }
  return out.sort()
}

function slugOf(rel) {
  const noExt = rel.replace(/\.md$/, '')
  return noExt.endsWith('/index') ? noExt.slice(0, -6) : noExt === 'index' ? '' : noExt
}

function rewriteLink(target, rel) {
  if (/^([a-z]+:|#|\/)/i.test(target)) return target
  const [p, hash = ''] = target.split('#')
  const resolved = path.posix.normalize(path.posix.join(path.posix.dirname(rel), p))
  if (resolved.startsWith('..')) {
    return `${repo}/blob/main/${path.posix.normalize(path.posix.join('docs', resolved))}`
  }
  if (!resolved.endsWith('.md')) return target
  return `${base}/${slugOf(resolved)}/${hash ? '#' + hash : ''}`
}

function transform(src, rel, order) {
  const lines = src.split('\n')
  let title = path.basename(rel, '.md')
  let fence = false
  const body = []
  for (const line of lines) {
    if (body.length === 0 && line.trim() === '') continue
    if (/^(```|~~~)/.test(line.trim())) fence = !fence
    if (!fence && body.length === 0 && /^# /.test(line)) {
      title = line.slice(2).trim()
      continue
    }
    if (fence) {
      body.push(line)
      continue
    }
    body.push(line.replace(/\]\(([^)\s]+)\)/g, (_, t) => `](${rewriteLink(t, rel)})`))
  }
  const fm = [`title: ${JSON.stringify(title)}`]
  const pos = order.get(rel)
  if (pos) fm.push(`sidebar:`, `  order: ${pos}`)
  return `---\n${fm.join('\n')}\n---\n${body.join('\n').replace(/^\n+/, '')}`
}

const order = await readmeOrder()
for (const entry of await readdir(outDir).catch(() => [])) {
  if (entry !== 'index.mdx') await rm(path.join(outDir, entry), { recursive: true })
}
const files = await walk(docsDir)
for (const file of files) {
  const rel = path.relative(docsDir, file).split(path.sep).join('/')
  const out = path.join(outDir, rel)
  await mkdir(path.dirname(out), { recursive: true })
  await writeFile(out, transform(await readFile(file, 'utf8'), rel, order))
}
console.log(`synced ${files.length} pages from docs/`)

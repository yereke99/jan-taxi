#!/usr/bin/env node
// Shared with the other Mini App — keep both copies identical (make verify-shared).
//
// Project rules that the type checker cannot enforce (ARCHITECTURE §11, §14,
// §15, §18). Run from an app root: `node scripts/lint.mjs`.

import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'

const root = process.cwd()
const src = join(root, 'src')

function walk(dir) {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = join(dir, e.name)
    return e.isDirectory() ? walk(p) : [p]
  })
}

/** Removes comments so rules only see code. Good enough for our sources. */
function stripComments(text) {
  return text
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/(^|[^:'"`])\/\/.*$/gm, '$1')
}

const RULES = [
  {
    id: 'no-100vh',
    why: 'use var(--jt-vh): 100vh ignores the Telegram viewport (§15)',
    files: /\.(vue|css|ts)$/,
    test: (code) => /\b100d?vh\b/.test(code),
  },
  {
    id: 'no-browser-dialogs',
    why: 'use JActionSheet / toast instead of alert/confirm/prompt (§15)',
    files: /\.(vue|ts)$/,
    test: (code) => /(^|[^.\w])(window\.)?(alert|confirm|prompt)\s*\(/m.test(code),
  },
  {
    id: 'no-v-html',
    why: 'user text must be escaped by Vue interpolation (§11)',
    files: /\.vue$/,
    test: (code) => /\sv-html\s*=/.test(code),
  },
  {
    id: 'no-console-log',
    why: 'use console.warn/error for real problems; no debug logging in commits',
    files: /\.(vue|ts)$/,
    test: (code) => /console\.log\s*\(/.test(code),
  },
  {
    id: 'sdk-through-telegram-module',
    why: "import Telegram APIs from '@/telegram' so SDK usage stays in one place",
    files: /\.(vue|ts)$/,
    skip: (rel) => rel.startsWith('src/telegram/'),
    test: (code) => /from\s+['"]@tma\.js\/sdk(-vue)?['"]/.test(code),
  },
]

const problems = []
for (const file of walk(src)) {
  const rel = relative(root, file).split('\\').join('/')
  if (rel.endsWith('.test.ts')) continue
  const code = stripComments(readFileSync(file, 'utf8'))
  for (const rule of RULES) {
    if (!rule.files.test(rel) || rule.skip?.(rel)) continue
    if (rule.test(code)) problems.push(`${rel}: ${rule.id} — ${rule.why}`)
  }
}

const html = join(root, 'index.html')
if (existsSync(html) && statSync(html).isFile() && /telegram-web-app\.js/.test(readFileSync(html, 'utf8'))) {
  problems.push('index.html: no-telegram-script — @tma.js/sdk talks to the client directly; do not load telegram-web-app.js')
}

if (problems.length > 0) {
  console.error(`lint: ${problems.length} problem(s)`)
  for (const p of problems) console.error(`  ${p}`)
  process.exit(1)
}
console.log('lint: ok')

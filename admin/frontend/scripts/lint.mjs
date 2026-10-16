#!/usr/bin/env node
// Project rules the type checker cannot enforce (ARCHITECTURE §11, §15).
// Run from admin/frontend: `node scripts/lint.mjs`.

import { readdirSync, readFileSync } from 'node:fs'
import { join, relative } from 'node:path'

const root = process.cwd()

function walk(dir) {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = join(dir, e.name)
    return e.isDirectory() ? walk(p) : [p]
  })
}

function stripComments(text) {
  return text
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/(^|[^:'"`])\/\/.*$/gm, '$1')
}

const RULES = [
  {
    id: 'no-browser-dialogs',
    why: 'use ConfirmDialog/confirmAction and toast instead of alert/confirm/prompt',
    files: /\.(vue|ts)$/,
    test: (code) => /(^|[^.\w])(window\.)?(alert|confirm|prompt)\s*\(/m.test(code),
  },
  {
    id: 'no-v-html',
    why: 'all user-provided text must go through Vue interpolation (§11)',
    files: /\.vue$/,
    test: (code) => /\sv-html\s*=/.test(code),
  },
  {
    id: 'no-console-log',
    why: 'use console.warn/error for real problems; no debug logging',
    files: /\.(vue|ts)$/,
    test: (code) => /console\.log\s*\(/.test(code),
  },
  {
    id: 'api-client-only',
    why: "call the API through services/api (cookie + CSRF), not fetch() directly",
    files: /\.(vue|ts)$/,
    skip: (rel) => rel === 'src/services/http.ts',
    test: (code) => /(^|[^.\w])fetch\s*\(/m.test(code),
  },
]

const problems = []
for (const file of walk(join(root, 'src'))) {
  const rel = relative(root, file).split('\\').join('/')
  if (rel.endsWith('.test.ts')) continue
  const code = stripComments(readFileSync(file, 'utf8'))
  for (const rule of RULES) {
    if (!rule.files.test(rel) || rule.skip?.(rel)) continue
    if (rule.test(code)) problems.push(`${rel}: ${rule.id} — ${rule.why}`)
  }
}

if (problems.length > 0) {
  console.error(`lint: ${problems.length} problem(s)`)
  for (const p of problems) console.error(`  ${p}`)
  process.exit(1)
}
console.log('lint: ok')

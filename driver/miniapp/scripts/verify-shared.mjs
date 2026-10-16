#!/usr/bin/env node
// Shared with the other Mini App — keep both copies identical (make verify-shared).
//
// Verifies that the shared Mini App core is byte-identical in
// passenger/miniapp and driver/miniapp (ARCHITECTURE §2) and that every shared
// source file carries the "Shared with the other Mini App" header.
//
//   node passenger/miniapp/scripts/verify-shared.mjs          check (exit 1 on drift)
//   node passenger/miniapp/scripts/verify-shared.mjs --sync   copy passenger → driver

import { createHash } from 'node:crypto'
import { copyFileSync, existsSync, mkdirSync, readdirSync, readFileSync, statSync } from 'node:fs'
import { dirname, join, relative, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const repo = resolve(dirname(fileURLToPath(import.meta.url)), '../../..')
const A = join(repo, 'passenger/miniapp')
const B = join(repo, 'driver/miniapp')

/** Shared paths relative to each app root (directories are recursive). */
export const SHARED = [
  'src/telegram',
  'src/maps',
  'src/styles',
  'src/components/ui',
  'src/services/http.ts',
  'src/services/http.test.ts',
  'src/services/realtime.ts',
  'src/services/realtime.test.ts',
  'src/services/auth.ts',
  'src/services/config.ts',
  'src/services/config.test.ts',
  'src/utils',
  'src/i18n/core',
  'scripts/verify-shared.mjs',
  'scripts/lint.mjs',
]

const HEADER = 'Shared with the other Mini App — keep both copies identical (make verify-shared).'

function walk(root, rel) {
  const abs = join(root, rel)
  if (!existsSync(abs)) return []
  if (statSync(abs).isFile()) return [rel]
  return readdirSync(abs, { withFileTypes: true }).flatMap((e) => walk(root, join(rel, e.name)))
}

function digest(file) {
  return createHash('sha256').update(readFileSync(file)).digest('hex')
}

const sync = process.argv.includes('--sync')
const problems = []
const filesA = new Set(SHARED.flatMap((p) => walk(A, p)))
const filesB = new Set(SHARED.flatMap((p) => walk(B, p)))

for (const f of filesA) {
  if (!readFileSync(join(A, f), 'utf8').slice(0, 400).includes(HEADER)) problems.push(`missing shared header: passenger/miniapp/${f}`)
  if (sync) {
    mkdirSync(dirname(join(B, f)), { recursive: true })
    copyFileSync(join(A, f), join(B, f))
    continue
  }
  if (!filesB.has(f)) problems.push(`only in passenger/miniapp: ${f}`)
  else if (digest(join(A, f)) !== digest(join(B, f))) problems.push(`differs: ${f}`)
}
for (const f of filesB) if (!filesA.has(f)) problems.push(`only in driver/miniapp: ${f}`)

if (problems.length > 0) {
  console.error(`Shared Mini App core drifted (${problems.length}):`)
  for (const p of problems) console.error(`  ${p}`)
  console.error(`Fix the copies, or run: node ${relative(process.cwd(), fileURLToPath(import.meta.url))} --sync`)
  process.exit(1)
}
console.log(`${sync ? 'Synced' : 'Verified'} ${filesA.size} shared files between passenger/miniapp and driver/miniapp.`)

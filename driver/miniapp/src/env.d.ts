/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** '1' only in the presentation build (pnpm build:demo / dev:demo). */
  readonly VITE_DEMO?: string
}

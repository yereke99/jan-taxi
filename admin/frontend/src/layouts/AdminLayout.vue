<!--
  Authenticated shell: a floating translucent sidebar (glass with a solid
  fallback, §15.1) with sections filtered by permission and the operator's
  account, and a top bar with the page title, live-update state and language.
  The realtime connection lives as long as this layout.
-->
<script setup lang="ts">
import { KeyRound, LogOut } from '@lucide/vue'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { SelectInput } from '../components'
import { realtime, realtimeStatus } from '../composables/useRealtime'
import { isLocale, LOCALES, setLocale, type Locale } from '../i18n'
import { NAV_ITEMS, NAV_SECTIONS } from '../navigation'
import { useAuthStore } from '../stores/auth'
import ChangePasswordDialog from './ChangePasswordDialog.vue'

const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const signingOut = ref(false)
const passwordOpen = ref(false)

const sections = computed(() =>
  NAV_SECTIONS.map((section) => ({
    section,
    items: NAV_ITEMS.filter((i) => i.section === section && auth.can(i.perm)),
  })).filter((s) => s.items.length > 0),
)

const title = computed(() => (route.meta.titleKey ? t(route.meta.titleKey) : ''))
const localeOptions = computed(() => LOCALES.map((l) => ({ value: l, label: t(`languages.${l}`) })))
const currentLocale = computed(() => (isLocale(locale.value) ? locale.value : 'kk'))
const live = computed(() => realtimeStatus.value === 'open')
const initials = computed(() => {
  const name = auth.admin?.full_name || auth.admin?.email || ''
  return name
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((p) => [...p][0]?.toUpperCase() ?? '')
    .join('')
})

// List pages keep their instance across query changes (filters live in the
// URL); detail pages remount when the record id changes.
const viewKey = computed(() => `${String(route.name)}:${typeof route.params.id === 'string' ? route.params.id : ''}`)

function isActive(name: string): boolean {
  return route.name === name || route.meta.navParent === name
}

function onLocale(v: Locale | null): void {
  if (v) setLocale(v)
}

async function signOut(): Promise<void> {
  if (signingOut.value) return
  signingOut.value = true
  try {
    realtime.stop()
    await auth.logout()
    await router.replace({ name: 'login' })
  } finally {
    signingOut.value = false
  }
}

// Keep the browser tab title in the chosen language.
watch(title, (v) => {
  document.title = v ? `${v} — Jan Taxi` : 'Jan Taxi'
})

onMounted(() => realtime.start())
onBeforeUnmount(() => realtime.stop())
</script>

<template>
  <div class="layout">
    <aside class="sidebar jt-glass">
      <div class="brand">
        <span class="brand__mark" aria-hidden="true">J</span>
        <div class="brand__text">
          <span class="brand__name">{{ t('app.name') }}</span>
          <span class="brand__panel">{{ t('app.panel') }}</span>
        </div>
      </div>
      <nav class="nav" :aria-label="t('app.panel')">
        <div v-for="s in sections" :key="s.section" class="nav__section">
          <p class="nav__heading">{{ t(`nav.section_${s.section}`) }}</p>
          <RouterLink
            v-for="item in s.items"
            :key="item.name"
            :to="{ name: item.name }"
            class="nav__item"
            :class="{ 'is-active': isActive(item.name) }"
            :aria-current="isActive(item.name) ? 'page' : undefined"
          >
            <component :is="item.icon" :size="16" :stroke-width="1.75" aria-hidden="true" />
            <span>{{ t(item.titleKey) }}</span>
          </RouterLink>
        </div>
      </nav>
      <div v-if="auth.admin" class="account">
        <div class="account__who">
          <span class="account__avatar" aria-hidden="true">{{ initials }}</span>
          <div class="account__text">
            <span class="account__name">{{ auth.admin.full_name || auth.admin.email }}</span>
            <span class="account__role">{{ t(`roles.${auth.admin.role}`) }}</span>
          </div>
        </div>
        <div class="account__actions">
          <button type="button" class="account__btn" @click="passwordOpen = true">
            <KeyRound :size="14" aria-hidden="true" />{{ t('account.change_password') }}
          </button>
          <button type="button" class="account__btn" :disabled="signingOut" @click="signOut">
            <LogOut :size="14" aria-hidden="true" />{{ t('auth.sign_out') }}
          </button>
        </div>
      </div>
    </aside>

    <div class="main">
      <header class="topbar">
        <h1 class="topbar__title">{{ title }}</h1>
        <div class="topbar__right">
          <span class="live" :class="{ 'is-live': live }" role="status">
            <span class="live__dot" aria-hidden="true" />
            {{ live ? t('account.realtime_open') : t('account.realtime_offline') }}
          </span>
          <label class="topbar__lang">
            <span class="jt-visually-hidden">{{ t('common.language') }}</span>
            <SelectInput :model-value="currentLocale" :options="localeOptions" :aria-label="t('common.language')" @update:model-value="onLocale" />
          </label>
        </div>
      </header>
      <main class="content">
        <RouterView :key="viewKey" />
      </main>
    </div>
    <ChangePasswordDialog v-model:open="passwordOpen" />
  </div>
</template>

<style scoped>
.layout {
  display: grid;
  grid-template-columns: calc(var(--jt-sidebar-width) + 8px) 1fr;
  height: 100%;
  background: var(--jt-color-grouped-bg);
}

.sidebar {
  display: flex;
  flex-direction: column;
  min-height: 0;
  margin: 8px 0 8px 8px;
  border-radius: var(--jt-radius-card);
  overflow: hidden;
}

.brand {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: var(--jt-space-5) var(--jt-space-4) var(--jt-space-4);
}

.brand__mark {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border-radius: 10px;
  background: var(--jt-color-primary);
  color: var(--jt-color-on-primary);
  font: 700 17px/1 var(--jt-font-family);
}

.brand__text {
  display: flex;
  flex-direction: column;
}

.brand__name {
  font: 700 15px/20px var(--jt-font-family);
}

.brand__panel {
  font: var(--jt-font-caption);
  color: var(--jt-color-label-secondary);
}

.nav {
  flex: 1;
  overflow-y: auto;
  padding: 0 var(--jt-space-2) var(--jt-space-4);
}

.nav__section + .nav__section {
  margin-top: var(--jt-space-3);
}

.nav__heading {
  padding: 0 var(--jt-space-3) var(--jt-space-1);
  font: 600 11px/16px var(--jt-font-family);
  color: var(--jt-color-label-secondary);
  text-transform: uppercase;
  letter-spacing: 0.4px;
}

.nav__item {
  display: flex;
  align-items: center;
  gap: 10px;
  height: var(--jt-control-height);
  padding: 0 var(--jt-space-3);
  border-radius: var(--jt-radius-round);
  color: var(--jt-color-label);
  font: var(--jt-font-body);
  text-decoration: none;
}

.nav__item:hover {
  background: var(--jt-color-hover);
  text-decoration: none;
}

.nav__item.is-active {
  background: var(--jt-color-primary);
  color: var(--jt-color-on-primary);
  font-weight: 600;
}

.account {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
  padding: var(--jt-space-3) var(--jt-space-3) var(--jt-space-4);
  box-shadow: inset 0 var(--jt-hairline) 0 var(--jt-color-separator);
}

.account__who {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.account__avatar {
  display: inline-flex;
  flex: none;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border-radius: 50%;
  background: var(--jt-color-fill-tertiary);
  font: 600 12px/1 var(--jt-font-family);
}

.account__text {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.account__name {
  font: var(--jt-font-body-strong);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.account__role {
  font: var(--jt-font-caption);
  color: var(--jt-color-label-secondary);
}

.account__actions {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.account__btn {
  display: flex;
  align-items: center;
  gap: 8px;
  height: 28px;
  padding: 0 var(--jt-space-2);
  border-radius: var(--jt-radius-round);
  color: var(--jt-color-label-secondary);
  font: var(--jt-font-small);
  text-align: left;
}

.account__btn:hover:not(:disabled) {
  background: var(--jt-color-hover);
  color: var(--jt-color-label);
}

.main {
  display: flex;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
}

.topbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--jt-space-4);
  height: var(--jt-topbar-height);
  padding: 0 var(--jt-space-6);
}

.topbar__title {
  font: var(--jt-font-title);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.topbar__right {
  display: flex;
  align-items: center;
  gap: var(--jt-space-4);
}

.topbar__lang :deep(.select) {
  min-width: 128px;
}

.live {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font: var(--jt-font-caption);
  color: var(--jt-color-label-secondary);
}

.live__dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--jt-color-gray);
}

.live.is-live .live__dot {
  background: var(--jt-color-green);
}

.content {
  flex: 1;
  overflow-y: auto;
  padding: 0 var(--jt-space-6) var(--jt-space-8);
}
</style>

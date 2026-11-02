<!--
  Find a passenger by phone digits, Telegram id or invite code (at least 3
  characters) and open the passenger's marketing page. Results show the
  masked phone only.
-->
<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { promoUsersApi, type UserRef } from '../../api/marketing'
import { AppSpinner } from '../../components'
import { useErrors } from '../../composables/useErrors'
import { useFormat } from '../../composables/useFormat'
import { isAbortError } from '../../services/api'

const { t } = useI18n()
const fmt = useFormat()
const errors = useErrors()
const router = useRouter()

const q = ref('')
const items = ref<UserRef[]>([])
const loading = ref(false)
const searched = ref(false)
const error = ref('')
let timer: ReturnType<typeof setTimeout> | undefined
let ctrl: AbortController | null = null

const tooShort = computed(() => [...q.value.trim()].length < 3)

async function search(text: string): Promise<void> {
  ctrl?.abort()
  const mine = new AbortController()
  ctrl = mine
  loading.value = true
  error.value = ''
  try {
    const res = await promoUsersApi.find(text, mine.signal)
    if (ctrl !== mine) return
    items.value = res.items
    searched.value = true
  } catch (err) {
    if (isAbortError(err) || ctrl !== mine) return
    error.value = errors.text(err)
  } finally {
    if (ctrl === mine) loading.value = false
  }
}

watch(q, (v) => {
  clearTimeout(timer)
  if ([...v.trim()].length < 3) {
    ctrl?.abort()
    items.value = []
    searched.value = false
    loading.value = false
    return
  }
  timer = setTimeout(() => void search(v.trim()), 350)
})

function open(u: UserRef): void {
  void router.push({ name: 'marketing-user', params: { id: u.id } })
}

onBeforeUnmount(() => {
  clearTimeout(timer)
  ctrl?.abort()
})
</script>

<template>
  <div class="lookup">
    <label class="lookup__field">
      <span class="lookup__label">{{ t('marketing.find_user') }}</span>
      <input v-model="q" type="search" class="lookup__input" :placeholder="t('marketing.find_user_placeholder')" :aria-label="t('marketing.find_user')" autocomplete="off" />
    </label>
    <div v-if="!tooShort" class="lookup__results" role="listbox" :aria-busy="loading || undefined">
      <AppSpinner v-if="loading && !items.length" :size="16" />
      <p v-else-if="error" class="lookup__error" role="alert">{{ error }}</p>
      <p v-else-if="searched && !items.length" class="jt-muted lookup__empty">{{ t('marketing.find_user_empty') }}</p>
      <button v-for="u in items" :key="u.id" type="button" role="option" class="lookup__row" @click="open(u)">
        <span class="lookup__name">{{ u.name || t('marketing.user_short', { id: u.id.slice(0, 8) }) }}</span>
        <span class="jt-mono jt-muted">{{ u.phone_masked ?? '—' }}</span>
        <span class="jt-mono jt-muted">TG {{ u.telegram_user_id }}</span>
        <span class="jt-muted">{{ t('marketing.user_registered', { date: fmt.date(u.created_at) }) }}</span>
      </button>
    </div>
    <p v-else class="jt-muted lookup__hint">{{ t('marketing.find_user_hint') }}</p>
  </div>
</template>

<style scoped>
.lookup {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
  padding: var(--jt-space-4) var(--jt-space-5);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
  box-shadow:
    var(--jt-shadow-card),
    inset 0 0 0 var(--jt-hairline) var(--jt-color-border);
}

.lookup__field {
  display: flex;
  align-items: center;
  gap: var(--jt-space-3);
}

.lookup__label {
  font: var(--jt-font-headline);
  white-space: nowrap;
}

.lookup__input {
  flex: 1;
  max-width: 420px;
  height: var(--jt-control-height);
  padding: 0 12px;
  border: 0;
  border-radius: var(--jt-radius-round);
  background: var(--jt-color-bg);
  box-shadow: inset 0 0 0 1px var(--jt-color-border);
  font: var(--jt-font-body);
  outline: none;
}

.lookup__input:focus {
  box-shadow: inset 0 0 0 2px var(--jt-color-primary);
}

.lookup__results {
  display: flex;
  flex-direction: column;
}

.lookup__row {
  display: grid;
  grid-template-columns: minmax(0, 2fr) minmax(0, 1fr) minmax(0, 1fr) minmax(0, 1fr);
  gap: var(--jt-space-3);
  align-items: center;
  padding: 8px 10px;
  border-radius: var(--jt-radius-control);
  text-align: left;
}

.lookup__row:hover {
  background: var(--jt-color-hover);
}

.lookup__name {
  font-weight: 600;
}

.lookup__hint,
.lookup__empty {
  font: var(--jt-font-small);
}

.lookup__error {
  font: var(--jt-font-small);
  color: var(--jt-color-red-text);
}
</style>

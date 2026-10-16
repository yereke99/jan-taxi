<!--
  Notification templates per key × language: edit the Telegram HTML body,
  insert allowed {{placeholders}}, compare with the default text, save an
  override or revert to the default. The API validates placeholders and HTML.
-->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Language } from '../../api/common'
import { notificationsApi, type NotificationTemplate } from '../../api/system'
import { AppButton, StateView, Tabs, TextArea, confirmAction, toast } from '../../components'
import { useErrors } from '../../composables/useErrors'
import { useFormat } from '../../composables/useFormat'
import { useResource } from '../../composables/useResource'
import { isApiError } from '../../services/api'

const { t } = useI18n()
const fmt = useFormat()
const errors = useErrors()
const res = useResource((signal) => notificationsApi.templates(signal))
void res.reload()

const selectedKey = ref('')
const lang = ref<Language>('kk')
const body = ref('')
const error = ref('')
const saving = ref(false)
const textarea = ref<InstanceType<typeof TextArea> | null>(null)

const all = computed<NotificationTemplate[]>(() => res.data.value?.items ?? [])
const keys = computed(() => [...new Set(all.value.map((x) => x.key))])
const maxLength = computed(() => res.data.value?.max_body_length ?? 0)
const current = computed(() => all.value.find((x) => x.key === selectedKey.value && x.language === lang.value) ?? null)
const dirty = computed(() => !!current.value && body.value.trim() !== current.value.body.trim())
const langTabs = computed(() =>
  (['kk', 'ru', 'en'] as const).map((l) => {
    const item = all.value.find((x) => x.key === selectedKey.value && x.language === l)
    return { value: l, label: `${t(`languages.${l}`)}${item?.is_custom ? ' •' : ''}` }
  }),
)

watch(keys, (list) => {
  if (!selectedKey.value && list[0]) selectedKey.value = list[0]
})

watch(current, (c) => {
  body.value = c?.body ?? ''
  error.value = ''
})

async function choose(key: string, l: Language = lang.value): Promise<void> {
  if (dirty.value) {
    const ok = await confirmAction({ title: t('common.unsaved_changes'), confirmLabel: t('common.discard'), destructive: true })
    if (!ok) return
  }
  selectedKey.value = key
  lang.value = l
}

/** The placeholder as written in a template body. */
function token(name: string): string {
  return '{' + '{' + name + '}' + '}'
}

function insert(name: string): void {
  const tok = token(name)
  const el = (textarea.value?.$el as HTMLElement | undefined)?.querySelector('textarea')
  if (!el) {
    body.value += tok
    return
  }
  const start = el.selectionStart ?? body.value.length
  const end = el.selectionEnd ?? start
  body.value = body.value.slice(0, start) + tok + body.value.slice(end)
  requestAnimationFrame(() => {
    el.focus()
    el.setSelectionRange(start + tok.length, start + tok.length)
  })
}

function replaceItem(item: NotificationTemplate): void {
  const data = res.data.value
  if (!data) return
  res.set({ ...data, items: data.items.map((x) => (x.key === item.key && x.language === item.language ? item : x)) })
}

async function save(): Promise<void> {
  const c = current.value
  if (!c || saving.value) return
  saving.value = true
  error.value = ''
  try {
    replaceItem(await notificationsApi.saveTemplate(c.key, c.language, body.value))
    toast.success(t('system.template_saved'))
  } catch (err) {
    const reason = isApiError(err) && typeof err.details.reason === 'string' ? err.details.reason : ''
    error.value = [errors.fields(err).body ?? errors.text(err), reason].filter(Boolean).join(' — ')
  } finally {
    saving.value = false
  }
}

async function revert(): Promise<void> {
  const c = current.value
  if (!c) return
  const ok = await confirmAction({ title: t('system.revert_title'), message: t('system.revert_message'), confirmLabel: t('system.revert') })
  if (!ok) return
  try {
    replaceItem(await notificationsApi.revertTemplate(c.key, c.language))
    toast.success(t('system.template_reverted'))
  } catch (err) {
    errors.report(err)
  }
}

function keyLabel(key: string): string {
  return key.replace(/_/g, ' ')
}
</script>

<template>
  <StateView v-if="!res.data.value" :loading="res.loading.value" :error="res.error.value" @retry="res.reload()" />
  <div v-else class="tpl">
    <nav class="tpl__keys" :aria-label="t('system.templates')">
      <button
        v-for="k in keys"
        :key="k"
        type="button"
        class="tpl__key"
        :class="{ 'is-active': k === selectedKey }"
        :aria-current="k === selectedKey ? 'true' : undefined"
        @click="choose(k)"
      >
        <span class="jt-mono">{{ keyLabel(k) }}</span>
        <span v-if="all.some((x) => x.key === k && x.is_custom)" class="tpl__custom">{{ t('system.custom') }}</span>
      </button>
    </nav>

    <section v-if="current" class="tpl__editor">
      <header class="tpl__head">
        <div>
          <h3 class="jt-section-title jt-mono">{{ current.key }}</h3>
          <p class="jt-muted">
            {{ current.bots.map((b) => t(`bots.${b}`)).join(', ') }}
            <template v-if="current.button"> · {{ t('system.button', { label: current.button.label, param: current.button.start_param }) }}</template>
          </p>
        </div>
        <Tabs :model-value="lang" :tabs="langTabs" :aria-label="t('common.language')" @update:model-value="(l) => choose(selectedKey, l)" />
      </header>

      <div class="tpl__params">
        <p class="tpl__label">{{ t('system.placeholders') }}</p>
        <div class="tpl__chips">
          <button v-for="p in current.params" :key="p.name" type="button" class="chip" @click="insert(p.name)">
            <span class="jt-mono">{{ token(p.name) }}</span>
            <span v-if="p.optional" class="chip__opt">{{ t('common.optional') }}</span>
          </button>
        </div>
      </div>

      <TextArea ref="textarea" v-model="body" :rows="10" :max-length="maxLength" mono :invalid="Boolean(error)" :aria-label="t('system.body')" />
      <p v-if="error" class="tpl__error" role="alert">{{ error }}</p>
      <p class="jt-muted tpl__hint">{{ t('system.html_hint') }}</p>

      <div class="tpl__actions">
        <span class="jt-muted">
          {{ current.is_custom ? t('system.custom_since', { time: fmt.dateTime(current.updated_at), name: current.updated_by?.full_name ?? '—' }) : t('system.using_default') }}
        </span>
        <div class="jt-row">
          <AppButton v-if="current.is_custom" variant="destructive" @click="revert">{{ t('system.revert') }}</AppButton>
          <AppButton :disabled="!dirty" @click="body = current.body">{{ t('common.reset') }}</AppButton>
          <AppButton variant="primary" :disabled="!dirty" :loading="saving" @click="save">{{ t('common.save') }}</AppButton>
        </div>
      </div>

      <details class="tpl__default">
        <summary>{{ t('system.default_text') }}</summary>
        <pre>{{ current.default_body }}</pre>
      </details>
      <p v-if="current.override_body && current.override_body !== current.body" class="tpl__error">{{ t('system.override_invalid') }}</p>
    </section>
  </div>
</template>

<style scoped>
.tpl {
  display: grid;
  grid-template-columns: 280px minmax(0, 1fr);
  gap: var(--jt-space-4);
  align-items: start;
}

.tpl__keys {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: var(--jt-space-2);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
  box-shadow: inset 0 0 0 var(--jt-hairline) var(--jt-color-border);
}

.tpl__key {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
  padding: 6px 10px;
  border-radius: var(--jt-radius-control);
  font-size: 12px;
  text-align: left;
}

.tpl__key:hover {
  background: var(--jt-color-hover);
}

.tpl__key.is-active {
  background: var(--jt-color-primary);
  color: var(--jt-color-on-primary);
}

.tpl__custom {
  font: 600 10px/14px var(--jt-font-family);
  text-transform: uppercase;
  opacity: 0.7;
}

.tpl__editor {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-3);
  padding: var(--jt-space-5);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
  box-shadow: inset 0 0 0 var(--jt-hairline) var(--jt-color-border);
}

.tpl__head {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--jt-space-3);
}

.tpl__label {
  font: 500 13px/18px var(--jt-font-family);
}

.tpl__chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 6px;
}

.chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 26px;
  padding: 0 10px;
  border-radius: var(--jt-radius-round);
  background: var(--jt-color-fill-secondary);
  font-size: 12px;
}

.chip:hover {
  background: var(--jt-color-fill-tertiary);
}

.chip__opt {
  font: var(--jt-font-caption);
  color: var(--jt-color-label-secondary);
}

.tpl__hint {
  font: var(--jt-font-caption);
}

.tpl__actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--jt-space-3);
}

.tpl__error {
  padding: 8px 12px;
  border-radius: var(--jt-radius-control);
  background: var(--jt-color-red-tint);
  color: var(--jt-color-red-text);
  font: var(--jt-font-small);
}

.tpl__default summary {
  cursor: pointer;
  color: var(--jt-color-label-secondary);
}

.tpl__default pre {
  margin: var(--jt-space-2) 0 0;
  padding: 12px;
  border-radius: var(--jt-radius-inner);
  background: var(--jt-color-fill-secondary);
  font: 12px/18px var(--jt-font-mono);
  white-space: pre-wrap;
}
</style>

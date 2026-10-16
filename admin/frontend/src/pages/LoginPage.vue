<!-- Email + password sign-in (cookie session; CSRF token kept by the auth store). -->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { AppButton, FormField, SelectInput, TextInput } from '../components'
import { useErrors } from '../composables/useErrors'
import { isLocale, LOCALES, setLocale, type Locale } from '../i18n'
import { homeLocation, safeRedirect } from '../router'
import { isApiError } from '../services/api'
import { useAuthStore } from '../stores/auth'

const { t, locale } = useI18n()
const errors = useErrors()
const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const email = ref('')
const password = ref('')
const submitting = ref(false)
const formError = ref('')
const fieldErrors = ref<{ email?: string; password?: string }>({})

const notice = computed(() => (route.query.reason === 'expired' ? t('auth.session_expired') : ''))
const localeOptions = computed(() => LOCALES.map((l) => ({ value: l, label: t(`languages.${l}`) })))
const currentLocale = computed(() => (isLocale(locale.value) ? locale.value : 'kk'))

function validate(): boolean {
  const errs: { email?: string; password?: string } = {}
  if (!email.value.trim()) errs.email = t('auth.email_required')
  if (!password.value) errs.password = t('auth.password_required')
  fieldErrors.value = errs
  return !errs.email && !errs.password
}

async function submit(): Promise<void> {
  formError.value = ''
  if (submitting.value || !validate()) return
  submitting.value = true
  try {
    await auth.login(email.value, password.value)
    password.value = ''
    await router.replace(safeRedirect(route.query.redirect) ?? homeLocation(auth.can))
  } catch (err) {
    // A 401 here means wrong credentials, not an expired session; 423 carries locked_until.
    formError.value = isApiError(err) && err.status === 401 ? t('errors.invalid_credentials') : errors.text(err)
  } finally {
    submitting.value = false
  }
}

function onLocale(v: Locale | null): void {
  if (v) setLocale(v)
}
</script>

<template>
  <main class="login">
    <form class="card" novalidate @submit.prevent="submit">
      <div class="card__head">
        <p class="brand">{{ t('app.name') }}</p>
        <h1 class="title">{{ t('auth.title') }}</h1>
      </div>

      <p v-if="notice && !formError" class="notice" role="status">{{ notice }}</p>
      <p v-if="formError" class="error" role="alert">{{ formError }}</p>

      <FormField v-slot="f" :label="t('auth.email')" :error="fieldErrors.email" required>
        <TextInput
          :id="f.id"
          v-model="email"
          type="email"
          autocomplete="username"
          inputmode="email"
          :invalid="f.invalid"
          :aria-describedby="f.describedby"
        />
      </FormField>
      <FormField v-slot="f" :label="t('auth.password')" :error="fieldErrors.password" required>
        <TextInput
          :id="f.id"
          v-model="password"
          type="password"
          autocomplete="current-password"
          :invalid="f.invalid"
          :aria-describedby="f.describedby"
        />
      </FormField>

      <AppButton type="submit" variant="primary" size="lg" block :loading="submitting">{{ t('auth.sign_in') }}</AppButton>

      <label class="lang">
        <span class="jt-visually-hidden">{{ t('common.language') }}</span>
        <SelectInput :model-value="currentLocale" :options="localeOptions" :aria-label="t('common.language')" @update:model-value="onLocale" />
      </label>
    </form>
  </main>
</template>

<style scoped>
.login {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 100%;
  padding: var(--jt-space-6);
}

.card {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-4);
  width: 380px;
  max-width: 100%;
  padding: var(--jt-space-8) var(--jt-space-6) var(--jt-space-6);
  border-radius: var(--jt-radius-modal);
  background: var(--jt-color-bg);
  box-shadow:
    var(--jt-shadow-card),
    inset 0 0 0 var(--jt-hairline) var(--jt-color-border);
}

.card__head {
  margin-bottom: var(--jt-space-2);
}

.brand {
  font: 700 15px/20px var(--jt-font-family);
  color: var(--jt-color-label-secondary);
}

.title {
  margin-top: var(--jt-space-1);
  font: var(--jt-font-title);
}

.notice,
.error {
  padding: 10px 12px;
  border-radius: var(--jt-radius-control);
  font: var(--jt-font-small);
}

.notice {
  background: var(--jt-color-fill-secondary);
  color: var(--jt-color-label-secondary);
}

.error {
  background: var(--jt-color-red-tint);
  color: var(--jt-color-red-text);
}

.lang {
  align-self: center;
}
</style>

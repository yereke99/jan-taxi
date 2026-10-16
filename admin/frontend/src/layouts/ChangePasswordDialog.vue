<!-- Own password change (POST /admin/auth/password); other sessions are revoked by the API. -->
<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminsApi } from '../api/system'
import { AppButton, FormField, Modal, TextInput, toast } from '../components'
import { useErrors } from '../composables/useErrors'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ 'update:open': [open: boolean] }>()

const MIN_PASSWORD = 12
const { t } = useI18n()
const errors = useErrors()
const current = ref('')
const next = ref('')
const repeat = ref('')
const fieldErrors = ref<Record<string, string>>({})
const busy = ref(false)

watch(
  () => props.open,
  (open) => {
    if (!open) return
    current.value = next.value = repeat.value = ''
    fieldErrors.value = {}
  },
)

async function submit(): Promise<void> {
  const f: Record<string, string> = {}
  if (!current.value) f.current_password = errors.reason('required')
  if (!next.value) f.new_password = errors.reason('required')
  else if ([...next.value].length < MIN_PASSWORD) f.new_password = errors.reason('too_short')
  else if (next.value === current.value) f.new_password = errors.reason('same_as_current')
  if (next.value && repeat.value !== next.value) f.repeat = t('account.password_mismatch')
  fieldErrors.value = f
  if (Object.keys(f).length > 0 || busy.value) return
  busy.value = true
  try {
    await adminsApi.changeOwnPassword(current.value, next.value)
    toast.success(t('account.password_changed'))
    emit('update:open', false)
  } catch (err) {
    const api = errors.fields(err)
    if (api.current_password || api.new_password) fieldErrors.value = api
    else errors.report(err)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Modal :open="open" :title="t('account.change_password')" :persistent="busy" @update:open="(v) => emit('update:open', v)">
    <form class="jt-form" novalidate @submit.prevent="submit">
      <FormField v-slot="f" :label="t('account.current_password')" :error="fieldErrors.current_password" required>
        <TextInput :id="f.id" v-model="current" type="password" autocomplete="current-password" :invalid="f.invalid" />
      </FormField>
      <FormField v-slot="f" :label="t('account.new_password')" :error="fieldErrors.new_password" :hint="t('account.password_hint')" required>
        <TextInput :id="f.id" v-model="next" type="password" autocomplete="new-password" :invalid="f.invalid" />
      </FormField>
      <FormField v-slot="f" :label="t('account.repeat_password')" :error="fieldErrors.repeat" required>
        <TextInput :id="f.id" v-model="repeat" type="password" autocomplete="new-password" :invalid="f.invalid" />
      </FormField>
      <button type="submit" hidden />
    </form>
    <template #footer>
      <AppButton :disabled="busy" @click="emit('update:open', false)">{{ t('common.cancel') }}</AppButton>
      <AppButton variant="primary" :loading="busy" @click="submit">{{ t('common.save') }}</AppButton>
    </template>
  </Modal>
</template>

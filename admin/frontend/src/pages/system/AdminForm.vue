<!-- Create an administrator or edit role, name, active flag and password (admins.manage). -->
<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminsApi, type AdminUser } from '../../api/system'
import { AppButton, Drawer, FormField, SelectInput, TextInput, ToggleSwitch, toast } from '../../components'
import { useErrors } from '../../composables/useErrors'
import { ROLES, ROLE_PERMISSIONS, type Role } from '../../permissions'
import { useAuthStore } from '../../stores/auth'

const props = defineProps<{ open: boolean; admin: AdminUser | null }>()
const emit = defineEmits<{ 'update:open': [open: boolean]; saved: [admin: AdminUser] }>()

const MIN_PASSWORD = 12
const { t } = useI18n()
const errors = useErrors()
const auth = useAuthStore()

const form = reactive({ email: '', fullName: '', role: 'operator' as Role, active: true, password: '' })
const fieldErrors = ref<Record<string, string>>({})
const saving = ref(false)

watch(
  () => props.open,
  (open) => {
    if (!open) return
    const a = props.admin
    Object.assign(form, { email: a?.email ?? '', fullName: a?.full_name ?? '', role: a?.role ?? 'operator', active: a?.is_active ?? true, password: '' })
    fieldErrors.value = {}
  },
)

const isSelf = computed(() => props.admin?.id === auth.admin?.id)
const roleOptions = computed(() => ROLES.map((r) => ({ value: r, label: t(`roles.${r}`) })))
const rolePerms = computed(() => ROLE_PERMISSIONS[form.role].map((p) => t(`permissions.${p.replace('.', '_')}`)))

function validate(): Record<string, string> {
  const f: Record<string, string> = {}
  if (!props.admin && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(form.email.trim())) f.email = 'invalid'
  if (!form.fullName.trim()) f.full_name = 'required'
  else if ([...form.fullName.trim()].length > 120) f.full_name = 'too_long'
  if (!props.admin && !form.password) f.password = 'required'
  if (form.password && [...form.password].length < MIN_PASSWORD) f.password = 'too_short'
  return f
}

async function save(): Promise<void> {
  const f = validate()
  fieldErrors.value = errors.translate(f)
  if (Object.keys(f).length > 0 || saving.value) return
  saving.value = true
  try {
    let saved: AdminUser
    if (props.admin) {
      const body: { full_name?: string; role?: Role; is_active?: boolean; password?: string } = {}
      if (form.fullName.trim() !== props.admin.full_name) body.full_name = form.fullName.trim()
      if (!isSelf.value && form.role !== props.admin.role) body.role = form.role
      if (!isSelf.value && form.active !== props.admin.is_active) body.is_active = form.active
      if (form.password) body.password = form.password
      saved = Object.keys(body).length ? await adminsApi.update(props.admin.id, body) : props.admin
    } else {
      saved = await adminsApi.create({ email: form.email.trim(), full_name: form.fullName.trim(), role: form.role, password: form.password })
    }
    toast.success(t('system.admin_saved'))
    emit('saved', saved)
    emit('update:open', false)
  } catch (err) {
    fieldErrors.value = errors.fields(err)
    if (Object.keys(fieldErrors.value).length === 0) errors.report(err)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <Drawer :open="open" :title="admin ? t('system.admin_edit') : t('system.admin_new')" width="480px" @update:open="(v) => emit('update:open', v)">
    <form class="jt-form" novalidate autocomplete="off" @submit.prevent="save">
      <FormField v-slot="f" :label="t('auth.email')" :error="fieldErrors.email" required>
        <TextInput :id="f.id" v-model="form.email" type="email" :readonly="Boolean(admin)" :invalid="f.invalid" />
      </FormField>
      <FormField v-slot="f" :label="t('system.full_name')" :error="fieldErrors.full_name" required>
        <TextInput :id="f.id" v-model="form.fullName" :invalid="f.invalid" />
      </FormField>
      <FormField v-slot="f" :label="t('system.role')" :error="fieldErrors.role" :hint="isSelf ? t('system.self_role_hint') : ''">
        <SelectInput :id="f.id" :model-value="form.role" :options="roleOptions" :disabled="isSelf" @update:model-value="(v) => v && (form.role = v)" />
      </FormField>
      <ul class="perms">
        <li v-for="p in rolePerms" :key="p">{{ p }}</li>
      </ul>
      <ToggleSwitch v-if="admin" v-model="form.active" :disabled="isSelf" :label="t('system.admin_active')" />
      <FormField v-slot="f" :label="admin ? t('system.new_password') : t('system.initial_password')" :error="fieldErrors.password" :hint="admin ? t('system.password_reset_hint') : t('system.password_hint')" :required="!admin">
        <TextInput :id="f.id" v-model="form.password" type="password" autocomplete="new-password" :invalid="f.invalid" />
      </FormField>
      <button type="submit" hidden />
    </form>
    <template #footer>
      <AppButton @click="emit('update:open', false)">{{ t('common.cancel') }}</AppButton>
      <AppButton variant="primary" :loading="saving" @click="save">{{ admin ? t('common.save') : t('common.create') }}</AppButton>
    </template>
  </Drawer>
</template>

<style scoped>
.perms {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  margin-top: -8px;
}

.perms li {
  padding: 0 8px;
  border-radius: var(--jt-radius-round);
  background: var(--jt-color-fill-secondary);
  font: 500 11px/20px var(--jt-font-family);
  color: var(--jt-color-label-secondary);
}
</style>

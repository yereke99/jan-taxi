<!--
  Moderation and management actions for one driver, each behind its
  permission and the statuses the API accepts: approve / request corrections /
  reject (drivers.moderate), suspend / restore / change city (drivers.manage),
  company bonus (finance.manage).
-->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { driversApi, type AdminDriverDetail } from '../../api/drivers'
import { AppButton, FormField, Modal, ReasonDialog, SelectInput, TextArea, confirmAction, toast } from '../../components'
import { useCities } from '../../composables/useCities'
import { useErrors } from '../../composables/useErrors'
import { useFormat } from '../../composables/useFormat'
import { LIMITS, validateReason } from '../../forms/validation'
import { useAuthStore } from '../../stores/auth'
import BonusDialog from './BonusDialog.vue'
import CorrectionsDialog from './CorrectionsDialog.vue'

type DriverAction = 'approve' | 'corrections' | 'reject' | 'suspend' | 'restore' | 'city' | 'bonus'

const props = withDefaults(defineProps<{ driver: AdminDriverDetail; only?: 'moderation' | 'all' }>(), { only: 'all' })
const emit = defineEmits<{ updated: [driver: AdminDriverDetail]; changed: [] }>()

const { t } = useI18n()
const errors = useErrors()
const fmt = useFormat()
const auth = useAuthStore()
const { cities } = useCities()

const approveOpen = ref(false)
const approveComment = ref('')
const approveError = ref('')
const approving = ref(false)
const correctionsOpen = ref(false)
const rejectOpen = ref(false)
const suspendOpen = ref(false)
const cityOpen = ref(false)
const cityId = ref<string | null>(null)
const cityBusy = ref(false)
const cityError = ref('')
const bonusOpen = ref(false)

const status = computed(() => props.driver.status)
const available = computed<Record<DriverAction, boolean>>(() => ({
  approve: auth.can('drivers.moderate') && status.value === 'pending',
  corrections: auth.can('drivers.moderate') && status.value === 'pending',
  reject: auth.can('drivers.moderate') && (status.value === 'pending' || status.value === 'changes_requested'),
  suspend: props.only === 'all' && auth.can('drivers.manage') && status.value !== 'suspended',
  restore: props.only === 'all' && auth.can('drivers.manage') && status.value === 'suspended',
  city: props.only === 'all' && auth.can('drivers.manage'),
  bonus: props.only === 'all' && auth.can('finance.manage') && (status.value === 'approved' || status.value === 'suspended'),
}))

const bonusTarget = computed(() => ({
  id: props.driver.id,
  code: props.driver.code,
  first_name: props.driver.first_name,
  last_name: props.driver.last_name,
  phone: props.driver.phone,
  status: props.driver.status,
  balance: props.driver.wallet?.balance ?? null,
}))

const cityOptions = computed(() => cities.value.filter((c) => c.is_active).map((c) => ({ value: c.id, label: `${fmt.name(c.name)} · ${c.code}` })))

watch(approveOpen, (open) => {
  if (open) {
    approveComment.value = ''
    approveError.value = ''
  }
})

function updated(d: AdminDriverDetail, message: string): void {
  emit('updated', d)
  toast.success(message)
}

async function approve(): Promise<void> {
  const problem = approveComment.value.trim() ? validateReason(approveComment.value, LIMITS.decisionComment) : null
  approveError.value = problem ? errors.reason(problem) : ''
  if (problem || approving.value) return
  approving.value = true
  try {
    const d = await driversApi.approve(props.driver.id, approveComment.value.trim())
    approveOpen.value = false
    updated(d, t('drivers.approved_ok', { code: d.code }))
  } catch (err) {
    approveError.value = errors.text(err)
  } finally {
    approving.value = false
  }
}

async function reject(reason: string): Promise<void> {
  const d = await driversApi.reject(props.driver.id, reason, '')
  updated(d, t('drivers.rejected_ok', { code: d.code }))
}

async function suspend(reason: string): Promise<void> {
  const d = await driversApi.suspend(props.driver.id, reason)
  updated(d, t('drivers.suspended_ok', { code: d.code }))
}

async function restore(): Promise<void> {
  const ok = await confirmAction({
    title: t('drivers.restore_title', { code: props.driver.code }),
    message: t('drivers.restore_message'),
    confirmLabel: t('drivers.restore'),
  })
  if (!ok) return
  try {
    const d = await driversApi.restore(props.driver.id)
    updated(d, t('drivers.restored_ok', { code: d.code }))
  } catch (err) {
    errors.report(err)
  }
}

async function changeCity(): Promise<void> {
  if (!cityId.value || cityBusy.value) return
  cityBusy.value = true
  cityError.value = ''
  try {
    const d = await driversApi.changeCity(props.driver.id, cityId.value)
    cityOpen.value = false
    updated(d, t('drivers.city_changed'))
  } catch (err) {
    cityError.value = errors.text(err)
  } finally {
    cityBusy.value = false
  }
}

function run(action: DriverAction): void {
  if (!available.value[action]) return
  switch (action) {
    case 'approve':
      approveOpen.value = true
      break
    case 'corrections':
      correctionsOpen.value = true
      break
    case 'reject':
      rejectOpen.value = true
      break
    case 'suspend':
      suspendOpen.value = true
      break
    case 'restore':
      void restore()
      break
    case 'city':
      cityId.value = props.driver.city?.id ?? null
      cityError.value = ''
      cityOpen.value = true
      break
    case 'bonus':
      bonusOpen.value = true
      break
  }
}

/** True while any dialog of this component is open (keyboard shortcuts pause). */
const busyDialog = computed(() => approveOpen.value || correctionsOpen.value || rejectOpen.value || suspendOpen.value || cityOpen.value || bonusOpen.value)

defineExpose({ run, busyDialog })
</script>

<template>
  <div class="actions">
    <AppButton v-if="available.bonus" @click="run('bonus')">{{ t('bonus.grant') }}</AppButton>
    <AppButton v-if="available.city" @click="run('city')">{{ t('drivers.change_city') }}</AppButton>
    <AppButton v-if="available.restore" @click="run('restore')">{{ t('drivers.restore') }}</AppButton>
    <AppButton v-if="available.suspend" variant="destructive" @click="run('suspend')">{{ t('drivers.suspend') }}</AppButton>
    <AppButton v-if="available.reject" variant="destructive" @click="run('reject')">{{ t('drivers.reject') }}</AppButton>
    <AppButton v-if="available.corrections" @click="run('corrections')">{{ t('drivers.request_corrections') }}</AppButton>
    <AppButton v-if="available.approve" variant="primary" @click="run('approve')">{{ t('drivers.approve') }}</AppButton>

    <Modal v-model:open="approveOpen" :title="t('drivers.approve_title', { code: driver.code })" :persistent="approving">
      <div class="jt-form">
        <p class="jt-muted">{{ t('drivers.approve_message') }}</p>
        <p v-if="!driver.welcome_bonus" class="jt-muted">{{ t('bonus.approve_note') }}</p>
        <FormField v-slot="f" :label="t('drivers.approve_comment')" :error="approveError">
          <TextArea :id="f.id" v-model="approveComment" :rows="2" :max-length="LIMITS.decisionComment" :invalid="f.invalid" />
        </FormField>
      </div>
      <template #footer>
        <AppButton :disabled="approving" @click="approveOpen = false">{{ t('common.cancel') }}</AppButton>
        <AppButton variant="primary" :loading="approving" @click="approve">{{ t('drivers.approve') }}</AppButton>
      </template>
    </Modal>

    <BonusDialog v-if="available.bonus" v-model:open="bonusOpen" :driver="bonusTarget" @done="emit('changed')" />

    <CorrectionsDialog v-model:open="correctionsOpen" :driver="driver" @done="(d) => updated(d, t('drivers.corrections_sent', { code: d.code }))" />

    <ReasonDialog
      v-model:open="rejectOpen"
      :title="t('drivers.reject_title', { code: driver.code })"
      :message="t('drivers.reject_message')"
      :max-length="LIMITS.rejectReason"
      :confirm-label="t('drivers.reject')"
      destructive
      :submit="reject"
    />
    <ReasonDialog
      v-model:open="suspendOpen"
      :title="t('drivers.suspend_title', { code: driver.code })"
      :message="t('drivers.suspend_message')"
      :max-length="LIMITS.suspendReason"
      :confirm-label="t('drivers.suspend')"
      destructive
      :submit="suspend"
    />

    <Modal v-model:open="cityOpen" :title="t('drivers.change_city')" :persistent="cityBusy">
      <div class="jt-form">
        <p class="jt-muted">{{ t('drivers.change_city_message') }}</p>
        <FormField v-slot="f" :label="t('drivers.region')" :error="cityError">
          <SelectInput :id="f.id" v-model="cityId" :options="cityOptions" :invalid="f.invalid" />
        </FormField>
      </div>
      <template #footer>
        <AppButton :disabled="cityBusy" @click="cityOpen = false">{{ t('common.cancel') }}</AppButton>
        <AppButton variant="primary" :loading="cityBusy" :disabled="!cityId || cityId === driver.city?.id" @click="changeCity">{{ t('common.save') }}</AppButton>
      </template>
    </Modal>
  </div>
</template>

<style scoped>
.actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: var(--jt-space-2);
}
</style>

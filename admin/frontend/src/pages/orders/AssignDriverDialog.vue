<!--
  Assign or reassign an order: search approved drivers of the order's city by
  code, phone, plate or name, pick one, confirm. The API runs the same
  checks as driver acceptance (funds, active order, city); an offline driver
  is allowed and reported as a warning.
-->
<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { driversApi, type AdminDriverListItem } from '../../api/drivers'
import { ordersApi, type AdminOrder } from '../../api/orders'
import { AppButton, AppSpinner, Modal, TextInput } from '../../components'
import { useErrors } from '../../composables/useErrors'
import { useFormat } from '../../composables/useFormat'
import { isAbortError, isApiError } from '../../services/api'

const props = defineProps<{ open: boolean; order: AdminOrder }>()
const emit = defineEmits<{ 'update:open': [open: boolean]; assigned: [order: AdminOrder]; stale: [] }>()

const { t } = useI18n()
const fmt = useFormat()
const errors = useErrors()
const q = ref('')
const results = ref<AdminDriverListItem[]>([])
const searching = ref(false)
const selected = ref<AdminDriverListItem | null>(null)
const busy = ref(false)
const formError = ref('')
let timer: ReturnType<typeof setTimeout> | undefined
let ctrl: AbortController | null = null

const reassign = computed(() => props.order.driver !== null)

watch(
  () => props.open,
  (open) => {
    if (!open) return
    q.value = ''
    selected.value = null
    formError.value = ''
    void search()
  },
)

watch(q, () => {
  clearTimeout(timer)
  timer = setTimeout(() => void search(), 250)
})

async function search(): Promise<void> {
  ctrl?.abort()
  const mine = new AbortController()
  ctrl = mine
  searching.value = true
  try {
    const page = await driversApi.list({ status: 'approved', city_id: props.order.city.id, q: q.value, online: q.value ? '' : 'true' }, 1, 20, mine.signal)
    if (ctrl === mine) results.value = page.items.filter((d) => d.id !== props.order.driver?.id)
  } catch (err) {
    if (!isAbortError(err) && ctrl === mine) formError.value = errors.text(err)
  } finally {
    if (ctrl === mine) searching.value = false
  }
}

function lacksFunds(d: AdminDriverListItem): boolean {
  return d.wallet_available !== null && d.wallet_available < props.order.commission_amount
}

async function assign(): Promise<void> {
  const d = selected.value
  if (!d || busy.value) return
  busy.value = true
  formError.value = ''
  try {
    const updated = await ordersApi.assign(props.order.id, d.id)
    emit('assigned', updated)
    emit('update:open', false)
  } catch (err) {
    formError.value = errors.text(err)
    // A driver accepted (or the order moved on) meanwhile: show the current order.
    if (isApiError(err) && (err.code === 'order_already_taken' || err.code === 'invalid_transition')) emit('stale')
  } finally {
    busy.value = false
  }
}

onBeforeUnmount(() => {
  clearTimeout(timer)
  ctrl?.abort()
})
</script>

<template>
  <Modal :open="open" :title="reassign ? t('orders.reassign_title') : t('orders.assign_title')" width="640px" :persistent="busy" @update:open="(v) => emit('update:open', v)">
    <div class="assign">
      <p class="jt-muted">
        {{ t('orders.assign_hint', { city: fmt.name(order.city.name), commission: fmt.money(order.commission_amount) }) }}
      </p>
      <TextInput v-model="q" type="search" :placeholder="t('drivers.search_placeholder')" :aria-label="t('common.search')" />
      <div class="assign__list" role="listbox" :aria-label="t('orders.drivers_found')" :aria-busy="searching">
        <AppSpinner v-if="searching && results.length === 0" :size="18" />
        <p v-else-if="results.length === 0" class="jt-muted assign__empty">{{ q ? t('orders.no_drivers_found') : t('orders.no_drivers_online') }}</p>
        <button
          v-for="d in results"
          :key="d.id"
          type="button"
          role="option"
          class="assign__row"
          :class="{ 'is-selected': selected?.id === d.id }"
          :aria-selected="selected?.id === d.id"
          @click="selected = d"
        >
          <span class="jt-mono assign__code">{{ d.code }}</span>
          <span class="assign__name">
            {{ d.first_name }} {{ d.last_name }}
            <span class="jt-muted">{{ d.vehicle ? `${d.vehicle.make} ${d.vehicle.model} · ${d.vehicle.plate}` : '' }}</span>
          </span>
          <span class="assign__state" :class="d.is_online ? 'jt-positive' : 'jt-muted'">{{ d.is_online ? t('common.online') : t('common.offline') }}</span>
          <span class="jt-num" :class="{ 'jt-negative': lacksFunds(d) }">{{ fmt.money(d.wallet_available) }}</span>
        </button>
      </div>
      <p v-if="selected && !selected.is_online" class="assign__warn">{{ t('orders.assign_offline_warning') }}</p>
      <p v-if="selected && lacksFunds(selected)" class="assign__warn">{{ t('orders.assign_funds_warning') }}</p>
      <p v-if="reassign" class="jt-muted">{{ t('orders.reassign_note', { code: order.driver?.code ?? '' }) }}</p>
      <p v-if="formError" class="assign__error" role="alert">{{ formError }}</p>
    </div>
    <template #footer>
      <AppButton :disabled="busy" @click="emit('update:open', false)">{{ t('common.cancel') }}</AppButton>
      <AppButton variant="primary" :disabled="!selected" :loading="busy" @click="assign">
        {{ selected ? t('orders.assign_to', { code: selected.code }) : t('orders.assign') }}
      </AppButton>
    </template>
  </Modal>
</template>

<style scoped>
.assign {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-3);
}

.assign__list {
  display: flex;
  flex-direction: column;
  max-height: 320px;
  min-height: 120px;
  overflow-y: auto;
  border-radius: var(--jt-radius-inner);
  box-shadow: inset 0 0 0 1px var(--jt-color-border);
  padding: 4px;
}

.assign__empty {
  padding: var(--jt-space-4);
  text-align: center;
}

.assign__row {
  display: grid;
  grid-template-columns: 72px 1fr 90px 100px;
  align-items: center;
  gap: var(--jt-space-2);
  padding: 8px 10px;
  border-radius: 10px;
  text-align: left;
}

.assign__row:hover {
  background: var(--jt-color-hover);
}

.assign__row.is-selected {
  background: var(--jt-color-primary);
  color: var(--jt-color-on-primary);
}

.assign__row.is-selected .jt-muted,
.assign__row.is-selected .jt-positive,
.assign__row.is-selected .jt-negative {
  color: inherit;
  opacity: 0.8;
}

.assign__name {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.assign__state {
  font: var(--jt-font-small);
}

.assign__warn {
  padding: 8px 12px;
  border-radius: var(--jt-radius-control);
  background: var(--jt-color-orange-tint);
  color: var(--jt-color-orange-text);
  font: var(--jt-font-small);
}

.assign__error {
  padding: 8px 12px;
  border-radius: var(--jt-radius-control);
  background: var(--jt-color-red-tint);
  color: var(--jt-color-red-text);
  font: var(--jt-font-small);
}
</style>

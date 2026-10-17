<!--
  Contacts on an active trip: the passenger's phone (call or copy) and, for
  parcels, the recipient's phone. Shown only while the order is active (the
  API withholds them otherwise).
-->
<script setup lang="ts">
import { Copy, Phone } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { DriverOrderDetail } from '../../api/types'
import { copyText } from '../../composables/clipboard'
import { haptics } from '../../telegram'
import { formatKzPhone, telHref } from '../../utils/phone'
import { JListGroup, JListRow, toast } from '../ui'

const props = defineProps<{ order: DriverOrderDetail }>()

const { t } = useI18n()

const passenger = computed(() => props.order.passenger)
/** Copies the number: a fallback where a tel: link does not open the dialer. */
async function copy(phone: string | null | undefined): Promise<void> {
  if (!phone) return
  if (await copyText(phone)) {
    haptics.selection()
    toast.show(t('common.copied'))
  }
}
</script>

<template>
  <JListGroup v-if="passenger || order.recipient_phone" class="contacts">
    <JListRow
      v-if="passenger"
      :title="passenger.first_name || t('trip.passenger')"
      :subtitle="passenger.phone ? formatKzPhone(passenger.phone) : t('trip.no_contact')"
    >
      <template #accessory>
        <span class="contacts__actions">
          <button v-if="passenger.phone" type="button" class="contacts__btn" :aria-label="t('common.copy')" @click="copy(passenger.phone)">
            <Copy :size="18" :stroke-width="2.25" aria-hidden="true" />
          </button>
          <a v-if="passenger.phone" class="contacts__btn" :href="telHref(passenger.phone)" :aria-label="t('trip.call_passenger')">
            <Phone :size="20" :stroke-width="2.25" aria-hidden="true" />
          </a>
        </span>
      </template>
    </JListRow>
    <JListRow v-if="order.recipient_phone" :title="t('trip.recipient')" :subtitle="formatKzPhone(order.recipient_phone)">
      <template #accessory>
        <span class="contacts__actions">
          <button type="button" class="contacts__btn" :aria-label="t('common.copy')" @click="copy(order.recipient_phone)">
            <Copy :size="18" :stroke-width="2.25" aria-hidden="true" />
          </button>
          <a class="contacts__btn" :href="telHref(order.recipient_phone)" :aria-label="t('trip.call_recipient')">
            <Phone :size="20" :stroke-width="2.25" aria-hidden="true" />
          </a>
        </span>
      </template>
    </JListRow>
  </JListGroup>
</template>

<style scoped>
.contacts__actions {
  display: flex;
  gap: var(--jt-space-2);
}

.contacts__btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: var(--jt-tap-target);
  height: var(--jt-tap-target);
  border-radius: 50%;
  background: var(--jt-color-bg);
  color: var(--jt-color-label);
}

.contacts__btn:active {
  opacity: var(--jt-opacity-pressed);
}
</style>

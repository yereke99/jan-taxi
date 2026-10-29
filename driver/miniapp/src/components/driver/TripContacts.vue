<!--
  Contacts on the driver's own active order (§22.4/§22.5): the passenger's
  name, a call button with the phone when they shared it through Telegram,
  and buttons that open WhatsApp or their Telegram chat; for parcels also the
  recipient's phone. The API sends them only while the order is active.
-->
<script setup lang="ts">
import { Copy, MessageCircle, Phone } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { DriverOrderDetail } from '../../api/types'
import { copyText } from '../../composables/clipboard'
import { haptics, openExternal, openTelegram } from '../../telegram'
import { formatKzPhone, normalizeContactPhone, whatsAppHref } from '../../utils/phone'
import { JListGroup, JListRow, toast } from '../ui'

const props = defineProps<{ order: DriverOrderDetail }>()

const { t } = useI18n()

const canContact = computed(() => props.order.is_mine && ['accepted', 'driver_arriving', 'in_progress'].includes(props.order.status))
const passenger = computed(() => canContact.value ? props.order.passenger : null)
const passengerPhone = computed(() => passenger.value?.phone_verified && passenger.value.phone ? normalizeContactPhone(passenger.value.phone) : null)
const recipientPhone = computed(() => canContact.value && props.order.recipient_phone ? normalizeContactPhone(props.order.recipient_phone) : null)
const whatsapp = computed(() => passengerPhone.value ? whatsAppHref(passengerPhone.value) : null)
const tgUrl = computed(() => passenger.value?.telegram_url ?? null)
/** https://t.me links open inside Telegram; tg://user?id= links are followed as plain links. */
const tgIsWeb = computed(() => Boolean(tgUrl.value?.startsWith('https://')))

const subtitle = computed(() => {
  const p = passenger.value
  if (!p) return ''
  if (passengerPhone.value) return formatKzPhone(passengerPhone.value)
  if (p.phone) return t('trip.invalid_phone')
  return p.telegram_username ? `@${p.telegram_username}` : t('trip.phone_not_shared')
})

/** Copies the number: a fallback where a tel: link does not open the dialer. */
async function copy(phone: string | null | undefined): Promise<void> {
  if (!phone) return
  if (await copyText(phone)) {
    haptics.selection()
    toast.show(t('common.copied'))
  } else toast.error(t('trip.copy_failed'))
}

function openWhatsApp(e: MouseEvent): void {
  const url = whatsapp.value
  if (!url) return
  e.preventDefault()
  haptics.impact('light')
  if (!openExternal(url)) toast.error(t('trip.open_failed'))
}

function openChat(e: MouseEvent): void {
  const url = tgUrl.value
  if (!url || !tgIsWeb.value) return
  e.preventDefault()
  haptics.impact('light')
  if (!openTelegram(url)) toast.error(t('trip.open_failed'))
}
</script>

<template>
  <JListGroup v-if="passenger || recipientPhone" class="contacts" :header="t('trip.contacts')">
    <JListRow v-if="passenger" :title="passenger.first_name || t('trip.passenger')" :subtitle="subtitle">
      <template v-if="passengerPhone" #subtitle>
        <button type="button" class="contacts__number" data-sheet-no-drag :aria-label="t('trip.copy_passenger_phone')" @click.stop="copy(passengerPhone)">
          <span>{{ formatKzPhone(passengerPhone) }}</span>
          <Copy :size="13" :stroke-width="2" aria-hidden="true" />
        </button>
      </template>
      <template #accessory>
        <span class="contacts__actions" data-sheet-no-drag>
          <a
            v-if="tgUrl"
            class="contacts__btn"
            :href="tgUrl"
            target="_blank"
            rel="noopener noreferrer"
            :aria-label="t('trip.write_passenger')"
            @click.stop="openChat"
          >
            <MessageCircle :size="20" :stroke-width="2.25" aria-hidden="true" />
          </a>
          <a v-if="whatsapp" class="contacts__btn" :href="whatsapp" target="_blank" rel="noopener noreferrer" :aria-label="t('trip.whatsapp_passenger')" :title="t('trip.whatsapp_passenger')" @click.stop="openWhatsApp">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="M21 11.5a9 9 0 0 1-13.3 7.9L3 21l1.6-4.7A9 9 0 1 1 21 11.5Z" />
              <path d="m9 7 1.5 3-1 1a10 10 0 0 0 3.5 3.5l1-1L17 15c-1 3-7 0-9-4-1-2-1-3 1-4Z" />
            </svg>
          </a>
          <a v-if="passengerPhone" class="contacts__btn is-primary" :href="`tel:${passengerPhone}`" target="_blank" rel="noopener noreferrer" :aria-label="t('trip.call_passenger')" @click.stop>
            <Phone :size="20" :stroke-width="2.25" aria-hidden="true" />
          </a>
        </span>
      </template>
    </JListRow>
    <JListRow v-if="recipientPhone" :title="t('trip.recipient')" :subtitle="formatKzPhone(recipientPhone)">
      <template #accessory>
        <span class="contacts__actions" data-sheet-no-drag>
          <button type="button" class="contacts__btn" :aria-label="t('common.copy')" @click.stop="copy(recipientPhone)">
            <Copy :size="18" :stroke-width="2.25" aria-hidden="true" />
          </button>
          <a class="contacts__btn is-primary" :href="`tel:${recipientPhone}`" target="_blank" rel="noopener noreferrer" :aria-label="t('trip.call_recipient')" @click.stop>
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

.contacts :deep(.jt-row__trailing) {
  max-width: none;
}

.contacts__number {
  display: inline-flex;
  align-items: center;
  gap: var(--jt-space-1);
  min-height: 24px;
  color: inherit;
  text-align: left;
  font: inherit;
  user-select: text;
  -webkit-user-select: text;
}

.contacts__number svg {
  flex: none;
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

.contacts__btn.is-primary {
  background: var(--jt-color-primary);
  color: var(--jt-color-on-primary);
}

.contacts__btn:active {
  opacity: var(--jt-opacity-pressed);
}
</style>

<!--
  Contacts on the driver's own active order (§22.4/§22.5): the passenger's
  name and phone (tap to copy it), and when they shared the phone through
  Telegram a WhatsApp button and a call button, otherwise a button that opens
  their Telegram chat in the same place; for parcels also the recipient's
  phone. The API sends them only while the order is active. Call links open
  in a new window, the form every Telegram client hands to the phone app
  (see callPhone); the copyable number is the fallback where no call can start.
-->
<script setup lang="ts">
import { Copy, MessageCircle, Phone } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { DriverOrderDetail } from '../../api/types'
import { haptics, openExternal, openTelegram } from '../../telegram'
import { copyText } from '../../utils/clipboard'
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

/** The tel: link starts the call itself (never prevented); this is only the tap feedback. */
function onCall(): void {
  haptics.impact('light')
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
            v-if="whatsapp"
            class="contacts__btn"
            :href="whatsapp"
            target="_blank"
            rel="noopener noreferrer"
            :aria-label="t('trip.whatsapp_passenger')"
            :title="t('trip.whatsapp_passenger')"
            @click.stop="openWhatsApp"
          >
            <svg class="contacts__whatsapp" width="20" height="20" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" focusable="false">
              <path
                d="M17.47 14.38c-.3-.15-1.76-.87-2.03-.97-.27-.1-.47-.15-.67.15-.2.3-.77.97-.94 1.16-.17.2-.35.22-.64.07-.3-.15-1.26-.46-2.39-1.47-.88-.79-1.48-1.76-1.65-2.06-.17-.3-.02-.46.13-.6.13-.14.3-.35.44-.52.15-.18.2-.3.3-.5.1-.2.05-.37-.02-.52-.08-.15-.67-1.61-.92-2.2-.24-.58-.49-.5-.67-.51h-.57c-.2 0-.52.07-.79.37-.27.3-1.04 1.02-1.04 2.48s1.07 2.88 1.21 3.07c.15.2 2.1 3.2 5.08 4.49.71.31 1.26.49 1.7.63.71.22 1.36.19 1.87.12.57-.09 1.76-.72 2-1.41.25-.7.25-1.29.18-1.41-.08-.13-.27-.2-.57-.35zM12.05 21.79h-.01a9.87 9.87 0 0 1-5.03-1.38l-.36-.21-3.74.98 1-3.65-.24-.37a9.86 9.86 0 0 1-1.51-5.26c0-5.45 4.44-9.89 9.89-9.89a9.82 9.82 0 0 1 6.99 2.9 9.82 9.82 0 0 1 2.89 6.99c0 5.45-4.44 9.89-9.88 9.89zm8.41-18.3A11.81 11.81 0 0 0 12.05 0C5.5 0 .16 5.34.16 11.89c0 2.1.55 4.14 1.59 5.95L.06 24l6.3-1.65a11.88 11.88 0 0 0 5.68 1.45h.01c6.55 0 11.89-5.34 11.89-11.89a11.82 11.82 0 0 0-3.48-8.41z"
              />
            </svg>
          </a>
          <a
            v-else-if="tgUrl"
            class="contacts__btn"
            :href="tgUrl"
            target="_blank"
            rel="noopener noreferrer"
            :aria-label="t('trip.write_passenger')"
            @click.stop="openChat"
          >
            <MessageCircle :size="20" :stroke-width="2.25" aria-hidden="true" />
          </a>
          <a v-if="passengerPhone" class="contacts__btn is-primary" :href="`tel:${passengerPhone}`" target="_blank" rel="noopener noreferrer" :aria-label="t('trip.call_passenger')" @click.stop="onCall">
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
          <a class="contacts__btn is-primary" :href="`tel:${recipientPhone}`" target="_blank" rel="noopener noreferrer" :aria-label="t('trip.call_recipient')" @click.stop="onCall">
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

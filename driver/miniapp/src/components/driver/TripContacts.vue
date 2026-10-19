<!--
  Contacts on the driver's own active order (§22.4/§22.5): the passenger's
  name, a call button with the phone when they shared it through Telegram,
  and a button that opens their Telegram chat; for parcels also the
  recipient's phone. The API sends them only while the order is active.
-->
<script setup lang="ts">
import { Copy, MessageCircle, Phone } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { DriverOrderDetail } from '../../api/types'
import { copyText } from '../../composables/clipboard'
import { haptics, openTelegram } from '../../telegram'
import { formatKzPhone, telHref } from '../../utils/phone'
import { JListGroup, JListRow, toast } from '../ui'

const props = defineProps<{ order: DriverOrderDetail }>()

const { t } = useI18n()

const passenger = computed(() => props.order.passenger)
const tgUrl = computed(() => passenger.value?.telegram_url ?? null)
/** https://t.me links open inside Telegram; tg://user?id= links are followed as plain links. */
const tgIsWeb = computed(() => Boolean(tgUrl.value?.startsWith('https://')))

const subtitle = computed(() => {
  const p = passenger.value
  if (!p) return ''
  if (p.phone) return formatKzPhone(p.phone)
  return p.telegram_username ? `@${p.telegram_username}` : t('trip.phone_not_shared')
})

/** Copies the number: a fallback where a tel: link does not open the dialer. */
async function copy(phone: string | null | undefined): Promise<void> {
  if (!phone) return
  if (await copyText(phone)) {
    haptics.selection()
    toast.show(t('common.copied'))
  }
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
  <JListGroup v-if="passenger || order.recipient_phone" class="contacts" :header="t('trip.contacts')">
    <JListRow v-if="passenger" :title="passenger.first_name || t('trip.passenger')" :subtitle="subtitle">
      <template #accessory>
        <span class="contacts__actions">
          <a
            v-if="tgUrl"
            class="contacts__btn"
            :href="tgUrl"
            target="_blank"
            rel="noopener noreferrer"
            :aria-label="t('trip.write_passenger')"
            @click="openChat"
          >
            <MessageCircle :size="20" :stroke-width="2.25" aria-hidden="true" />
          </a>
          <a v-if="passenger.phone" class="contacts__btn is-primary" :href="telHref(passenger.phone)" :aria-label="t('trip.call_passenger')">
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
          <a class="contacts__btn is-primary" :href="telHref(order.recipient_phone)" :aria-label="t('trip.call_recipient')">
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

.contacts__btn.is-primary {
  background: var(--jt-color-primary);
  color: var(--jt-color-on-primary);
}

.contacts__btn:active {
  opacity: var(--jt-opacity-pressed);
}
</style>

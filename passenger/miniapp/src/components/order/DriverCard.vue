<!--
  Assigned driver (Uber-like): photo, name with the last-name initial, rating,
  the car and a large plate number. Call / message actions only while the
  API exposes them (the phone is shared only while the order is active).
-->
<script setup lang="ts">
import { MessageCircle, Phone, Star } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { OrderDriver, Vehicle } from '../../api/types'
import { openTelegram } from '../../telegram'
import { telHref } from '../../utils/phone'

const props = defineProps<{
  driver: OrderDriver
  vehicle: Vehicle | null
  photoUrl: string | null
  /** Show call/message actions (active order). */
  contact?: boolean
}>()

const { t } = useI18n()

const name = computed(() => [props.driver.first_name, props.driver.last_name].filter(Boolean).join(' '))
const initial = computed(() => props.driver.first_name.trim().charAt(0).toUpperCase() || '·')
const car = computed(() => {
  const v = props.vehicle
  if (!v) return ''
  const model = [v.make, v.model].filter(Boolean).join(' ')
  return v.color ? `${model}, ${v.color}` : model
})
const ratingText = computed(() => (props.driver.rating !== null ? props.driver.rating.toFixed(1) : null))

function message(): void {
  const u = props.driver.telegram_username
  if (u) openTelegram(`https://t.me/${encodeURIComponent(u.replace(/^@/, ''))}`)
}
</script>

<template>
  <section class="driver" :aria-label="name">
    <div class="driver__top">
      <div class="driver__photo">
        <img v-if="photoUrl" :src="photoUrl" :alt="t('order.driver_photo')" />
        <span v-else aria-hidden="true">{{ initial }}</span>
      </div>
      <div class="driver__who">
        <p class="driver__name">{{ name }}</p>
        <p class="driver__meta">
          <template v-if="ratingText">
            <Star :size="13" :stroke-width="0" fill="currentColor" aria-hidden="true" />
            <span class="jt-tabular">{{ ratingText }}</span>
            <span aria-hidden="true">·</span>
          </template>
          <span v-else>{{ t('order.rating_none') }} ·</span>
          <span>{{ t('order.trips', { n: driver.completed_trips }) }}</span>
        </p>
      </div>
      <div v-if="vehicle?.plate" class="driver__plate">
        <span class="jt-visually-hidden">{{ t('order.plate') }}</span>
        <span class="driver__plate-number jt-tabular">{{ vehicle.plate }}</span>
      </div>
    </div>
    <p v-if="car" class="driver__car">{{ car }}</p>

    <div v-if="contact && (driver.phone || driver.telegram_username)" class="driver__actions">
      <a v-if="driver.phone" class="driver__action" :href="telHref(driver.phone)">
        <Phone :size="18" :stroke-width="2" aria-hidden="true" />
        <span>{{ t('order.call_driver') }}</span>
      </a>
      <button v-if="driver.telegram_username" type="button" class="driver__action" @click="message">
        <MessageCircle :size="18" :stroke-width="2" aria-hidden="true" />
        <span>{{ t('order.message_driver') }}</span>
      </button>
    </div>
  </section>
</template>

<style scoped>
.driver {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
}

.driver__top {
  display: flex;
  align-items: center;
  gap: var(--jt-space-3);
}

.driver__photo {
  flex: none;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 56px;
  height: 56px;
  border-radius: 50%;
  overflow: hidden;
  background: var(--jt-color-fill-tertiary);
  font: var(--jt-font-title3);
}

.driver__photo img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.driver__who {
  flex: 1;
  min-width: 0;
}

.driver__name {
  font: var(--jt-font-headline);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.driver__meta {
  display: flex;
  align-items: center;
  gap: 4px;
  white-space: nowrap;
  overflow: hidden;
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

.driver__plate {
  flex: none;
  padding: 4px 10px;
  border: 1.5px solid var(--jt-color-label);
  border-radius: 8px;
  background: var(--jt-color-bg);
}

.driver__plate-number {
  font: 700 22px/28px var(--jt-font-family);
  letter-spacing: 0.5px;
}

.driver__car {
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}

.driver__actions {
  display: flex;
  gap: var(--jt-space-2);
  margin-top: var(--jt-space-1);
}

.driver__action {
  flex: 1;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: var(--jt-space-2);
  height: 44px;
  border-radius: var(--jt-radius-capsule);
  background: var(--jt-color-fill-secondary);
  color: var(--jt-color-label);
  font: var(--jt-font-headline);
}

.driver__action:active {
  opacity: var(--jt-opacity-pressed);
}
</style>

<!--
  Live trip, top of the sheet: driver name with rating, car colour and model,
  the plate badge, and the driver's avatar over a simple car silhouette
  (initials when there is no photo). Below it the three round actions:
  safety, share trip details and call (the only filled one).
-->
<script setup lang="ts">
import { Phone, Share2, ShieldCheck, Star } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { OrderDriver, Vehicle } from '../../api/types'
import { telHref } from '../../utils/phone'
import KzPlate from './KzPlate.vue'

const props = defineProps<{
  driver: OrderDriver
  vehicle: Vehicle | null
  photoUrl: string | null
}>()

const emit = defineEmits<{ safety: []; share: [] }>()

const { t, locale } = useI18n()

const name = computed(() => [props.driver.first_name, props.driver.last_name].filter(Boolean).join(' '))
const initials = computed(() =>
  [props.driver.first_name, props.driver.last_name]
    .map((s) => s.trim().charAt(0).toUpperCase())
    .filter(Boolean)
    .join('.')
    .concat('.'),
)
const rating = computed(() =>
  props.driver.rating !== null ? props.driver.rating.toLocaleString(locale.value === 'en' ? 'en' : 'ru', { minimumFractionDigits: 1, maximumFractionDigits: 1 }) : null,
)
const car = computed(() => {
  const v = props.vehicle
  if (!v) return ''
  return [v.color, v.make, v.model].filter(Boolean).join(' ')
})
</script>

<template>
  <section class="ride-driver">
    <div class="ride-driver__text">
      <p class="ride-driver__name">
        <span>{{ name }}</span>
        <template v-if="rating">
          <Star :size="17" fill="currentColor" :stroke-width="0" aria-hidden="true" />
          <span class="jt-tabular">{{ rating }}</span>
        </template>
        <span v-else class="ride-driver__new">{{ t('order.rating_none') }}</span>
      </p>
      <p v-if="car" class="ride-driver__car">{{ car }}</p>
      <KzPlate v-if="vehicle?.plate" :plate="vehicle.plate" size="small" class="ride-driver__plate" />
    </div>
    <div class="ride-driver__visual" aria-hidden="true">
      <svg class="ride-driver__car-art" viewBox="0 0 132 56">
        <path
          d="M6 40c0-5 3-8 8-9l18-4 16-12c4-3 8-4 13-4h30c5 0 9 2 12 5l11 11 8 2c5 1 8 5 8 10v4c0 2-1 3-3 3h-7a12 12 0 0 0-23 0H42a12 12 0 0 0-23 0h-10c-2 0-3-1-3-3z"
          fill="#e5e5ea"
        />
        <path d="M52 17c3-2 6-3 10-3h12v13H38zm26-3h13c3 0 6 1 8 3l9 10H78z" fill="#c7c7cc" />
        <circle cx="30.5" cy="47" r="8.5" fill="#3a3a3c" />
        <circle cx="30.5" cy="47" r="3.5" fill="#d1d1d6" />
        <circle cx="102.5" cy="47" r="8.5" fill="#3a3a3c" />
        <circle cx="102.5" cy="47" r="3.5" fill="#d1d1d6" />
      </svg>
      <span class="ride-driver__avatar">
        <img v-if="photoUrl" :src="photoUrl" :alt="t('order.driver_photo')" />
        <span v-else>{{ initials }}</span>
      </span>
    </div>
  </section>

  <nav class="ride-actions">
    <button type="button" class="ride-action" @click="emit('safety')">
      <span class="ride-action__circle"><ShieldCheck :size="26" :stroke-width="1.8" aria-hidden="true" /></span>
      <span class="ride-action__label">{{ t('order.safety') }}</span>
    </button>
    <button type="button" class="ride-action" @click="emit('share')">
      <span class="ride-action__circle"><Share2 :size="24" :stroke-width="1.8" aria-hidden="true" /></span>
      <span class="ride-action__label">{{ t('order.share') }}</span>
    </button>
    <a v-if="driver.phone" class="ride-action" :href="telHref(driver.phone)">
      <span class="ride-action__circle is-primary"><Phone :size="24" :stroke-width="2" aria-hidden="true" /></span>
      <span class="ride-action__label">{{ t('order.call_driver') }}</span>
    </a>
  </nav>
</template>

<style scoped>
.ride-driver {
  display: flex;
  align-items: center;
  gap: var(--jt-space-3);
}

.ride-driver__text {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 2px;
}

.ride-driver__name {
  display: flex;
  align-items: center;
  gap: 5px;
  font: var(--jt-font-headline);
  font-size: 19px;
}

.ride-driver__new {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

.ride-driver__car {
  font: var(--jt-font-body);
  color: var(--jt-color-label);
}

.ride-driver__plate {
  margin-top: 6px;
}

.ride-driver__visual {
  position: relative;
  flex: none;
  width: 132px;
  height: 76px;
}

.ride-driver__car-art {
  position: absolute;
  left: 0;
  bottom: 0;
  width: 112px;
  height: auto;
}

.ride-driver__avatar {
  position: absolute;
  right: 0;
  top: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 60px;
  height: 60px;
  border-radius: 50%;
  border: 3px solid var(--jt-color-bg);
  background: var(--jt-color-fill-tertiary);
  overflow: hidden;
  font: 600 19px/1 var(--jt-font-family);
  color: var(--jt-color-label-secondary);
}

.ride-driver__avatar img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.ride-actions {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 4px;
  margin-top: var(--jt-space-4);
}

.ride-action {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 6px;
  color: var(--jt-color-label);
  text-decoration: none;
  text-align: center;
}

.ride-action__circle {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 56px;
  height: 56px;
  border-radius: 50%;
  background: var(--jt-color-fill-secondary);
  transition: transform var(--jt-duration-spring) var(--jt-ease-spring);
}

.ride-action__circle.is-primary {
  background: var(--jt-color-primary);
  color: var(--jt-color-on-primary);
}

.ride-action:active .ride-action__circle {
  transform: scale(0.94);
}

.ride-action__label {
  font: var(--jt-font-footnote);
  font-weight: 500;
  letter-spacing: -0.1px;
}
</style>

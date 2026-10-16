<!-- Quote line under the route: distance · duration and the starting fare, or the preview state. -->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { JSpinner } from '../ui'
import { useBookingStore } from '../../stores/booking'
import { useSessionStore } from '../../stores/session'
import { formatDistance, formatDuration, formatMoney } from '../../utils/format'

defineProps<{ error?: string }>()

const { t } = useI18n()
const booking = useBookingStore()
const session = useSessionStore()

const q = computed(() => (booking.quoteCurrent ? booking.quote : null))
</script>

<template>
  <div class="quote" aria-live="polite">
    <p v-if="error" class="quote__error" role="alert">{{ error }}</p>
    <div v-else-if="booking.previewStatus === 'loading'" class="quote__row is-muted">
      <JSpinner />
      <span>{{ t('booking.calculating') }}</span>
    </div>
    <div v-else-if="q" class="quote__row">
      <span class="quote__trip jt-tabular">
        {{ t('booking.summary', { distance: formatDistance(q.distance_m, session.language), duration: formatDuration(q.duration_s, session.language) }) }}
      </span>
      <span class="quote__fare jt-tabular">{{ t('booking.fare_from', { fare: formatMoney(q.fare.recommended, session.language) }) }}</span>
    </div>
    <p v-else-if="booking.blockedStops.length === 0" class="quote__hint">{{ t('booking.fill_route') }}</p>
  </div>
</template>

<style scoped>
.quote {
  min-height: 24px;
}

.quote__row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--jt-space-3);
  font: var(--jt-font-callout);
}

.quote__row.is-muted {
  justify-content: flex-start;
  color: var(--jt-color-label-secondary);
}

.quote__trip {
  color: var(--jt-color-label-secondary);
}

.quote__fare {
  font: var(--jt-font-headline);
}

.quote__hint {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

.quote__error {
  font: var(--jt-font-footnote);
  color: var(--jt-color-red-text);
}
</style>

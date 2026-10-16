<!-- Status pill for orders, moderation, top-ups, disputes and notifications. -->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { statusTone, type StatusKind } from '../utils/status'

const props = defineProps<{ kind: StatusKind; status: string }>()

const { t, te } = useI18n()
const tone = computed(() => statusTone(props.kind, props.status))
const label = computed(() => {
  const key = `${props.kind}_status.${props.status}`
  return te(key, 'kk') ? t(key) : props.status
})
</script>

<template>
  <span class="badge" :class="`badge--${tone}`">{{ label }}</span>
</template>

<style scoped>
.badge {
  display: inline-flex;
  align-items: center;
  height: 22px;
  padding: 0 8px;
  border-radius: var(--jt-radius-round);
  font: 600 12px/16px var(--jt-font-family);
  white-space: nowrap;
}

.badge--neutral {
  background: var(--jt-color-fill-secondary);
  color: var(--jt-color-label-secondary);
}

.badge--dark {
  background: var(--jt-color-primary);
  color: var(--jt-color-on-primary);
}

.badge--success {
  background: var(--jt-color-green-tint);
  color: var(--jt-color-green-text);
}

.badge--danger {
  background: var(--jt-color-red-tint);
  color: var(--jt-color-red-text);
}

.badge--info {
  background: var(--jt-color-blue-tint);
  color: var(--jt-color-blue-text);
}

.badge--warning {
  background: var(--jt-color-orange-tint);
  color: var(--jt-color-orange-text);
}
</style>

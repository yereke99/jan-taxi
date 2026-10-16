<!--
  The moderator's remarks for the given registration fields: field name,
  localized reason and the optional comment (shown as plain text).
-->
<script setup lang="ts">
import { CircleAlert } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { CorrectionItem, RegistrationField } from '../../../api/types'

const props = defineProps<{ items: readonly CorrectionItem[]; fields?: readonly RegistrationField[] }>()

const { t } = useI18n()

const shown = computed(() => (props.fields ? props.items.filter((c) => props.fields!.includes(c.field)) : [...props.items]))
</script>

<template>
  <section v-if="shown.length > 0" class="corr" role="note">
    <div v-for="(c, i) in shown" :key="`${c.field}-${i}`" class="corr__item">
      <CircleAlert :size="18" :stroke-width="2.25" class="corr__icon" aria-hidden="true" />
      <div class="corr__text">
        <p class="corr__field">{{ t(`registration.field.${c.field}`) }}</p>
        <p class="corr__reason">{{ t(`registration.reason.${c.reason}`) }}</p>
        <p v-if="c.comment" class="corr__comment">«{{ c.comment }}»</p>
      </div>
    </div>
  </section>
</template>

<style scoped>
.corr {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-3);
  margin: 0 var(--jt-side-margin) var(--jt-space-5);
  padding: var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-red-tint);
}

.corr__item {
  display: flex;
  gap: var(--jt-space-3);
}

.corr__icon {
  flex: none;
  margin-top: 1px;
  color: var(--jt-color-red);
}

.corr__field {
  font: var(--jt-font-headline);
}

.corr__reason {
  font: var(--jt-font-subheadline);
}

.corr__comment {
  margin-top: 2px;
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
  overflow-wrap: anywhere;
}
</style>

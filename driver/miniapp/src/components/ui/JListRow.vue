<!-- Shared with the other Mini App — keep both copies identical (make verify-shared). -->
<!--
  Row for JListGroup. Renders a RouterLink when `to` is set, a button when
  `clickable`, otherwise a static row. Slots: icon (leading 29px tile),
  default (replaces title), subtitle, accessory (replaces value + chevron,
  e.g. a JSwitch).
-->
<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, type RouteLocationRaw } from 'vue-router'
import { ChevronRight } from '@lucide/vue'

const props = withDefaults(
  defineProps<{
    title?: string
    subtitle?: string
    value?: string
    to?: RouteLocationRaw
    clickable?: boolean
    chevron?: boolean
    destructive?: boolean
    disabled?: boolean
  }>(),
  { title: '', subtitle: '', value: '', to: undefined, clickable: false, chevron: undefined, destructive: false, disabled: false },
)

const emit = defineEmits<{ click: [event: MouseEvent] }>()

const tag = computed(() => (props.to && !props.disabled ? RouterLink : props.clickable ? 'button' : 'div'))
const interactive = computed(() => !props.disabled && (props.to !== undefined || props.clickable))
const showChevron = computed(() => props.chevron ?? props.to !== undefined)

function onClick(e: MouseEvent): void {
  if (!props.disabled) emit('click', e)
}
</script>

<template>
  <component
    :is="tag"
    :to="tag === RouterLink ? to : undefined"
    :type="tag === 'button' ? 'button' : undefined"
    :disabled="tag === 'button' ? disabled : undefined"
    class="jt-row"
    :class="{ 'is-interactive': interactive, 'is-destructive': destructive, 'is-disabled': disabled, 'has-icon': $slots.icon }"
    role="listitem"
    @click="onClick"
  >
    <span v-if="$slots.icon" class="jt-row__icon"><slot name="icon" /></span>
    <span class="jt-row__main">
      <span class="jt-row__text">
        <span class="jt-row__title"><slot>{{ title }}</slot></span>
        <span v-if="subtitle || $slots.subtitle" class="jt-row__subtitle">
          <slot name="subtitle">{{ subtitle }}</slot>
        </span>
      </span>
      <span class="jt-row__trailing">
        <slot name="accessory">
          <span v-if="value" class="jt-row__value">{{ value }}</span>
          <ChevronRight v-if="showChevron" class="jt-row__chevron" :size="20" :stroke-width="2.25" aria-hidden="true" />
        </slot>
      </span>
    </span>
  </component>
</template>

<style scoped>
.jt-row {
  position: relative;
  display: flex;
  align-items: center;
  width: 100%;
  min-height: var(--jt-row-min-height);
  padding-left: var(--jt-space-4);
  background: var(--jt-color-bg);
  color: var(--jt-color-label);
  text-align: left;
  font: var(--jt-font-body);
  text-decoration: none;
}

.jt-row.is-interactive {
  cursor: pointer;
  user-select: none;
  -webkit-user-select: none;
}

.jt-row.is-interactive:active {
  background: var(--jt-color-fill-tertiary);
}

.jt-row.is-disabled {
  opacity: var(--jt-opacity-disabled);
}

.jt-row.is-destructive .jt-row__title {
  color: var(--jt-color-red);
}

.jt-row__icon {
  flex: none;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 29px;
  height: 29px;
  margin-right: var(--jt-space-4);
  border-radius: 7px;
  color: var(--jt-color-label);
}

.jt-row__main {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: var(--jt-space-2);
  align-self: stretch;
  padding: 11px var(--jt-space-4) 11px 0;
}

/* Hairline separator inset to the text column (iOS). */
.jt-row:not(:last-child) .jt-row__main {
  box-shadow: inset 0 calc(-1 * var(--jt-hairline)) 0 var(--jt-color-separator);
}

.jt-row__text {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.jt-row__title {
  overflow-wrap: anywhere;
}

.jt-row__subtitle {
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
  overflow-wrap: anywhere;
}

.jt-row__trailing {
  flex: none;
  display: flex;
  align-items: center;
  gap: var(--jt-space-1);
  max-width: 55%;
}

.jt-row__value {
  color: var(--jt-color-label-secondary);
  text-align: right;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.jt-row__chevron {
  color: var(--jt-color-label-tertiary);
  margin-right: -4px;
}
</style>

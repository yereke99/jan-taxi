<!--
  White content card (radius 22, §15.1). `flush` removes the body padding for
  tables and maps; the header keeps the title, a subtitle and actions.
-->
<script setup lang="ts">
withDefaults(defineProps<{ title?: string; subtitle?: string; flush?: boolean; tag?: string }>(), {
  title: '',
  subtitle: '',
  flush: false,
  tag: 'section',
})
</script>

<template>
  <component :is="tag" class="card" :class="{ 'card--flush': flush }">
    <header v-if="title || $slots.title || $slots.actions" class="card__head">
      <div class="card__titles">
        <h2 v-if="title || $slots.title" class="card__title">
          <slot name="title">{{ title }}</slot>
        </h2>
        <p v-if="subtitle" class="card__subtitle">{{ subtitle }}</p>
      </div>
      <div v-if="$slots.actions" class="card__actions"><slot name="actions" /></div>
    </header>
    <div class="card__body"><slot /></div>
    <footer v-if="$slots.footer" class="card__foot"><slot name="footer" /></footer>
  </component>
</template>

<style scoped>
.card {
  display: flex;
  flex-direction: column;
  min-width: 0;
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
  box-shadow:
    var(--jt-shadow-card),
    inset 0 0 0 var(--jt-hairline) var(--jt-color-border);
}

.card__head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--jt-space-3);
  padding: var(--jt-space-4) var(--jt-space-5) 0;
}

.card--flush .card__head {
  padding-bottom: var(--jt-space-3);
}

.card__titles {
  min-width: 0;
}

.card__title {
  font: var(--jt-font-title3);
}

.card__subtitle {
  margin-top: 2px;
  font: var(--jt-font-small);
  color: var(--jt-color-label-secondary);
}

.card__actions {
  display: flex;
  align-items: center;
  gap: var(--jt-space-2);
  flex: none;
}

.card__body {
  padding: var(--jt-space-4) var(--jt-space-5) var(--jt-space-5);
  min-width: 0;
}

.card--flush .card__body {
  padding: 0;
  overflow: hidden;
  border-radius: 0 0 var(--jt-radius-card) var(--jt-radius-card);
}

.card--flush .card__body:first-child {
  border-radius: var(--jt-radius-card);
}

.card--flush :deep(.table-wrap) {
  border-radius: 0;
  box-shadow: inset 0 var(--jt-hairline) 0 var(--jt-color-separator);
}

.card__foot {
  display: flex;
  justify-content: flex-end;
  gap: var(--jt-space-2);
  padding: var(--jt-space-3) var(--jt-space-5);
  box-shadow: inset 0 var(--jt-hairline) 0 var(--jt-color-separator);
}
</style>

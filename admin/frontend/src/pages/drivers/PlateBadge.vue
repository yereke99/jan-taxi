<!-- The declared licence plate, large, for comparison with the plate photo. -->
<script setup lang="ts">
defineProps<{ plate: string; size?: 'md' | 'lg' }>()
</script>

<template>
  <span class="plate" :class="`plate--${size ?? 'lg'}`" :aria-label="plate">
    <span class="plate__country" aria-hidden="true">KZ</span>
    <span class="plate__text">{{ plate || '—' }}</span>
  </span>
</template>

<style scoped>
.plate {
  display: inline-flex;
  align-items: stretch;
  overflow: hidden;
  border: 2px solid #000;
  border-radius: 8px;
  background: #fff;
  color: #000;
}

.plate__country {
  display: flex;
  align-items: flex-end;
  padding: 0 6px 4px;
  border-right: 2px solid #000;
  font: 700 11px/1 var(--jt-font-family);
}

.plate__text {
  padding: 6px 14px;
  font-family: var(--jt-font-mono);
  font-weight: 700;
  letter-spacing: 2px;
  white-space: nowrap;
}

.plate--lg .plate__text {
  font-size: 30px;
  line-height: 36px;
}

.plate--md .plate__text {
  font-size: 18px;
  line-height: 22px;
}
</style>

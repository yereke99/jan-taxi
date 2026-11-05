<!-- Shared with the other Mini App — keep both copies identical (make verify-shared). -->
<script setup lang="ts">
import { ArrowUpRight, Gift } from '@lucide/vue'

defineProps<{ label: string; value: string }>()
defineEmits<{ click: [] }>()
</script>

<template>
  <button type="button" class="jt-promo-gift" :aria-label="`${label} · ${value}`" @click="$emit('click')">
    <span class="jt-promo-gift__art" aria-hidden="true"><Gift :size="25" :stroke-width="1.8" /></span>
    <span class="jt-promo-gift__copy">
      <span class="jt-promo-gift__label">{{ label }}</span>
      <span class="jt-promo-gift__value">{{ value }}</span>
    </span>
    <ArrowUpRight class="jt-promo-gift__arrow" :size="15" :stroke-width="2" aria-hidden="true" />
  </button>
</template>

<style scoped>
.jt-promo-gift {
  display: inline-flex;
  align-items: center;
  gap: 9px;
  max-width: 100%;
  min-height: 52px;
  padding: 6px 12px 6px 7px;
  border: 1px solid var(--jt-color-bg);
  border-radius: var(--jt-radius-capsule);
  background: linear-gradient(120deg, var(--jt-color-bg), var(--jt-color-gift-bg));
  color: var(--jt-color-label);
  box-shadow: var(--jt-shadow-gift);
  cursor: pointer;
  -webkit-tap-highlight-color: transparent;
  transition: box-shadow 180ms ease, opacity 180ms ease;
}
.jt-promo-gift:active { opacity: var(--jt-opacity-pressed); }
.jt-promo-gift:focus-visible { outline: 2px solid var(--jt-color-gift-ink); outline-offset: 4px; }
.jt-promo-gift__art {
  display: grid;
  place-items: center;
  flex: none;
  width: 38px;
  height: 38px;
  border-radius: 50%;
  color: var(--jt-color-gift-ink);
  background: linear-gradient(150deg, var(--jt-color-gift-bg), var(--jt-color-gift-accent));
}
.jt-promo-gift__art svg { transform-origin: 50% 75%; animation: gift-sway 5.5s ease-in-out infinite; }
.jt-promo-gift__copy { display: flex; flex-direction: column; min-width: 0; text-align: left; }
.jt-promo-gift__label { font: 500 11px/15px var(--jt-font-family); color: var(--jt-color-gift-ink); }
.jt-promo-gift__value { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font: 700 15px/20px var(--jt-font-family); }
.jt-promo-gift__arrow { flex: none; color: var(--jt-color-gift-ink); }
@keyframes gift-sway { 0%, 65%, 100% { transform: rotate(0); } 72% { transform: translateY(-2px) rotate(-9deg); } 80% { transform: rotate(7deg); } 88% { transform: rotate(-4deg); } }
@media (prefers-reduced-motion: reduce) { .jt-promo-gift__art svg { animation: none; } .jt-promo-gift { transition: none; } }
</style>

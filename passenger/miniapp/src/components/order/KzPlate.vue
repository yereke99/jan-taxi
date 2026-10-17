<!--
  Kazakhstan licence plate badge: the blue band with the sun and "KZ", the
  number and the two-digit region code in its own cell, black border like the
  real plate. Plates that do not follow the "123 ABC 04" pattern are shown
  as they are.
-->
<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(defineProps<{ plate: string; size?: 'regular' | 'small' }>(), { size: 'regular' })

const parts = computed(() => {
  const compact = props.plate.toUpperCase().replace(/\s+/g, '')
  const m = /^(\d{3})([A-Z]{2,3})(\d{2})$/.exec(compact)
  return m ? { main: `${m[1]} ${m[2]}`, region: m[3] } : { main: props.plate.toUpperCase(), region: '' }
})
</script>

<template>
  <span class="kz-plate" :class="`is-${size}`" role="img" :aria-label="plate">
    <span class="kz-plate__band" aria-hidden="true">
      <span class="kz-plate__sun" />
      <span class="kz-plate__code">KZ</span>
    </span>
    <span class="kz-plate__main" aria-hidden="true">{{ parts.main }}</span>
    <span v-if="parts.region" class="kz-plate__region" aria-hidden="true">{{ parts.region }}</span>
  </span>
</template>

<style scoped>
.kz-plate {
  display: inline-flex;
  align-items: stretch;
  height: 40px;
  border: 2px solid #000;
  border-radius: 7px;
  background: #fff;
  color: #000;
  overflow: hidden;
  font-family: 'DIN Alternate', 'Arial Narrow', var(--jt-font-family);
  font-variant-numeric: tabular-nums;
}

.kz-plate__band {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: space-between;
  width: 20px;
  padding: 5px 0 3px;
  background: #00afca;
}

.kz-plate__sun {
  width: 9px;
  height: 9px;
  border-radius: 50%;
  background: #fec50c;
}

.kz-plate__code {
  font: 700 8px/1 var(--jt-font-family);
  color: #fff;
}

.kz-plate__main {
  display: flex;
  align-items: center;
  padding: 0 8px;
  font-size: 24px;
  font-weight: 600;
  letter-spacing: 0.5px;
  white-space: nowrap;
}

.kz-plate__region {
  display: flex;
  align-items: center;
  padding: 0 6px;
  border-left: 2px solid #000;
  font-size: 24px;
  font-weight: 600;
}

.kz-plate.is-small {
  height: 30px;
  border-width: 1.5px;
  border-radius: 6px;
}

.kz-plate.is-small .kz-plate__band {
  width: 15px;
  padding: 4px 0 2px;
}

.kz-plate.is-small .kz-plate__sun {
  width: 7px;
  height: 7px;
}

.kz-plate.is-small .kz-plate__code {
  font-size: 6px;
}

.kz-plate.is-small .kz-plate__main,
.kz-plate.is-small .kz-plate__region {
  font-size: 18px;
  padding: 0 6px;
}

.kz-plate.is-small .kz-plate__region {
  border-left-width: 1.5px;
  padding: 0 5px;
}
</style>

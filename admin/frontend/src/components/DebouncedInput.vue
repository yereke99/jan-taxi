<!-- Text filter that commits after a pause in typing (or on Enter), so lists are not re-queried per keystroke. -->
<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'

const props = withDefaults(defineProps<{ modelValue: string; placeholder?: string; delay?: number }>(), { placeholder: '', delay: 400 })
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()

const local = ref(props.modelValue)
let timer: ReturnType<typeof setTimeout> | undefined

watch(
  () => props.modelValue,
  (v) => {
    if (v !== local.value.trim()) local.value = v
  },
)

function commit(): void {
  clearTimeout(timer)
  const v = local.value.trim()
  if (v !== props.modelValue) emit('update:modelValue', v)
}

function onInput(e: Event): void {
  local.value = (e.target as HTMLInputElement).value
  clearTimeout(timer)
  timer = setTimeout(commit, props.delay)
}

onBeforeUnmount(() => clearTimeout(timer))
</script>

<template>
  <input class="dinput" type="search" :value="local" :placeholder="placeholder" :aria-label="placeholder" @input="onInput" @keydown.enter.prevent="commit" @blur="commit" />
</template>

<style scoped>
.dinput {
  width: 180px;
  height: var(--jt-control-height);
  padding: 0 10px;
  border: 0;
  border-radius: var(--jt-radius-control);
  background: var(--jt-color-bg);
  box-shadow: inset 0 0 0 1px var(--jt-color-border);
  font: var(--jt-font-body);
  outline: none;
}

.dinput:focus {
  box-shadow: inset 0 0 0 2px var(--jt-color-primary);
}
</style>

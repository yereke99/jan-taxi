<!--
  Label + control + hint/error. The default slot receives { id, invalid,
  describedby } to wire the control:
    <FormField :label="t('auth.email')" :error="err" v-slot="f">
      <TextInput :id="f.id" :invalid="f.invalid" :aria-describedby="f.describedby" v-model="email" />
    </FormField>
-->
<script setup lang="ts">
import { computed, useId } from 'vue'
import { useI18n } from 'vue-i18n'

const props = withDefaults(
  defineProps<{ label: string; hint?: string; error?: string; required?: boolean; id?: string }>(),
  { hint: '', error: '', required: false, id: undefined },
)

defineSlots<{ default(props: { id: string; invalid: boolean; describedby: string | undefined }): unknown }>()

const { t } = useI18n()
const generated = useId()
const fieldId = computed(() => props.id ?? generated)
const msgId = computed(() => `${fieldId.value}-msg`)
const describedby = computed(() => (props.error || props.hint ? msgId.value : undefined))
</script>

<template>
  <div class="field" :class="{ 'is-invalid': error }">
    <label class="field__label" :for="fieldId">
      {{ label }}
      <span v-if="required" class="field__required" :title="t('common.required')" aria-hidden="true">*</span>
    </label>
    <slot :id="fieldId" :invalid="Boolean(error)" :describedby="describedby" />
    <p v-if="error || hint" :id="msgId" class="field__msg" :role="error ? 'alert' : undefined">{{ error || hint }}</p>
  </div>
</template>

<style scoped>
.field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.field__label {
  font: 500 13px/18px var(--jt-font-family);
  color: var(--jt-color-label);
}

.field__required {
  margin-left: 2px;
  color: var(--jt-color-red);
}

.field__msg {
  font: var(--jt-font-small);
  color: var(--jt-color-label-secondary);
}

.is-invalid .field__msg {
  color: var(--jt-color-red);
}
</style>

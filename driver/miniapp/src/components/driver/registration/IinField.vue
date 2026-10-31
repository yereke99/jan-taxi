<!--
  The IIN (ЖСН) input: digits only with the numeric keyboard and a 0/12
  counter, checked as it is typed. A wrong check digit (or a thirteenth digit)
  is reported at once; "required" and "12 digits" wait until the driver tries
  to go on (showErrors). A server message (taken by another driver) wins.
-->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { IIN_LENGTH, iinDigits, iinProblem } from '../../../features/registration/iin'
import { JTextField } from '../../ui'

const props = withDefaults(
  defineProps<{
    modelValue: string
    label: string
    showErrors?: boolean
    /** An error the API returned for this value, shown instead of the local checks. */
    serverError?: string
    readonly?: boolean
    hint?: string
  }>(),
  { showErrors: false, serverError: '', readonly: false, hint: '' },
)

const emit = defineEmits<{ 'update:modelValue': [value: string]; enter: [] }>()

const { t } = useI18n()
const box = ref<HTMLElement | null>(null)

const error = computed(() => {
  if (props.readonly) return ''
  if (props.serverError) return props.serverError
  const problem = iinProblem(props.modelValue)
  if (!problem) return ''
  const overLength = props.modelValue.length > IIN_LENGTH
  return problem === 'checksum' || overLength || props.showErrors ? t(`registration.personal.iin_errors.${problem}`) : ''
})

function onInput(raw: string): void {
  const digits = iinDigits(raw)
  // The input is controlled: when a letter or a space was dropped, show what is kept.
  if (digits !== raw) {
    const input = box.value?.querySelector('input')
    if (input) input.value = digits
  }
  if (digits !== props.modelValue) emit('update:modelValue', digits)
}
</script>

<template>
  <div ref="box">
    <JTextField
      :model-value="modelValue"
      :label="label"
      inputmode="numeric"
      autocomplete="off"
      enterkeyhint="done"
      :max="IIN_LENGTH"
      :readonly="readonly"
      :error="error"
      :hint="error ? '' : hint"
      @update:model-value="onInput"
      @enter="emit('enter')"
    />
  </div>
</template>

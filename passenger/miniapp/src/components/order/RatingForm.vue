<!-- Trip rating: 1–5 stars and an optional comment (≤120 code points), sent once (POST /orders/{id}/rating). -->
<script setup lang="ts">
import { Star } from '@lucide/vue'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { rateOrder } from '../../api/orders'
import type { Order } from '../../api/types'
import { useErrorText, useFieldErrorText } from '../../i18n/core'
import { isApiError } from '../../services/http'
import { haptics } from '../../telegram'
import { cleanText, fitsLimit, TEXT_MAX } from '../../utils/text'
import { JButton, JTextArea, toast } from '../ui'

const props = defineProps<{ orderId: string }>()
const emit = defineEmits<{ rated: [order: Order] }>()

const { t } = useI18n()
const errorText = useErrorText()
const fieldErrorText = useFieldErrorText()

const rating = ref(0)
const comment = ref('')
const sending = ref(false)
const commentError = ref('')
const ratingError = ref('')

const canSend = computed(() => rating.value >= 1 && fitsLimit(comment.value) && !sending.value)

function choose(n: number): void {
  rating.value = n
  ratingError.value = ''
  haptics.selection()
}

async function send(): Promise<void> {
  if (rating.value < 1) {
    ratingError.value = t('rating.required')
    return
  }
  if (!fitsLimit(comment.value)) {
    commentError.value = fieldErrorText('too_long')
    return
  }
  sending.value = true
  commentError.value = ''
  try {
    const order = await rateOrder(props.orderId, rating.value, cleanText(comment.value) || undefined)
    haptics.notification('success')
    toast.success(t('rating.thanks'))
    emit('rated', order)
  } catch (err) {
    if (isApiError(err) && err.fields.comment) commentError.value = fieldErrorText(err.fields.comment)
    else toast.error(errorText(err))
  } finally {
    sending.value = false
  }
}
</script>

<template>
  <section class="rating">
    <h2 class="rating__title">{{ t('rating.title') }}</h2>
    <div class="rating__stars" role="radiogroup" :aria-label="t('rating.title')">
      <button
        v-for="n in 5"
        :key="n"
        type="button"
        role="radio"
        class="rating__star"
        :class="{ 'is-on': n <= rating }"
        :aria-checked="n === rating"
        :aria-label="t('rating.star', { n })"
        @click="choose(n)"
      >
        <Star :size="36" :stroke-width="1.5" :fill="n <= rating ? 'currentColor' : 'none'" aria-hidden="true" />
      </button>
    </div>
    <p v-if="ratingError" class="rating__error" role="alert">{{ ratingError }}</p>
    <JTextArea
      v-if="rating > 0"
      v-model="comment"
      :label="t('rating.comment')"
      :placeholder="t('rating.comment_placeholder')"
      :max="TEXT_MAX"
      :rows="2"
      :error="commentError"
    />
    <JButton block :loading="sending" :disabled="!canSend" @click="send">{{ t('rating.submit') }}</JButton>
  </section>
</template>

<style scoped>
.rating {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-3);
}

.rating__title {
  font: var(--jt-font-headline);
  text-align: center;
}

.rating__stars {
  display: flex;
  justify-content: center;
  gap: var(--jt-space-2);
}

.rating__star {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 48px;
  height: 48px;
  color: var(--jt-color-label-tertiary);
  transition: transform var(--jt-duration-fast) var(--jt-ease-spring);
}

.rating__star.is-on {
  color: var(--jt-color-label);
}

.rating__star:active {
  transform: scale(0.9);
}

.rating__error {
  font: var(--jt-font-footnote);
  color: var(--jt-color-red-text);
  text-align: center;
}
</style>

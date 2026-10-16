<script setup lang="ts">
import type { TopUp } from '../../api/types'
import { useFormat } from '../../composables/useFormat'
import { topUpStatusTone } from '../../utils/status'
import { useI18n } from 'vue-i18n'
import { JBadge, JListRow } from '../ui'
import { ROUTE } from '../../router/names'

defineProps<{ topup: TopUp }>()

const { t } = useI18n()
const fmt = useFormat()
</script>

<template>
  <JListRow :to="{ name: ROUTE.topupDetail, params: { id: topup.id } }" :title="fmt.money(topup.verified_amount ?? topup.amount)">
    <template #subtitle>
      <span class="jt-tabular">{{ topup.reference }}</span> · {{ fmt.dateTime(topup.created_at) }}
    </template>
    <template #accessory>
      <JBadge :tone="topUpStatusTone(topup.status)" :label="t(`topup_status.${topup.status}`)" />
    </template>
  </JListRow>
</template>

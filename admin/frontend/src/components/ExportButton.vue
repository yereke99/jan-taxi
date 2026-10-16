<!--
  CSV export of the current filters (reports.export). The file is fetched with
  the session cookie and handed to the browser as a download.
-->
<script setup lang="ts">
import { Download } from '@lucide/vue'
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { reportsApi, type ReportKind } from '../api/system'
import { useErrors } from '../composables/useErrors'
import { useAuthStore } from '../stores/auth'
import AppButton from './AppButton.vue'
import { toast } from './toast'

const props = defineProps<{ kind: ReportKind; query: () => Record<string, string | undefined> }>()

const { t } = useI18n()
const errors = useErrors()
const auth = useAuthStore()
const busy = ref(false)

async function run(): Promise<void> {
  if (busy.value) return
  busy.value = true
  const id = toast.show(t('common.exporting'), 'neutral', 0)
  try {
    const name = await reportsApi.download(props.kind, props.query())
    toast.success(t('common.exported', { name }))
  } catch (err) {
    errors.report(err)
  } finally {
    toast.dismiss(id)
    busy.value = false
  }
}
</script>

<template>
  <AppButton v-if="auth.can('reports.export')" size="sm" :loading="busy" @click="run">
    <template #icon><Download :size="14" aria-hidden="true" /></template>
    {{ t('common.export_csv') }}
  </AppButton>
</template>

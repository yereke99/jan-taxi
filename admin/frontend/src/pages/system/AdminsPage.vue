<!-- Administrators (admins.manage): roles, status, last sign-in, lockouts. -->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminsApi, type AdminUser } from '../../api/system'
import { AppButton, DataTable, Pagination, StateView, type Column } from '../../components'
import { useFormat } from '../../composables/useFormat'
import { usePagedList } from '../../composables/usePagedList'
import { useAuthStore } from '../../stores/auth'
import AdminForm from './AdminForm.vue'

const { t } = useI18n()
const fmt = useFormat()
const auth = useAuthStore()

const list = usePagedList({}, (_, page, size, signal) => adminsApi.list(page, size, signal))
const formOpen = ref(false)
const editing = ref<AdminUser | null>(null)

const columns = computed<Column<AdminUser>[]>(() => [
  { key: 'full_name', label: t('system.full_name') },
  { key: 'email', label: t('auth.email'), mono: true },
  { key: 'role', label: t('system.role'), format: (v) => t(`roles.${String(v)}`) },
  { key: 'is_active', label: t('common.status') },
  { key: 'last_login_at', label: t('system.last_login'), format: (v) => fmt.dateTime(v as string | null) },
  { key: 'created_at', label: t('common.created'), format: (v) => fmt.date(v as string) },
])

function openForm(a: AdminUser | null): void {
  editing.value = a
  formOpen.value = true
}

function lockedNow(a: AdminUser): boolean {
  return a.locked_until !== null && Date.parse(a.locked_until) > Date.now()
}
</script>

<template>
  <section class="jt-page">
    <div class="toolbar">
      <p class="jt-muted">{{ t('system.admins_hint') }}</p>
      <AppButton variant="primary" size="sm" @click="openForm(null)">{{ t('system.admin_new') }}</AppButton>
    </div>
    <StateView v-if="list.error.value" :error="list.error.value" @retry="list.reload()" />
    <template v-else>
      <DataTable :columns="columns" :rows="list.rows.value" row-key="id" :loading="list.loading.value" clickable @row-click="openForm">
        <template #cell-full_name="{ row }">
          {{ row.full_name }}
          <span v-if="row.id === auth.admin?.id" class="jt-muted"> · {{ t('system.you') }}</span>
        </template>
        <template #cell-is_active="{ row }">
          <span v-if="lockedNow(row)" class="jt-negative">{{ t('system.locked_until', { time: fmt.dateTime(row.locked_until) }) }}</span>
          <span v-else-if="row.is_active">{{ t('common.active') }}</span>
          <span v-else class="jt-muted">{{ t('system.deactivated') }}</span>
        </template>
      </DataTable>
      <Pagination v-model:page="list.page.value" :page-size="list.pageSize" :total="list.total.value" />
    </template>
    <AdminForm v-model:open="formOpen" :admin="editing" @saved="list.reload({ silent: true })" />
  </section>
</template>

<style scoped>
.toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--jt-space-4);
}
</style>

<!--
  A passenger in marketing tables: name and masked phone (never the full
  number), linked to the passenger's marketing page. With only an id, a short
  form of it.
-->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { UserRef } from '../../api/marketing'

const props = defineProps<{ user?: UserRef | null; id?: string | null }>()

const { t } = useI18n()
const userId = computed(() => props.user?.id ?? props.id ?? null)
const name = computed(() => props.user?.name?.trim() || (userId.value ? t('marketing.user_short', { id: userId.value.slice(0, 8) }) : '—'))
</script>

<template>
  <span class="person">
    <RouterLink v-if="userId" :to="{ name: 'marketing-user', params: { id: userId } }" @click.stop>{{ name }}</RouterLink>
    <span v-else class="jt-muted">—</span>
    <span v-if="user?.phone_masked" class="jt-mono jt-muted person__phone">{{ user.phone_masked }}</span>
  </span>
</template>

<style scoped>
.person {
  display: inline-flex;
  flex-direction: column;
  align-items: flex-start;
  line-height: 18px;
}

.person__phone {
  font-size: 12px;
}
</style>

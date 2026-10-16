<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { AppButton } from '../components'

const { t } = useI18n()
</script>

<template>
  <main class="state">
    <h1 class="state__title">{{ t('pages.not_found_title') }}</h1>
    <p class="state__text">{{ t('pages.not_found_text') }}</p>
    <RouterLink to="/" custom v-slot="{ navigate }">
      <AppButton variant="primary" @click="navigate">{{ t('common.back_home') }}</AppButton>
    </RouterLink>
  </main>
</template>

<style scoped>
.state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--jt-space-3);
  height: 100%;
  padding: var(--jt-space-6);
  text-align: center;
}

.state__title {
  font: var(--jt-font-title);
}

.state__text {
  color: var(--jt-color-label-secondary);
}
</style>

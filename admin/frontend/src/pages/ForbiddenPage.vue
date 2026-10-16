<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { AppButton } from '../components'
import { firstAllowedRoute } from '../navigation'
import { useAuthStore } from '../stores/auth'

const { t } = useI18n()
const auth = useAuthStore()
const home = computed(() => firstAllowedRoute(auth.can))
</script>

<template>
  <section class="state">
    <h2 class="state__title">{{ t('pages.forbidden_title') }}</h2>
    <p class="state__text">{{ t('pages.forbidden_text') }}</p>
    <RouterLink v-if="home" :to="{ name: home }" custom v-slot="{ navigate }">
      <AppButton variant="primary" @click="navigate">{{ t('common.back_home') }}</AppButton>
    </RouterLink>
  </section>
</template>

<style scoped>
.state {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--jt-space-3);
  max-width: 480px;
}

.state__title {
  font: var(--jt-font-title);
}

.state__text {
  color: var(--jt-color-label-secondary);
}
</style>

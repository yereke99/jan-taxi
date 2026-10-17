<!--
  Top row of the driver's main lists: the profile photo (initials until it
  loads), the registered name and the driver code. The Telegram name is only
  a fallback while the profile is loading.
-->
<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useProfileStore } from '../../stores/profile'
import { useSessionStore } from '../../stores/session'
import PrivateImage from './PrivateImage.vue'

const session = useSessionStore()
const profile = useProfileStore()

const names = computed<[string, string]>(() => {
  const p = profile.profile
  if (p && (p.first_name || p.last_name)) return [p.first_name, p.last_name]
  return [session.me?.first_name ?? '', session.me?.last_name ?? '']
})
const name = computed(() => names.value.filter(Boolean).join(' '))
const initials = computed(() =>
  names.value
    .map((s) => s.trim().charAt(0).toUpperCase())
    .filter(Boolean)
    .join(''),
)
const avatarUrl = computed(() => profile.profile?.avatar?.url ?? null)

onMounted(() => {
  if (!profile.profile && session.driver) void profile.refresh()
})
</script>

<template>
  <div class="who">
    <span class="who__avatar" aria-hidden="true">
      <PrivateImage v-if="avatarUrl" :url="avatarUrl" round>
        <template #empty>{{ initials }}</template>
      </PrivateImage>
      <template v-else>{{ initials }}</template>
    </span>
    <span class="who__text">
      <span class="who__name">{{ name }}</span>
      <span v-if="session.driver?.code" class="who__code jt-tabular">{{ session.driver.code }}</span>
    </span>
  </div>
</template>

<style scoped>
.who {
  display: flex;
  align-items: center;
  gap: var(--jt-space-3);
  min-width: 0;
}

.who__avatar {
  flex: none;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 44px;
  height: 44px;
  border-radius: 50%;
  overflow: hidden;
  background: var(--jt-color-fill-tertiary);
  font: 600 15px/1 var(--jt-font-family);
  color: var(--jt-color-label-secondary);
}

.who__avatar :deep(.pimg) {
  width: 100%;
  height: 100%;
}

.who__text {
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.who__name {
  font: var(--jt-font-headline);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.who__code {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}
</style>

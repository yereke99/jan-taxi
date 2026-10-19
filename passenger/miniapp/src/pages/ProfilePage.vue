<!--
  Profile and menu: who is signed in, the phone (shared through Telegram so
  drivers can call, §22.5) and the passenger's sections.
-->
<script setup lang="ts">
import { Car, Headset, Languages, ListOrdered } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import ScrollPage from '../components/ScrollPage.vue'
import { JListGroup, JListRow, JSpinner, toast } from '../components/ui'
import { ROUTE } from '../router/names'
import { usePhoneStore } from '../stores/phone'
import { useSessionStore } from '../stores/session'

const { t } = useI18n()
const session = useSessionStore()
const phone = usePhoneStore()

async function sharePhone(): Promise<void> {
  const outcome = await phone.share()
  if (outcome === 'verified') toast.success(t('phone.saved'))
  else if (outcome === 'timeout') toast.show(t('phone.not_confirmed'))
}

const me = computed(() => session.me)
const name = computed(() => [me.value?.first_name, me.value?.last_name].filter(Boolean).join(' '))
const initial = computed(() => (me.value?.first_name ?? '').trim().charAt(0).toUpperCase())
</script>

<template>
  <ScrollPage :title="t('menu.title')">
    <section v-if="me" class="profile-card">
      <span class="profile-card__avatar" aria-hidden="true">{{ initial }}</span>
      <span class="profile-card__text">
        <span class="profile-card__name">{{ name }}</span>
        <span v-if="me.username" class="profile-card__meta">@{{ me.username }}</span>
      </span>
    </section>

    <JListGroup v-if="me" :footer="me.phone_masked ? '' : t('menu.phone_hint')">
      <JListRow :title="t('menu.phone')" :value="me.phone_masked ?? t('menu.phone_none')" />
      <JListRow v-if="!me.phone_verified" :title="t('menu.phone_share')" clickable :chevron="false" :disabled="phone.flow !== 'idle'" @click="sharePhone">
        <template v-if="phone.flow !== 'idle'" #accessory><JSpinner size="small" /></template>
      </JListRow>
    </JListGroup>

    <JListGroup>
      <JListRow :title="t('menu.my_orders')" :to="{ name: ROUTE.orders }">
        <template #icon><ListOrdered :size="22" :stroke-width="1.75" /></template>
      </JListRow>
      <JListRow :title="t('menu.support')" :to="{ name: ROUTE.support }">
        <template #icon><Headset :size="22" :stroke-width="1.75" /></template>
      </JListRow>
      <JListRow :title="t('menu.become_driver')" :to="{ name: ROUTE.becomeDriver }">
        <template #icon><Car :size="22" :stroke-width="1.75" /></template>
      </JListRow>
      <JListRow :title="t('menu.language')" :value="t(`languages.${session.language}`)" :to="{ name: ROUTE.language }">
        <template #icon><Languages :size="22" :stroke-width="1.75" /></template>
      </JListRow>
    </JListGroup>
  </ScrollPage>
</template>

<style scoped>
.profile-card {
  display: flex;
  align-items: center;
  gap: var(--jt-space-4);
  margin: 0 var(--jt-side-margin) var(--jt-space-6);
  padding: var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
}

.profile-card__avatar {
  flex: none;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 56px;
  height: 56px;
  border-radius: 50%;
  background: var(--jt-color-primary);
  color: var(--jt-color-on-primary);
  font: var(--jt-font-title2);
}

.profile-card__text {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.profile-card__name {
  font: var(--jt-font-title3);
  overflow-wrap: anywhere;
}

.profile-card__meta {
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}
</style>

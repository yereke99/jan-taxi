<!--
  Review layout: what the driver declared next to the evidence they uploaded
  (identity photos, vehicle photos with the declared plate shown large).
  Images are loaded through the API with the session cookie; ID documents
  need documents.view (the API omits them otherwise).
-->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AdminDriverDetail } from '../../api/drivers'
import { ImageViewer, KeyValueList, SecureImage, type KeyValueItem, type ViewerImage } from '../../components'
import { useFormat } from '../../composables/useFormat'
import PlateBadge from './PlateBadge.vue'

const props = withDefaults(defineProps<{ driver: AdminDriverDetail; section?: 'all' | 'identity' | 'vehicle' }>(), { section: 'all' })

const { t, te } = useI18n()
const fmt = useFormat()
const viewerIndex = ref<number | null>(null)

const PHOTO_ORDER = ['plate', 'main', 'back', 'side', 'extra'] as const

interface Evidence extends ViewerImage {
  key: string
}

const identity = computed<Array<Evidence & { missing: boolean }>>(() => {
  const d = props.driver
  const docs = d.documents
  const out: Array<Evidence & { missing: boolean }> = [
    { key: 'avatar', url: d.avatar?.url ?? '', label: t('correction_field.avatar'), missing: !d.avatar },
  ]
  if (docs) {
    out.push(
      { key: 'id_front', url: docs.id_front?.url ?? '', label: t('correction_field.id_front'), missing: !docs.id_front },
      { key: 'id_back', url: docs.id_back?.url ?? '', label: t('correction_field.id_back'), missing: !docs.id_back },
    )
  }
  return out
})

const vehiclePhotos = computed<Evidence[]>(() =>
  [...(props.driver.vehicle?.photos ?? [])]
    .sort((a, b) => PHOTO_ORDER.indexOf(a.kind) - PHOTO_ORDER.indexOf(b.kind))
    .map((p) => ({ key: p.id, url: p.url, label: t(`drivers.photo_${p.kind}`) })),
)

const platePhoto = computed(() => vehiclePhotos.value.find((p) => p.key === props.driver.vehicle?.photos.find((x) => x.kind === 'plate')?.id) ?? null)
const mainPhoto = computed(() => vehiclePhotos.value.find((p) => p.key === props.driver.vehicle?.photos.find((x) => x.kind === 'main')?.id) ?? null)

/** All images in one viewer sequence (←/→ walks through them). */
const gallery = computed<Evidence[]>(() => [...identity.value.filter((i) => !i.missing), ...vehiclePhotos.value])

function openImage(key: string): void {
  const i = gallery.value.findIndex((g) => g.key === key)
  viewerIndex.value = i >= 0 ? i : null
}

const declaredPerson = computed<KeyValueItem[]>(() => {
  const d = props.driver
  return [
    { key: 'name', label: t('drivers.full_name'), value: `${d.first_name} ${d.last_name}`.trim() },
    { key: 'phone', label: t('common.phone'), value: d.phone ? `${d.phone} · ${d.phone_verified ? t('drivers.verified') : t('drivers.unverified')}` : null, mono: true },
    { key: 'city', label: t('common.city'), value: d.city ? fmt.name(d.city.name) : null },
    { key: 'telegram', label: 'Telegram', value: d.user.username ? `@${d.user.username}` : String(d.user.telegram_user_id), mono: true },
  ]
})

const declaredVehicle = computed<KeyValueItem[]>(() => {
  const v = props.driver.vehicle
  return [
    { key: 'make', label: t('correction_field.vehicle_make_model'), value: v ? `${v.make} ${v.model}`.trim() : null },
    { key: 'color', label: t('correction_field.vehicle_color'), value: v?.color ?? null },
    { key: 'normalized', label: t('drivers.plate_normalized'), value: v?.plate_normalized ?? null, mono: true },
    { key: 'kind', label: t('drivers.plate_kind'), value: v?.plate_kind ? (te(`drivers.plate_kind_${v.plate_kind}`, 'kk') ? t(`drivers.plate_kind_${v.plate_kind}`) : v.plate_kind) : null },
  ]
})
</script>

<template>
  <div class="review">
    <section v-if="section !== 'vehicle'" class="review__block">
      <h3 class="review__title">{{ t('drivers.identity') }}</h3>
      <div class="review__grid">
        <KeyValueList class="review__declared" :items="declaredPerson" />
        <div class="review__evidence">
          <SecureImage
            v-for="img in identity"
            :key="img.key"
            :url="img.missing ? null : img.url"
            :label="img.label"
            height="170px"
            fit="contain"
            @open="openImage(img.key)"
          />
          <p v-if="!driver.documents" class="review__locked">{{ t('viewer.no_access') }}</p>
        </div>
      </div>
    </section>

    <section v-if="section !== 'identity'" class="review__block">
      <h3 class="review__title">{{ t('drivers.vehicle') }}</h3>
      <div class="review__plate-row">
        <div class="review__plate">
          <p class="jt-muted">{{ t('drivers.declared_plate') }}</p>
          <PlateBadge :plate="driver.vehicle?.plate ?? ''" />
          <KeyValueList :items="declaredVehicle" />
        </div>
        <SecureImage
          :url="platePhoto?.url ?? null"
          :label="t('drivers.photo_plate')"
          height="220px"
          fit="contain"
          @open="platePhoto && openImage(platePhoto.key)"
        />
        <SecureImage
          :url="mainPhoto?.url ?? null"
          :label="t('drivers.photo_main')"
          height="220px"
          fit="contain"
          @open="mainPhoto && openImage(mainPhoto.key)"
        />
      </div>
      <div class="review__photos">
        <SecureImage
          v-for="p in vehiclePhotos.filter((x) => x.key !== platePhoto?.key && x.key !== mainPhoto?.key)"
          :key="p.key"
          :url="p.url"
          :label="p.label"
          height="150px"
          @open="openImage(p.key)"
        />
      </div>
    </section>

    <ImageViewer v-model:index="viewerIndex" :images="gallery" />
  </div>
</template>

<style scoped>
.review {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-5);
}

.review__block {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-3);
}

.review__title {
  font: var(--jt-font-headline);
}

.review__grid {
  display: grid;
  grid-template-columns: minmax(200px, 280px) minmax(0, 1fr);
  gap: var(--jt-space-4);
  align-items: start;
}

.review__evidence {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(130px, 1fr));
  gap: var(--jt-space-3);
}

.review__locked {
  grid-column: span 2;
  align-self: center;
  color: var(--jt-color-label-secondary);
}

.review__plate-row {
  display: grid;
  grid-template-columns: minmax(200px, 280px) repeat(auto-fit, minmax(150px, 1fr));
  gap: var(--jt-space-3);
  align-items: start;
}

.review__plate {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--jt-space-2);
}

.review__plate :deep(.kv) {
  align-self: stretch;
}

.review__photos {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
  gap: var(--jt-space-3);
}
</style>

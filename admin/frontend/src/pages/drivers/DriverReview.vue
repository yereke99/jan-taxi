<!--
  Review layout: what the driver declared (name, patronymic, the masked IIN,
  phone) next to the evidence: the profile photo, plus the ID scans and
  vehicle photos of drivers who registered before those were retired (new
  drivers never upload them, which is not a gap). Images are loaded through
  the API with the session cookie; ID scans need documents.view (the API
  omits them otherwise).
-->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { FileRef } from '../../api/common'
import type { AdminDriverDetail } from '../../api/drivers'
import { ImageViewer, KeyValueList, SecureImage, VehicleClassIcon, type KeyValueItem, type ViewerImage } from '../../components'
import { useFormat } from '../../composables/useFormat'
import { useVehicleClasses } from '../../composables/useVehicleClasses'
import PlateBadge from './PlateBadge.vue'

const props = withDefaults(defineProps<{ driver: AdminDriverDetail; section?: 'all' | 'identity' | 'vehicle' }>(), { section: 'all' })

const { t, te } = useI18n()
const fmt = useFormat()
const vc = useVehicleClasses()
const viewerIndex = ref<number | null>(null)

const PHOTO_ORDER = ['plate', 'main', 'back', 'side', 'extra'] as const

interface Evidence extends ViewerImage {
  mime?: string
  key: string
}

/** The ID scans the driver uploaded before they were retired; [] for everyone else. */
const idScans = computed<Evidence[]>(() => {
  const docs = props.driver.documents
  if (!docs) return []
  const scan = (key: 'id_front' | 'id_back', f: FileRef | null): Evidence[] =>
    f ? [{ key, url: f.url, mime: f.mime, label: t(`correction_field.${key}`) }] : []
  return [...scan('id_front', docs.id_front), ...scan('id_back', docs.id_back)]
})

/** The profile photo (required, so a missing one is shown as such) and any ID scans. */
const identity = computed<Array<Evidence & { missing: boolean }>>(() => {
  const d = props.driver
  return [
    { key: 'avatar', url: d.avatar?.url ?? '', label: t('correction_field.avatar'), missing: !d.avatar },
    ...idScans.value.map((s) => ({ ...s, missing: false })),
  ]
})

const vehiclePhotos = computed<Evidence[]>(() =>
  [...(props.driver.vehicle?.photos ?? [])]
    .sort((a, b) => PHOTO_ORDER.indexOf(a.kind) - PHOTO_ORDER.indexOf(b.kind))
    .map((p) => ({ key: p.id, url: p.url, label: t(`drivers.photo_${p.kind}`) })),
)

const platePhoto = computed(() => vehiclePhotos.value.find((p) => p.key === props.driver.vehicle?.photos.find((x) => x.kind === 'plate')?.id) ?? null)
const mainPhoto = computed(() => vehiclePhotos.value.find((p) => p.key === props.driver.vehicle?.photos.find((x) => x.kind === 'main')?.id) ?? null)

/** All images in one viewer sequence (←/→ walks through them). */
const gallery = computed<Evidence[]>(() => [...identity.value.filter((i) => !i.missing && i.mime !== 'application/pdf'), ...vehiclePhotos.value])

function openImage(key: string): void {
  const i = gallery.value.findIndex((g) => g.key === key)
  viewerIndex.value = i >= 0 ? i : null
}

const declaredPerson = computed<KeyValueItem[]>(() => {
  const d = props.driver
  return [
    { key: 'name', label: t('drivers.full_name'), value: `${d.first_name} ${d.last_name}`.trim() },
    { key: 'patronymic', label: t('drivers.patronymic'), value: d.patronymic },
    { key: 'iin', label: t('drivers.iin'), value: d.iin_masked ?? t('drivers.iin_not_entered'), mono: d.iin_masked !== null },
    { key: 'phone', label: t('common.phone'), value: d.phone ? `${d.phone} · ${d.phone_verified ? t('drivers.verified') : t('drivers.unverified')}` : null, mono: true },
    { key: 'city', label: t('drivers.region'), value: d.city ? fmt.name(d.city.name) : null },
    { key: 'telegram', label: 'Telegram', value: d.user.username ? `@${d.user.username}` : String(d.user.telegram_user_id), mono: true },
  ]
})

const declaredVehicle = computed<KeyValueItem[]>(() => {
  const v = props.driver.vehicle
  return [
    { key: 'class', label: t('classes.class'), value: v?.vehicle_class ? vc.name(v.vehicle_class, v.vehicle_class_name) : t('classes.not_chosen') },
    { key: 'seats', label: t('classes.seats_declared'), value: v?.seats ?? null },
    { key: 'cargo', label: t('classes.cargo_declared'), value: v?.cargo_volume_l === null || v?.cargo_volume_l === undefined ? null : t('classes.liters', { n: fmt.count(v.cargo_volume_l) }) },
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
            :pdf="img.mime === 'application/pdf'"
            :label="img.label"
            height="170px"
            fit="contain"
            @open="openImage(img.key)"
          />
          <p v-if="!driver.documents" class="review__note">{{ t('viewer.no_access') }}</p>
          <p v-else-if="idScans.length === 0" class="review__note">{{ t('drivers.id_scans_none') }}</p>
        </div>
      </div>
    </section>

    <section v-if="section !== 'identity'" class="review__block">
      <h3 class="review__title">{{ t('drivers.vehicle') }}</h3>
      <div class="review__plate-row">
        <div class="review__plate">
          <p class="jt-muted">{{ t('drivers.declared_plate') }}</p>
          <PlateBadge :plate="driver.vehicle?.plate ?? ''" />
          <KeyValueList :items="declaredVehicle">
            <template #value-class="{ item }">
              <span class="review__class">
                <VehicleClassIcon v-if="driver.vehicle?.vehicle_class" :icon="vc.byCode(driver.vehicle.vehicle_class)?.icon ?? driver.vehicle.vehicle_class" :size="16" />
                {{ item.value }}
              </span>
            </template>
          </KeyValueList>
        </div>
        <SecureImage
          v-if="platePhoto"
          :url="platePhoto.url"
          :label="t('drivers.photo_plate')"
          height="220px"
          fit="contain"
          @open="openImage(platePhoto.key)"
        />
        <SecureImage
          v-if="mainPhoto"
          :url="mainPhoto.url"
          :label="t('drivers.photo_main')"
          height="220px"
          fit="contain"
          @open="openImage(mainPhoto.key)"
        />
        <p v-if="vehiclePhotos.length === 0" class="review__note">{{ t('drivers.vehicle_photos_none') }}</p>
      </div>
      <div v-if="vehiclePhotos.length > 0" class="review__photos">
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

.review__note {
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

.review__class {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.review__photos {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
  gap: var(--jt-space-3);
}
</style>

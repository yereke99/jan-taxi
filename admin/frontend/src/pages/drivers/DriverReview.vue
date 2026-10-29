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

const identity = computed<Array<Evidence & { missing: boolean }>>(() => {
  const d = props.driver
  const docs = d.documents
  const out: Array<Evidence & { missing: boolean }> = [
    { key: 'avatar', url: d.avatar?.url ?? '', label: t('correction_field.avatar'), missing: !d.avatar },
  ]
  if (docs) {
    out.push(
      { mime:docs.id_front?.mime,key: 'id_front', url: docs.id_front?.url ?? '', label: t('correction_field.id_front'), missing: !docs.id_front },
      { mime:docs.id_back?.mime,key: 'id_back', url: docs.id_back?.url ?? '', label: t('correction_field.id_back'), missing: !docs.id_back },
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
const gallery = computed<Evidence[]>(() => [...identity.value.filter((i) => !i.missing && i.mime !== 'application/pdf'), ...vehiclePhotos.value])

function openImage(key: string): void {
  const i = gallery.value.findIndex((g) => g.key === key)
  viewerIndex.value = i >= 0 ? i : null
}

const declaredPerson = computed<KeyValueItem[]>(() => {
  const d = props.driver
  return [
    { key: 'name', label: t('drivers.full_name'), value: `${d.first_name} ${d.last_name}`.trim() },
    ...(d.identity ? [
      {key:'iin',label:t('drivers.iin'),value:d.identity.iin,mono:true},
      {key:'document_number',label:t('drivers.document_number'),value:d.identity.document_number,mono:true},
      {key:'document_type',label:t('drivers.document_type'),value:d.identity.document_type},
      {key:'patronymic',label:t('drivers.patronymic'),value:d.identity.patronymic},
    ]:[]),
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
      <div v-if="driver.verification" class="review__block">
        <p>{{ t('drivers.verification') }}: {{ driver.verification.status }} · {{ t('drivers.verification_confidence') }}: {{ driver.verification.confidence ?? '—' }}%</p>
        <p v-if="driver.verification.reasons.length">{{ t('drivers.verification_reasons') }}: {{ driver.verification.reasons.join(', ') }}</p>
        <p v-for="conflict in driver.identity_conflicts ?? []" :key="conflict.kind">{{ t('drivers.verification_reasons') }}: {{ conflict.kind }} · <router-link :to="`/drivers/${conflict.driver_id}`">{{ conflict.driver_code }}</router-link> · {{ conflict.status }}</p>
        <template v-if="driver.verification.result">
          <p>{{ t('drivers.extracted_identity') }}: {{ driver.verification.result.last_name }} {{ driver.verification.result.first_name }} {{ driver.verification.result.extracted.patronymic }}</p>
          <p>{{ t('drivers.iin') }}: {{ driver.verification.result.extracted.iin }} · {{ t('drivers.document_number') }}: {{ driver.verification.result.extracted.document_number }}</p>
          <dl><template v-for="(comparison,field) in driver.verification.result.comparisons" :key="field"><dt>{{ field }}</dt><dd>{{ comparison }} · {{ driver.verification.result.field_confidence[field] }}%</dd></template></dl>
        </template>
      </div>
      <details v-if="driver.verification_history?.length">
        <summary>{{ t('drivers.verification_history') }}</summary>
        <p v-for="item in driver.verification_history" :key="item.id">{{ fmt.dateTime(item.created_at) }} · {{ item.status }} · {{ item.confidence ?? '—' }}%</p>
      </details>
      <div class="review__grid">
        <KeyValueList class="review__declared" :items="declaredPerson" />
        <div class="review__evidence">
          <SecureImage
            v-for="img in identity"
            :key="img.key"
            :url="img.missing ? null : img.url" :pdf="img.mime === 'application/pdf'"
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

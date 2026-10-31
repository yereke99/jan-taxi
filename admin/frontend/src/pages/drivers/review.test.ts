import { mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ref } from 'vue'
import type { AdminDriverDetail, VehiclePhoto } from '../../api/drivers'
import { i18n } from '../../i18n'
import CorrectionsDialog from './CorrectionsDialog.vue'
import DriverReview from './DriverReview.vue'

// No file downloads and no class list request in these tests: thumbnails stay
// empty, class codes are shown as they are.
vi.mock('../../composables/useSecureImage', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../composables/useSecureImage')>()),
  useSecureImage: () => ({ src: ref(null), type: ref(''), loading: ref(false), error: ref(null), reload: () => undefined }),
}))
vi.mock('../../composables/useVehicleClasses', () => ({
  useVehicleClasses: () => ({ name: (code: string | null) => code ?? '—', byCode: () => undefined }),
}))

const file = (id: string, kind: string, mime = 'image/jpeg') => ({
  id,
  kind,
  mime,
  url: `/api/v1/admin/files/${id}`,
  width: 0,
  height: 0,
  created_at: '2026-10-31T08:00:00Z',
})

function detail(over: Partial<AdminDriverDetail> = {}): AdminDriverDetail {
  return {
    id: 'd1',
    code: 'EMB001',
    status: 'approved',
    first_name: 'Erlan',
    last_name: 'Seitov',
    patronymic: 'Askaruly',
    iin_masked: '********1234',
    phone: '+77010000001',
    phone_verified: true,
    phone_verified_at: '2026-10-31T08:00:00Z',
    city: { id: 'c1', code: 'EMB', name: { kk: 'Ембі', ru: 'Эмба', en: 'Embi' } },
    is_online: false,
    online_since: null,
    last_heartbeat_at: null,
    user: { id: 'u1', telegram_user_id: 1001, username: null, is_blocked: false, driver_bot_started_at: null, driver_bot_blocked_at: null },
    avatar: file('a1', 'avatar'),
    documents: { id_front: null, id_back: null },
    vehicle: {
      make: 'Toyota',
      model: 'Camry',
      color: 'White',
      plate: '123 ABC 04',
      plate_normalized: '123ABC04',
      plate_kind: 'current',
      vehicle_class: 'sedan',
      vehicle_class_name: null,
      seats: 4,
      cargo_volume_l: 400,
      photos: [],
    },
    corrections: [],
    applications: [],
    moderation_events: [],
    wallet: null,
    welcome_bonus: null,
    bonuses: [],
    stats: { completed_trips: 0, accepted_orders: 0, cancelled_by_driver: 0, total_fares: 0, total_commission: 0 },
    last_location: null,
    active_order: null,
    rating: null,
    rating_count: 0,
    completed_trips: 0,
    submitted_at: '2026-10-31T08:00:00Z',
    approved_at: '2026-10-31T08:00:00Z',
    suspended_at: null,
    suspended_reason: null,
    pd_consent_at: '2026-10-31T08:00:00Z',
    pd_consent_version: 'driver-pd-2026-10-v3',
    created_at: '2026-10-31T08:00:00Z',
    updated_at: '2026-10-31T08:00:00Z',
    ...over,
  }
}

function captions(root: { findAll: (s: string) => Array<{ text: () => string }> }): string[] {
  return root.findAll('figcaption').map((c) => c.text())
}

const mounted: VueWrapper[] = []

/** Mounts and remembers the wrapper so it is unmounted before the next test. */
function track<W extends VueWrapper>(w: W): W {
  mounted.push(w)
  return w
}

beforeEach(() => {
  i18n.global.locale.value = 'en'
})

afterEach(() => {
  for (const w of mounted.splice(0)) w.unmount()
  i18n.global.locale.value = 'kk'
  document.body.innerHTML = ''
})

describe('DriverReview', () => {
  it('shows the patronymic and the masked IIN; a new driver has no ID scans or car photos and that is not an error', () => {
    const w = track(mount(DriverReview, { props: { driver: detail() }, global: { plugins: [i18n] } }))
    const text = w.text()
    expect(text).toContain('Patronymic')
    expect(text).toContain('Askaruly')
    expect(text).toContain('********1234')
    expect(text).toContain('ID scans are no longer collected')
    expect(text).toContain('Car photos are no longer collected')
    expect(captions(w)).toEqual(['Profile photo'])
  })

  it('says when the IIN was not entered yet and keeps the ID scans and car photos of earlier drivers', () => {
    const photos: VehiclePhoto[] = [
      { ...file('p1', 'main'), kind: 'main' },
      { ...file('p2', 'plate'), kind: 'plate' },
    ]
    const old = detail({
      iin_masked: null,
      patronymic: '',
      documents: { id_front: file('f1', 'id_front', 'application/pdf'), id_back: file('f2', 'id_back') },
      vehicle: { ...detail().vehicle!, photos },
    })
    const w = track(mount(DriverReview, { props: { driver: old }, global: { plugins: [i18n] } }))
    expect(w.text()).toContain('Not entered')
    expect(w.text()).not.toContain('no longer collected')
    expect(captions(w)).toEqual(['Profile photo', 'ID card (front)', 'ID card (back)', 'Plate photo', 'Car, front'])
  })

  it('without documents.view says so instead of listing ID scans', () => {
    const w = track(mount(DriverReview, { props: { driver: detail({ documents: null }), section: 'identity' }, global: { plugins: [i18n] } }))
    expect(w.text()).toContain('No permission to view documents')
    expect(w.text()).not.toContain('ID scans are no longer collected')
  })
})

describe('CorrectionsDialog', () => {
  it('offers only the fields a driver can still change and no reasons about retired photos', async () => {
    const w = track(mount(CorrectionsDialog, { props: { open: false, driver: detail({ status: 'pending' }) }, global: { plugins: [i18n] }, attachTo: document.body }))
    await w.setProps({ open: true })
    const labels = [...document.querySelectorAll('.corr__check span')].map((s) => s.textContent?.trim())
    expect(labels).toEqual([
      'Profile photo',
      'First name',
      'Last name',
      'IIN and patronymic',
      'Phone',
      'Vehicle class, seats and cargo space',
      'Vehicle make and model',
      'Vehicle colour',
      'Licence plate',
    ])
    const identity = [...document.querySelectorAll<HTMLInputElement>('.corr__check input')][3]!
    identity.checked = true
    identity.dispatchEvent(new Event('change'))
    await w.vm.$nextTick()
    const reasons = [...document.querySelectorAll('.corr__reason option')].map((o) => o.textContent?.trim())
    expect(reasons).toContain('Missing information')
    expect(reasons).not.toContain('ID photo is unreadable')
    expect(reasons).not.toContain('Vehicle photo is unclear')
  })
})

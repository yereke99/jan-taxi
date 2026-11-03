import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import type { FileRef } from '../../../api/types'
import { ApiError } from '../../../services/http'
import { ru } from '../../../i18n/messages/ru'
import PhotoField from './PhotoField.vue'

const mocks = vi.hoisted(() => ({
  importAvatar: vi.fn(),
  upload: vi.fn(),
  telegramPhoto: vi.fn(),
  rawInitData: vi.fn(),
  notification: vi.fn(),
}))
vi.mock('../../../stores/registration', () => ({ useRegistrationStore: () => ({ useTelegramAvatar: mocks.importAvatar, upload: mocks.upload }) }))
vi.mock('../../../stores/session', () => ({ useSessionStore: () => ({ me: { telegram_user_id: 42 } }) }))
vi.mock('../../../telegram', () => ({
  getTelegramPhotoUrl: mocks.telegramPhoto,
  getRawInitData: mocks.rawInitData,
  haptics: { notification: mocks.notification },
}))
vi.mock('../../../features/media/image', async (original) => ({
  ...await original<typeof import('../../../features/media/image')>(),
  prepareImage: vi.fn(async (file: Blob) => file),
}))

const saved: FileRef = { id: 'old', kind: 'avatar', url: '/api/v1/driver/files/old', width: 100, height: 100, created_at: '' }
const imported: FileRef = { ...saved, id: 'tg1', url: '/api/v1/driver/files/tg1' }
const mounted: VueWrapper[] = []

function render(locked = false): VueWrapper {
  const wrapper = mount(PhotoField, {
    props: { kind: 'avatar', file: saved, label: 'Фото профиля', facing: 'user', round: true, locked },
    global: {
      plugins: [createI18n({ legacy: false, locale: 'ru', messages: { ru } })],
      stubs: { PrivateImage: { name: 'PrivateImage', props: ['url', 'previewUrl'], template: '<div class="private-preview"><slot name="empty" /></div>' } },
    },
  })
  mounted.push(wrapper)
  return wrapper
}

describe('registration photo sources', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.telegramPhoto.mockReturnValue('https://t.me/i/userpic/320/current.jpg')
    mocks.rawInitData.mockReturnValue('signed-launch')
    mocks.importAvatar.mockResolvedValue(imported)
    mocks.upload.mockResolvedValue(imported)
    const NativeURL = URL
    vi.stubGlobal('URL', class extends NativeURL {
      static override createObjectURL = vi.fn(() => 'blob:preview')
      static override revokeObjectURL = vi.fn()
    })
  })
  afterEach(() => {
    for (const wrapper of mounted.splice(0)) wrapper.unmount()
    vi.unstubAllGlobals()
  })

  it('offers Telegram, front camera and gallery, importing only after selection', async () => {
    const wrapper = render()
    expect(mocks.importAvatar).not.toHaveBeenCalled()
    expect(mocks.telegramPhoto).toHaveBeenCalledWith(42)
    expect(wrapper.findAll('.photo__actions button')).toHaveLength(3)
    const inputs = wrapper.findAll('input[type="file"]')
    expect(inputs[0]!.attributes('capture')).toBe('user')
    expect(inputs[1]!.attributes('capture')).toBeUndefined()
    await wrapper.get('.photo__action--telegram').trigger('click')
    await flushPromises()
    expect(mocks.importAvatar).toHaveBeenCalledWith('signed-launch', expect.any(AbortSignal))
    expect(wrapper.emitted('uploaded')).toEqual([[imported]])
  })

  it('keeps the current photo and allows retry or gallery after a failed import', async () => {
    mocks.importAvatar.mockRejectedValueOnce(new ApiError(502, 'telegram_photo_failed', 'failed'))
    const wrapper = render()
    await wrapper.get('.photo__action--telegram').trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('Не удалось получить фото из Telegram')
    expect(wrapper.getComponent({ name: 'PrivateImage' }).props('url')).toBe(saved.url)
    expect(wrapper.emitted('uploaded')).toBeUndefined()
    expect(wrapper.findAll('.photo__actions button').every((button) => button.attributes('disabled') === undefined)).toBe(true)
    await wrapper.get('.photo__retry').trigger('click')
    await flushPromises()
    expect(wrapper.emitted('uploaded')).toEqual([[imported]])
  })

  it('prevents concurrent source changes and aborts an import when the field is removed', async () => {
    let finish!: (file: FileRef) => void
    mocks.importAvatar.mockImplementation(() => new Promise<FileRef>((resolve) => (finish = resolve)))
    const wrapper = render()
    await wrapper.get('.photo__action--telegram').trigger('click')
    expect(wrapper.get('[role="status"]').text()).toContain('Сохраняем фото')
    expect(wrapper.findAll('.photo__actions button').every((button) => button.attributes('disabled') !== undefined)).toBe(true)
    const signal = mocks.importAvatar.mock.calls[0]![1] as AbortSignal
    wrapper.unmount()
    expect(signal.aborted).toBe(true)
    finish(imported)
    await flushPromises()
    expect(wrapper.emitted('uploaded')).toBeUndefined()
  })

  it('hides unavailable Telegram photos and all editing actions for locked fields', () => {
    mocks.telegramPhoto.mockReturnValue(null)
    const normal = render()
    expect(normal.find('.photo__action--telegram').exists()).toBe(false)
    expect(normal.findAll('.photo__actions button')).toHaveLength(2)
    const locked = render(true)
    expect(locked.find('.photo__actions').exists()).toBe(false)
    expect(locked.findAll('input[type="file"]').every((input) => input.attributes('disabled') !== undefined)).toBe(true)
  })

  it('uploads a gallery selection and ignores a cancelled picker', async () => {
    const wrapper = render()
    const gallery = wrapper.findAll('input[type="file"]')[1]!
    await gallery.trigger('change')
    expect(mocks.upload).not.toHaveBeenCalled()
    const file = new File(['photo'], 'portrait.jpg', { type: 'image/jpeg' })
    Object.defineProperty(gallery.element, 'files', { value: [file], configurable: true })
    await gallery.trigger('change')
    await flushPromises()
    expect(mocks.upload).toHaveBeenCalledWith('avatar', file, expect.any(Function), expect.any(AbortSignal))
    expect(wrapper.emitted('uploaded')).toEqual([[imported]])
    expect(mocks.importAvatar).not.toHaveBeenCalled()
  })
})

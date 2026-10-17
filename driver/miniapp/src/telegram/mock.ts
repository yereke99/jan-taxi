// Shared with the other Mini App — keep both copies identical (make verify-shared).
//
// Imitation of the Telegram iOS client for the presentation build (src/demo).
// Nothing in the production app imports this file. It answers the Mini Apps
// methods the app uses: viewport, safe areas (iPhone: top 59 / bottom 34 plus
// the 46px header row in fullscreen), fullscreen, white theme and the
// LocationManager, which reports the simulated device position.

import { emitEvent, mockTelegramEnv } from '@tma.js/sdk'

const THEME = {
  bg_color: '#ffffff',
  secondary_bg_color: '#f2f2f7',
  section_bg_color: '#ffffff',
  header_bg_color: '#ffffff',
  bottom_bar_bg_color: '#ffffff',
  text_color: '#000000',
  hint_color: '#8e8e93',
  subtitle_text_color: '#8e8e93',
  section_header_text_color: '#6d6d72',
  link_color: '#007aff',
  accent_text_color: '#007aff',
  button_color: '#000000',
  button_text_color: '#ffffff',
  destructive_text_color: '#ff3b30',
  section_separator_color: '#c8c7cc',
}

export interface TelegramMockOptions {
  user: { id: number; first_name: string; last_name: string; username: string; language_code: string }
  startParam: string | null
  /** iPhone safe areas; false emulates a desktop window without insets. */
  phone: boolean
  /** Current device position for LocationManager requests. */
  location: () => { lat: number; lon: number; accuracy_m: number }
}

export function installTelegramMock(opts: TelegramMockOptions): void {
  const initData = new URLSearchParams({
    user: JSON.stringify({ ...opts.user, allows_write_to_pm: true, is_premium: false }),
    chat_instance: '-1',
    chat_type: 'private',
    auth_date: String(Math.floor(Date.now() / 1000)),
    signature: 'demo',
    hash: 'demo',
  })
  const lp = new URLSearchParams({
    tgWebAppPlatform: 'ios',
    tgWebAppVersion: '8.0',
    tgWebAppFullscreen: '1',
    tgWebAppThemeParams: JSON.stringify(THEME),
    tgWebAppData: initData.toString(),
  })
  if (opts.startParam) lp.set('tgWebAppStartParam', opts.startParam)

  const safe = opts.phone ? { top: 59, right: 0, bottom: 34, left: 0 } : { top: 0, right: 0, bottom: 0, left: 0 }
  const content = opts.phone ? { top: 46, right: 0, bottom: 0, left: 0 } : { top: 0, right: 0, bottom: 0, left: 0 }
  const viewport = () => ({ height: window.innerHeight, width: window.innerWidth, is_expanded: true, is_state_stable: true })
  const reply = (name: string, payload: unknown): void => {
    setTimeout(() => (emitEvent as (n: string, p?: unknown) => void)(name, payload), 0)
  }

  mockTelegramEnv({
    launchParams: lp,
    resetPostMessage: true,
    onEvent(event) {
      switch (event.name) {
        case 'web_app_request_viewport':
          reply('viewport_changed', viewport())
          break
        case 'web_app_request_safe_area':
          reply('safe_area_changed', safe)
          break
        case 'web_app_request_content_safe_area':
          reply('content_safe_area_changed', content)
          break
        case 'web_app_request_fullscreen':
          reply('fullscreen_changed', { is_fullscreen: true })
          break
        case 'web_app_request_theme':
          reply('theme_changed', { theme_params: THEME })
          break
        case 'web_app_check_location':
          reply('location_checked', { available: true, access_requested: true, access_granted: true })
          break
        case 'web_app_request_location': {
          const p = opts.location()
          reply('location_requested', {
            available: true,
            latitude: p.lat,
            longitude: p.lon,
            altitude: null,
            course: null,
            speed: null,
            horizontal_accuracy: p.accuracy_m,
            vertical_accuracy: null,
            course_accuracy: null,
            speed_accuracy: null,
          })
          break
        }
        case 'web_app_setup_back_button': {
          // Lets screenshots draw Telegram's own "back" control like the client does.
          const visible = (event.params as { is_visible?: boolean } | undefined)?.is_visible === true
          document.documentElement.toggleAttribute('data-demo-tg-back', visible)
          break
        }
        case 'web_app_open_link':
        case 'web_app_open_tg_link':
          console.info('[demo] open link', event.params)
          break
        default:
          break
      }
    },
  })
  window.addEventListener('resize', () => reply('viewport_changed', viewport()))
}

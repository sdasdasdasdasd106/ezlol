import type { AugBuildResponse, AugPick, Build, Champion, Research, ChampionInfo, Game, Mastery, PlayRecord, SettingsView, Status, UpdateInfo } from './types'

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, { headers: { 'Content-Type': 'application/json' }, ...init })
  const body = await res.json().catch(() => ({}))
  if (!res.ok) throw new Error(body.error ?? `${res.status} ${res.statusText}`)
  return body as T
}

export const api = {
  status: () => req<Status & { ddVersion: string }>('/api/status'),
  champions: () => req<{ version: string; champions: Champion[] }>('/api/champions'),
  build: (key: string) => req<Build>(`/api/champions/${encodeURIComponent(key)}/build`),
  game: () => req<Game>('/api/game'),
  /** Read the augment cards on screen now (the scan hotkey / button). status ai = the AI is reading the text. */
  scanNow: () => req<{ status: 'ok' | 'ai'; best?: string; names?: string[]; message?: string }>('/api/game/scan', { method: 'POST' }),
  correctPick: (stage: number, augmentId: number) => req<AugPick[]>('/api/game/pick', { method: 'POST', body: JSON.stringify({ stage, augmentId }) }),
  augmentBuild: (champion: number, augments: number[], owned: number[]) =>
    req<AugBuildResponse>(`/api/champions/${champion}/augment-build?augments=${augments.join(',')}&owned=${owned.join(',')}`),
  openLogs: () => req<{ dir: string }>('/api/logs/open', { method: 'POST' }),
  aiResearch: (champion: number, start = false) => req<Research | { status: 'off' }>(`/api/ai/research?champion=${champion}${start ? '&start=1' : ''}`),
  // testing aid: push the logs to the repository's test-logs branch
  uploadLogs: () => req<{ uploaded: number; skipped: number; url: string }>('/api/logs/upload', { method: 'POST' }),
  info: (key: string) => req<{ champion: Champion; info?: ChampionInfo }>(`/api/champions/${encodeURIComponent(key)}/info`),
  settings: () => req<SettingsView>('/api/settings'),
  saveSettings: (patch: Record<string, unknown>) => req<SettingsView>('/api/settings', { method: 'PUT', body: JSON.stringify(patch) }),
  update: () => req<UpdateInfo>('/api/update'),
  checkUpdate: () => req<UpdateInfo>('/api/update/check', { method: 'POST' }),
  mastery: () => req<Record<string, Mastery>>('/api/mastery'),
  me: (championId: number) => req<{ mastery: Mastery | null; record: PlayRecord | null; mode: string }>(`/api/me/${championId}`),
}

/** Live client status over SSE (connection, phase, queue, picked champion). */
export function subscribe(onStatus: (s: Status) => void): () => void {
  const es = new EventSource('/api/events')
  es.addEventListener('status', (e) => onStatus(JSON.parse((e as MessageEvent).data)))
  return () => es.close()
}

export function winrate(c: { games: number; wins: number }): string {
  return c.games ? `${Math.round((c.wins / c.games) * 100)}%` : '–'
}


/** Desktop-only settings owned by the Electron shell (not the Go backend). */
export interface Hotkeys {
  /** read the augment cards on screen now */
  scan: string
  pick1: string
  pick2: string
  pick3: string
  toggle: string
}

export type OverlayPos = 'bottom-left' | 'top-left' | 'left-middle' | 'top-right' | 'bottom-right'

export interface AppSettings {
  launchAtLogin: boolean
  startInTray: boolean
  overlay: boolean
  overlayPos: OverlayPos
  hotkeys: Hotkeys
  /** hotkeys that could not be registered (taken by another program) */
  hotkeyErrors?: string[]
  loginItemSupported: boolean
  platform: string
  packaged: boolean
}

declare global {
  interface Window {
    ezlol?: {
      electron: boolean
      platform: string
      onUpdate?: (fn: (state: { status: string; version?: string; error?: string; progress?: number }) => void) => void
      installUpdate?: () => Promise<boolean>
      checkUpdate?: () => Promise<{ status: string; version?: string; error?: string; progress?: number }>
      getAppSettings?: () => Promise<AppSettings>
      setAppSettings?: (patch: Partial<Pick<AppSettings, 'launchAtLogin' | 'startInTray' | 'overlay' | 'overlayPos' | 'hotkeys'>>) => Promise<AppSettings>
      onAppSettings?: (fn: (s: AppSettings) => void) => void
      previewOverlay?: () => Promise<boolean>
      suspendHotkeys?: (on: boolean) => Promise<AppSettings>
      resetHotkeys?: () => Promise<AppSettings>
      onGameRefresh?: (fn: () => void) => void
      /** Only inside the overlay window. */
      overlay?: {
        want: (state: 'offer' | 'game' | 'info' | 'pinned' | 'none', height: number) => void
        onMode: (fn: (m: 'auto' | 'pinned' | 'hidden') => void) => void
        onHotkeys: (fn: (h: Hotkeys) => void) => void
        onFlash: (fn: (text: string) => void) => void
        onPreview: (fn: () => void) => void
        onRefresh: (fn: () => void) => void
      }
    }
  }
}
export const isElectron = () => !!window.ezlol?.electron

export function fmtPoints(n: number): string {
  return n >= 1000 ? `${Math.round(n / 1000)}k` : String(n)
}


/** A hotkey accelerator for people: CommandOrControl+Alt+S -> Ctrl+Alt+S. */
export function prettyHotkey(k: string | undefined): string {
  if (!k) return ''
  const mac = window.ezlol?.platform === 'darwin'
  return k
    .replace('CommandOrControl', mac ? 'Cmd' : 'Ctrl')
    .replace('Control', 'Ctrl')
    .replace(/num(\d)/, '小键盘$1')
}

/** The default hotkeys, for display before the desktop shell reports its own. */
export const DEFAULT_HOTKEYS: Hotkeys = { scan: 'CommandOrControl+Alt+S', pick1: 'CommandOrControl+Alt+1', pick2: 'CommandOrControl+Alt+2', pick3: 'CommandOrControl+Alt+3', toggle: 'CommandOrControl+Alt+H' }

/**
 * Turns a key press into an accelerator ("CommandOrControl+Alt+S"), or explains
 * why it cannot be one. Uses KeyboardEvent.code, which names the physical key:
 * KeyboardEvent.key gives "!" for Shift+1 and "Process" while a Chinese input
 * method is on. Mirrors normalizeHotkey in electron/main.js.
 */
export function hotkeyFromEvent(e: KeyboardEvent): { accel?: string; error?: string; partial?: boolean } {
  const mac = window.ezlol?.platform === 'darwin'
  const mods: string[] = []
  if (mac ? e.metaKey : e.ctrlKey) mods.push('CommandOrControl')
  if (mac && e.ctrlKey) mods.push('Control')
  if (e.altKey) mods.push('Alt')
  if (e.shiftKey) mods.push('Shift')
  const c = e.code
  let key = ''
  let m: RegExpMatchArray | null
  if ((m = c.match(/^Key([A-Z])$/))) key = m[1]
  else if ((m = c.match(/^Digit(\d)$/))) key = m[1]
  else if ((m = c.match(/^(F(?:[1-9]|1\d|2[0-4]))$/))) key = m[1]
  else if ((m = c.match(/^Numpad(\d)$/))) key = 'num' + m[1]
  else if (/^(Control|Alt|Shift|Meta|OS)(Left|Right)?$/.test(c) || ['Control', 'Alt', 'Shift', 'Meta'].includes(e.key)) return { partial: true }
  else return { error: '这个键不能用：请用字母、数字、F1–F11 或小键盘数字' }
  const standalone = /^F\d+$/.test(key) || key.startsWith('num')
  if (!standalone && !mods.includes('CommandOrControl') && !mods.includes('Control') && !mods.includes('Alt')) return { error: '字母和数字要和 Ctrl 或 Alt 一起按（不然打字时也会触发）' }
  if (key === 'F12' && !mac) return { error: 'Windows 不让程序占用 F12' }
  return { accel: [...mods, key].join('+') }
}

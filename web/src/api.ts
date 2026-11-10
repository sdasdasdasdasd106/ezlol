import type { AugBuildResponse, AugPick, Build, Champion, ChampionInfo, Game, Mastery, PlayRecord, SettingsView, Status, UpdateInfo } from './types'

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
  correctPick: (stage: number, augmentId: number) => req<AugPick[]>('/api/game/pick', { method: 'POST', body: JSON.stringify({ stage, augmentId }) }),
  augmentBuild: (champion: number, augments: number[], owned: number[]) =>
    req<AugBuildResponse>(`/api/champions/${champion}/augment-build?augments=${augments.join(',')}&owned=${owned.join(',')}`),
  openLogs: () => req<{ dir: string }>('/api/logs/open', { method: 'POST' }),
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
      /** Only inside the overlay window. */
      overlay?: {
        want: (state: 'offer' | 'info' | 'pinned' | 'none', height: number) => void
        onMode: (fn: (m: 'auto' | 'pinned' | 'hidden') => void) => void
        onHotkeys: (fn: (h: Hotkeys) => void) => void
        onFlash: (fn: (text: string) => void) => void
        onPreview: (fn: () => void) => void
      }
    }
  }
}
export const isElectron = () => !!window.ezlol?.electron

export function fmtPoints(n: number): string {
  return n >= 1000 ? `${Math.round(n / 1000)}k` : String(n)
}


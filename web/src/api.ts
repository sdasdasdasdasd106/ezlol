import type { AugBuildResponse, AugPick, Build, BuildsStatus, RunePage, Champion, ChampionInfo, Game, Mastery, PlayRecord, SettingsView, Status, UpdateInfo } from './types'

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, { headers: { 'Content-Type': 'application/json' }, ...init })
  const body = await res.json().catch(() => ({}))
  if (!res.ok) throw new Error(body.error ?? `${res.status} ${res.statusText}`)
  return body as T
}

export const api = {
  status: () => req<Status & { ddVersion: string }>('/api/status'),
  champions: () => req<{ version: string; champions: Champion[] }>('/api/champions'),
  build: (key: string, role?: string, mode?: string) => {
    const q = new URLSearchParams()
    if (role) q.set('role', role)
    if (mode) q.set('mode', mode)
    const qs = q.toString()
    return req<Build>(`/api/champions/${encodeURIComponent(key)}/build${qs ? `?${qs}` : ''}`)
  },
  buildsStatus: () => req<BuildsStatus>('/api/builds/status'),
  compile: (queue: 'ranked' | 'aram') => req<unknown>(`/api/builds/compile?queue=${queue}`, { method: 'POST' }),
  compileStop: () => req<unknown>('/api/builds/compile/stop', { method: 'POST' }),
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
  applyRunes: (championId: number, page: RunePage) =>
    req<{ ok: boolean }>('/api/runes/apply', { method: 'POST', body: JSON.stringify({ championId, page }) }),
  me: (championId: number, mode?: string) =>
    req<{ mastery: Mastery | null; record: PlayRecord | null; mode: string }>(`/api/me/${championId}${mode ? `?mode=${mode}` : ''}`),
}

/** Live client status over SSE (connection, phase, queue, picked champion). */
export function subscribe(onStatus: (s: Status) => void): () => void {
  const es = new EventSource('/api/events')
  es.addEventListener('status', (e) => onStatus(JSON.parse((e as MessageEvent).data)))
  return () => es.close()
}

export const ROLES = ['TOP', 'JUNGLE', 'MIDDLE', 'BOTTOM', 'UTILITY'] as const
export const ROLE_LABEL: Record<string, string> = {
  TOP: '上单',
  JUNGLE: '打野',
  MIDDLE: '中单',
  BOTTOM: '下路',
  UTILITY: '辅助',
  UNKNOWN: '?',
}

export function winrate(c: { games: number; wins: number }): string {
  return c.games ? `${Math.round((c.wins / c.games) * 100)}%` : '–'
}


/** Desktop-only settings owned by the Electron shell (not the Go backend). */
export interface AppSettings {
  launchAtLogin: boolean
  startInTray: boolean
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
      setAppSettings?: (patch: Partial<Pick<AppSettings, 'launchAtLogin' | 'startInTray'>>) => Promise<AppSettings>
      onAppSettings?: (fn: (s: AppSettings) => void) => void
    }
  }
}
export const isElectron = () => !!window.ezlol?.electron

export function fmtPoints(n: number): string {
  return n >= 1000 ? `${Math.round(n / 1000)}k` : String(n)
}


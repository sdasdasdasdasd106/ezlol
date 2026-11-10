import { reactive } from 'vue'
import { api } from './api'
import { toast } from './toast'
import type { Build, Champion, ChampionInfo, Mastery, PlayRecord, RunePage } from './types'

/** '' = follow the client's current queue; otherwise the build mode is forced. */
export type BuildMode = '' | 'sr' | 'aram' | 'arena'

export interface BuildState {
  champion: Champion | null
  build: Build | null
  role: string
  mode: BuildMode
  loading: boolean
  loadingChamp: Champion | null
  error: string
  me: { mastery: Mastery | null; record: PlayRecord | null } | null
  info: ChampionInfo | null
  applying: boolean
  expandAugs: boolean
  /** Augments picked in the lists to preview "the build after taking these" (ARAM). */
  augPreview: number[]
  /** Mode of the running game (from the game itself), used when no mode is forced. */
  autoMode: BuildMode
}

/**
 * Build state shared by the champion bar (which drives it) and the build
 * widgets (which only read it). It lives outside the widget grid, so switching
 * screens never refetches the build.
 */
export const buildState = reactive<BuildState>({
  champion: null,
  build: null,
  role: '',
  mode: '',
  loading: false,
  loadingChamp: null,
  error: '',
  me: null,
  info: null,
  applying: false,
  expandAugs: false,
  augPreview: [],
  autoMode: '',
})

// Requests can overlap (rapid champion switches); only the newest may write state.
let seq = 0

export async function loadBuild(champion: Champion | null = buildState.champion): Promise<void> {
  const s = buildState
  if (s.champion?.id !== champion?.id) s.augPreview = []
  s.champion = champion
  if (!champion) {
    seq++ // drop any in-flight response
    s.build = null
    s.me = null
    s.info = null
    s.loading = false
    s.loadingChamp = null
    s.error = ''
    return
  }
  const my = ++seq
  s.loading = true
  s.loadingChamp = champion
  s.error = ''
  try {
    const b = await api.build(champion.key, s.role === 'NONE' ? undefined : s.role || undefined, s.mode || s.autoMode || undefined)
    if (my !== seq) return
    s.build = b
    s.role = b.role
    api
      .me(champion.id, b.mode)
      .then((m) => {
        if (my === seq) s.me = m
      })
      .catch(() => {
        if (my === seq) s.me = null
      })
    api
      .info(champion.key)
      .then((r) => {
        if (my === seq) s.info = r.info ?? null
      })
      .catch(() => {
        if (my === seq) s.info = null
      })
  } catch (e) {
    if (my !== seq) return
    // Never leave the previous champion's build on screen under the new name.
    if (s.build && s.build.champion.id !== champion.id) {
      s.build = null
      s.me = null
      s.info = null
    }
    s.error = (e as Error).message
    toast({ key: 'build', kind: 'error', title: '获取出装失败', body: s.error })
  } finally {
    if (my === seq) s.loading = false
  }
}

export function pickRole(r: string): void {
  buildState.role = r
  void loadBuild()
}

export function pickMode(m: BuildMode): void {
  buildState.mode = m
  // Lane only means something on the Rift; drop ARAM's "NONE" and let the server pick.
  if (m !== 'sr' || buildState.role === 'NONE') buildState.role = ''
  void loadBuild()
}

export async function applyRunes(p: RunePage): Promise<void> {
  const s = buildState
  if (!s.build) return
  s.applying = true
  try {
    await api.applyRunes(s.build.champion.id, p)
    toast({ key: 'runes', kind: 'success', title: '符文已设置', body: `${s.build.champion.name}：${p.primary.name} + ${p.secondary.name}，已在客户端中选中。` })
  } catch (e) {
    toast({ key: 'runes', kind: 'error', title: '设置符文失败', body: (e as Error).message })
  } finally {
    s.applying = false
  }
}

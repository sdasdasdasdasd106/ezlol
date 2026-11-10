import { reactive } from 'vue'
import { api } from './api'
import { toast } from './toast'
import type { Build, Champion, ChampionInfo, Mastery, PlayRecord } from './types'

export interface BuildState {
  champion: Champion | null
  build: Build | null
  loading: boolean
  loadingChamp: Champion | null
  error: string
  me: { mastery: Mastery | null; record: PlayRecord | null } | null
  info: ChampionInfo | null
  expandAugs: boolean
  /** Augments picked in the lists to preview "the build after taking these". */
  augPreview: number[]
}

/**
 * Build state shared by the champion bar (which drives it) and the build
 * widgets (which only read it). It lives outside the widget grid, so switching
 * screens never refetches the build.
 */
export const buildState = reactive<BuildState>({
  champion: null,
  build: null,
  loading: false,
  loadingChamp: null,
  error: '',
  me: null,
  info: null,
  expandAugs: false,
  augPreview: [],
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
    const b = await api.build(champion.key)
    if (my !== seq) return
    s.build = b
    api
      .me(champion.id)
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

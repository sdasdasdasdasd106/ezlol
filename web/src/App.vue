<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, provide, ref, watch } from 'vue'
import { CATALOG, DASH_KEY, DashboardGrid, type ModeKey } from './dashboard'
import GhostWidget from './components/GhostWidget.vue'
import { api, isElectron, subscribe } from './api'
import * as sound from './sound'
import type { Champion, Game, Status } from './types'
import BuildPanel from './components/BuildPanel.vue'
import ChampionBar from './components/ChampionBar.vue'
import AugmentAssist from './components/AugmentAssist.vue'
import AugmentBuild from './components/AugmentBuild.vue'
import StrategyCard from './components/StrategyCard.vue'
import SettingsModal from './components/SettingsModal.vue'
import Toasts from './components/Toasts.vue'
import { toast } from './toast'
import { say, setVoice, useGameVoice } from './voice'
import type { UpdateInfo } from './types'

const params = new URLSearchParams(location.search)
const status = ref<Status | null>(null)
const game = ref<Game | null>(null)
const champions = ref<Champion[]>([])
const selected = ref<Champion | null>(null)
const followPick = ref(true)
const showSettings = ref(false)
if (params.get('open') === 'settings') setTimeout(() => (showSettings.value = true), 800)
const upd = ref<UpdateInfo | null>(null)
const electronUpdate = ref<{ status: string; version?: string; error?: string; progress?: number } | null>(null)
const installUpdate = () => window.ezlol?.installUpdate?.()
let announcedUpdate = ''
async function checkUpdateSoon() {
  try {
    upd.value = await api.update()
    if (!upd.value.checkedAt || upd.value.checkedAt.startsWith('0001')) upd.value = await api.checkUpdate()
    const u = upd.value
    if (u.hasUpdate && announcedUpdate !== u.latest && electronUpdate.value?.status !== 'downloading' && electronUpdate.value?.status !== 'downloaded') {
      announcedUpdate = u.latest
      toast({ key: 'update', kind: 'info', sticky: true, title: `ezlol 有新版本 ${u.latest}`, body: `当前版本 ${u.current}。`, actions: [{ label: '下载', primary: true, run: () => window.open(u.url, '_blank') }] })
    }
  } catch {
    /* offline */
  }
}
watch(electronUpdate, (st) => {
  if (!st) return
  if (st.status === 'downloaded') toast({ key: 'update', kind: 'success', sticky: true, title: `ezlol ${st.version} 已下载`, body: '已下载并校验，安装后会自动重启。', actions: [{ label: '安装并重启', primary: true, run: installUpdate }] })
  else if (st.status === 'downloading') toast({ key: 'update', kind: 'info', sticky: true, title: `正在下载 ${st.version}…`, body: st.progress ? `${Math.round(st.progress * 100)}%` : undefined })
  else if (st.status === 'error') toast({ key: 'update', kind: 'warn', title: '自动更新失败', body: `${st.error ?? ''}——请手动下载。`, actions: upd.value?.url ? [{ label: '下载', run: () => window.open(upd.value!.url, '_blank') }] : undefined })
})
const electron = isElectron()
// Page mode drives the layout: idle (lobby), select (champion select), game.
const forcedMode = params.get('mode') // dev: ?mode=game|select|idle
const mode = computed(() => {
  if (forcedMode === 'game' || forcedMode === 'select' || forcedMode === 'idle') return forcedMode
  const p = status.value?.phase
  if (p === 'InProgress' || game.value?.inGame) return 'game'
  if (p === 'ChampSelect') return 'select'
  return 'idle'
})
const error = ref('')
watch(error, (e) => e && toast({ key: 'app-error', kind: 'error', title: 'ezlol', body: e }))

let unsub: (() => void) | null = null

function onStatus(s: Status) {
  status.value = s
  if (followPick.value && s.pickedChampion) {
    const c = champions.value.find((x) => x.id === s.pickedChampion)
    if (c && selected.value?.id !== c.id) selected.value = c
  }
}

// In-game poll: your champion (the build follows it) and the augment offer.
// Fast while a game runs, slow otherwise; the endpoint is local and cheap.
let gameTimer: number | undefined
let polling = false
let again = false
async function pollGame() {
  if (polling) {
    again = true
    return
  }
  polling = true
  clearTimeout(gameTimer)
  try {
    game.value = await api.game()
  } catch {
    game.value = null
  } finally {
    polling = false
  }
  const me = game.value?.me?.champion
  if (followPick.value && me?.id && selected.value?.id !== me.id) {
    const c = champions.value.find((x) => x.id === me.id)
    if (c) selected.value = c
  }
  if (again) {
    again = false
    void pollGame()
    return
  }
  gameTimer = window.setTimeout(pollGame, game.value?.inGame ? 1500 : 4000)
}
// A hotkey (scan, pick) changed the game state: show it now.
window.ezlol?.onGameRefresh?.(() => void pollGame())
// What a hotkey did: a toast for when the player switches out, spoken if asked.
window.ezlol?.onHotkeyResult?.((text, speak) => {
  toast({ key: 'hotkey', kind: speak ? 'error' : 'info', title: text, ttl: 8000 })
  if (speak) say(text.split(/[（(]/)[0].slice(0, 40), { interrupt: true })
})
// Optional voice (Settings): the recommendation and the next item, spoken.
window.ezlol?.getAppSettings?.().then((a) => setVoice(!!a.voice)).catch(() => {})
window.ezlol?.onAppSettings?.((a) => setVoice(!!a.voice))
window.ezlol?.onSay?.((text, force) => say(text, { interrupt: true, force }))
useGameVoice(game, async (champ, augs, owned) => (await api.augmentBuild(champ, augs, owned)).plan.next?.[0]?.item.name ?? '')

onMounted(async () => {
  try {
    const [st, ch] = await Promise.all([api.status(), api.champions()])
    champions.value = ch.champions
    onStatus(st)
    const saved = localStorage.getItem('ezlol.champion')
    if (!selected.value && saved) selected.value = champions.value.find((c) => c.key === saved) ?? null
  } catch (e) {
    error.value = (e as Error).message
  }
  unsub = subscribe(onStatus)
  void pollGame()
  if (!params.get('quiet')) {
    setTimeout(checkUpdateSoon, 4000)
    setInterval(checkUpdateSoon, 6 * 3600 * 1000)
  }
  window.ezlol?.onUpdate?.((st) => (electronUpdate.value = st))
  sound.notify('', '') // triggers the permission prompt once
})
onUnmounted(() => {
  unsub?.()
  clearTimeout(gameTimer)
})

// Widget dashboard: one grid per screen; widgets register on mount.
const modeKey = computed<ModeKey>(() => mode.value)
const dash = new DashboardGrid(() => modeKey.value)
provide(DASH_KEY, dash)
const mainEl = ref<HTMLElement | null>(null)
const editing = ref(false)
// Ghost slots: every catalogued box for this screen that is not mounted right now.
const ghosts = computed(() => (editing.value ? CATALOG[modeKey.value].filter((w) => !dash.mounted.has(w.id)) : []))
const layoutVersion = ref(0)
function attachGrid() {
  if (mainEl.value) dash.attach(mainEl.value)
}
onMounted(attachGrid)
watch([modeKey, layoutVersion], async () => {
  await nextTick()
  attachGrid()
})
watch(editing, (v) => dash.setEditing(v))
function resetLayout() {
  const hadPreset = dash.hasPreset()
  dash.reset()
  layoutVersion.value++
  toast({ key: 'layout', kind: 'info', title: hadPreset ? '已恢复布局' : '已重置布局', body: hadPreset ? '回到你为这个界面保存的布局。' : '回到默认布局（这个界面没有保存过布局）。', ttl: 4000 })
}
const hasPreset = ref(false)
function saveLayout() {
  dash.savePreset()
  hasPreset.value = true
  toast({ key: 'layout', kind: 'success', title: '布局已保存', body: '之后点「重置」会回到这个布局。', ttl: 4000 })
}
function forgetLayout() {
  dash.clearPreset()
  hasPreset.value = false
  toast({ key: 'layout', kind: 'info', title: '已删除保存的布局', body: '「重置」会回到默认布局。', ttl: 4000 })
}
watch([editing, modeKey], () => (hasPreset.value = dash.hasPreset()))

function select(c: Champion) {
  selected.value = c
  localStorage.setItem('ezlol.champion', c.key)
}
</script>

<template>
  <div class="app">
    <header class="topbar">
      <div class="brand">ez<span>lol</span></div>
      <span v-if="status" class="pill" :class="status.connected ? 'ok' : 'bad'">
        <i class="dot" />{{ status.connected ? `${status.summoner || '已连接'}` : '客户端未运行' }}
      </span>
      <span v-if="status?.queueName" class="pill warn"><i class="dot" />{{ status.queueName }}</span>
      <div class="grow" />
      <template v-if="editing">
        <span class="muted">拖动面板调整位置和大小</span>
        <button class="primary" title="记住这个界面的当前布局" @click="saveLayout">保存布局</button>
        <button :title="hasPreset ? '回到你保存的布局' : '回到默认布局'" @click="resetLayout">重置</button>
        <button v-if="hasPreset" class="danger" title="删除这个界面保存的布局" @click="forgetLayout">删除保存</button>
      </template>
      <button class="gear" :class="{ active: editing }" :title="editing ? '完成调整' : '调整布局'" :aria-label="editing ? '完成调整' : '调整布局'" @click="editing = !editing">
        <svg viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <rect x="3" y="3" width="7" height="9" rx="1.5" /><rect x="14" y="3" width="7" height="5" rx="1.5" /><rect x="14" y="12" width="7" height="9" rx="1.5" /><rect x="3" y="16" width="7" height="5" rx="1.5" />
        </svg>
      </button>
      <button class="gear" title="设置" aria-label="设置" @click="showSettings = true">
        <svg viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <circle cx="12" cy="12" r="3" />
          <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z" />
        </svg>
      </button>
    </header>

    <Toasts />
    <SettingsModal v-if="showSettings" @close="showSettings = false" />

    <!-- Fixed bar, never part of the grid below: the champion card (picker as a dropdown) -->
    <div class="bars">
      <ChampionBar :champion="selected" :champions="champions" :status="status" :screen="mode" :compact="mode === 'game'" v-model:follow="followPick" @select="select" />
    </div>

    <div class="layout" :class="mode">
      <div ref="mainEl" :key="modeKey + ':' + layoutVersion" class="main grid-stack" :class="[mode, { editing }]">
        <!-- In game: the augment recommendation, then the build for the augments taken -->
        <AugmentAssist v-if="mode === 'game'" :game="game" @picks="(p) => game && (game.picks = p)" @refresh="pollGame" />
        <AugmentBuild :game="game" />
        <BuildPanel v-if="mode !== 'game'" />
        <StrategyCard v-if="mode !== 'game'" :game="game" :champion="selected" />
        <GhostWidget v-for="g in ghosts" :key="'ghost-' + g.id" :spec="g" />
      </div>
    </div>
  </div>
</template>

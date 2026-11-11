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
import ThemeMenu from './components/ThemeMenu.vue'
import type { ThemeOption } from './components/ThemeMenu.vue'
import { toast } from './toast'
import type { UpdateInfo } from './types'

const params = new URLSearchParams(location.search)
const status = ref<Status | null>(null)
const game = ref<Game | null>(null)
const champions = ref<Champion[]>([])
const selected = ref<Champion | null>(null)
const followPick = ref(true)
const soundOn = ref(sound.enabled())
const showSettings = ref(false)
const THEMES: ThemeOption[] = [
  { id: 'hextech', label: '海克斯', swatch: ['#010a13', '#c8aa6e', '#0ac8b9'] },
  { id: 'linear', label: '简洁深色', swatch: ['#0b0c10', '#141519', '#7c7cff'] },
  { id: 'neon', label: '霓虹', swatch: ['#05060d', '#ff2bd6', '#00f0ff'] },
  { id: 'synthwave', label: '合成波', swatch: ['#170a2b', '#ff2d95', '#ffd166'] },
  { id: 'terminal', label: '终端', swatch: ['#000000', '#1f3a1f', '#39ff14'] },
  { id: 'sketch', label: '手绘', swatch: ['#f6f1e7', '#2b2b2b', '#ffe066'] },
]
const themeParam = params.get('theme')
const theme = ref<string>(
  THEMES.some((t) => t.id === themeParam) ? (themeParam as string) : THEMES.some((t) => t.id === localStorage.getItem('ezlol.theme')) ? (localStorage.getItem('ezlol.theme') as string) : 'hextech',
)
if (params.get('open') === 'settings') setTimeout(() => (showSettings.value = true), 800)
watch(
  theme,
  (t) => {
    document.documentElement.dataset.theme = t
    localStorage.setItem('ezlol.theme', t)
  },
  { immediate: true },
)
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
async function pollGame() {
  try {
    game.value = await api.game()
  } catch {
    game.value = null
  }
  const me = game.value?.me?.champion
  if (followPick.value && me?.id && selected.value?.id !== me.id) {
    const c = champions.value.find((x) => x.id === me.id)
    if (c) selected.value = c
  }
  gameTimer = window.setTimeout(pollGame, game.value?.inGame ? 1500 : 4000)
}

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
watch(soundOn, (v) => sound.setEnabled(v))

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
      <span v-if="status?.patch" class="pill"><i class="dot" />版本 {{ status.patch }}</span>
      <span v-if="status?.queueName" class="pill warn"><i class="dot" />{{ status.queueName }}</span>
      <div class="grow" />
      <label class="toggle" :class="{ on: editing }" title="拖动、调整各个面板；每个界面的布局分开保存" @click="editing = !editing">
        <span class="track" /><span>编辑布局</span>
      </label>
      <button v-if="editing" class="primary" title="记住这个界面的当前布局" @click="saveLayout">保存布局</button>
      <button v-if="editing" :title="hasPreset ? '回到你保存的布局' : '回到默认布局'" @click="resetLayout">重置</button>
      <button v-if="editing && hasPreset" class="danger" title="删除这个界面保存的布局" @click="forgetLayout">删除保存</button>
      <label class="toggle" :class="{ on: soundOn }" @click="soundOn = !soundOn">
        <span class="track" /><span>提示音</span>
      </label>
      <ThemeMenu v-model="theme" :options="THEMES" />
      <button class="gear" title="设置" aria-label="设置" @click="showSettings = true">
        <svg viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
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
        <!-- In game (ARAM / Arena): the augment assistant first, build boxes after it -->
        <AugmentAssist v-if="mode === 'game'" :game="game" @picks="(p) => game && (game.picks = p)" />
        <AugmentBuild :game="game" />
        <BuildPanel :compact="mode === 'game'" />
        <StrategyCard :game="game" :champion="selected" />
        <GhostWidget v-for="g in ghosts" :key="'ghost-' + g.id" :spec="g" />
      </div>
    </div>
  </div>
</template>

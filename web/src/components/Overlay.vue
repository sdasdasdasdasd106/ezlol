<script setup lang="ts">
// Always-on-top mini window shown over the game (Electron only). It cannot be
// clicked (clicks pass through to the game), so everything it needs is shown
// at a glance and actions go through the global hotkeys.
// It tells the main process when it has something to show ("want"):
//   offer  augments read from the screen (scan hotkey): which to take
//   game   in a game: the build (start items, core, next item) and augments taken
//   info   a hotkey message outside a game, for a few seconds
//   pinned shown on demand (hotkey) outside a game
//   none   hide
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { api, DEFAULT_HOTKEYS, prettyHotkey } from '../api'
import type { Hotkeys } from '../api'
import type { AugBuildResponse, Build, Game, ItemRef, OfferCard } from '../types'

const game = ref<Game | null>(null)
const plan = ref<AugBuildResponse | null>(null)
const build = ref<Build | null>(null)
const mode = ref<'auto' | 'pinned' | 'hidden'>('auto')
const hotkeys = ref<Hotkeys>({ ...DEFAULT_HOTKEYS })
const flash = ref('') // hotkey feedback
const preview = ref(false)
const infoUntil = ref(0)
const root = ref<HTMLElement | null>(null)

const STAGE = ['', '开局', '7 级', '11 级', '15 级']
const STYLE: Record<string, string> = { crit: '暴击流', on_hit: '攻速特效流', ap: '法强流', hybrid: '混伤流', lethality: '穿甲流', tank: '坦克流', bruiser: '战士流', ad: '物理流' }
const pct = (x: number) => `${(x * 100).toFixed(1)}%`
const signed = (x: number) => `${x >= 0 ? '+' : ''}${(x * 100).toFixed(1)}%`
const pretty = prettyHotkey
// "Ctrl+Alt+1/2/3" when the three keys differ only in the digit.
const pickKeys = computed(() => {
  const [a, b, c] = [hotkeys.value.pick1, hotkeys.value.pick2, hotkeys.value.pick3].map(pretty)
  return a.endsWith('1') && b === a.slice(0, -1) + '2' && c === a.slice(0, -1) + '3' ? `${a}/2/3` : `${a} / ${b} / ${c}`
})

// Preview data so the position and size can be checked before a game.
const DEMO: Game = {
  inGame: true, gameTime: 420, mapId: 12, mode: 'aram', patch: '', ocr: 'available', ocrLang: 'zh-Hans-CN',
  me: { name: '', champion: { id: 22, key: 'Ashe', name: '寒冰射手', title: '艾希', tags: [], image: '', attack: 0, magic: 0 }, team: '', isDead: true, level: 7, items: [] },
  picks: [],
  offer: {
    active: true, pending: false, level: 7, stage: 2, best: '大法师', note: '「大法师」明显更好，比第二名高 4.0%。', screen: [2087, 3131, 1077],
    offered: [
      { id: 2087, name: '大法师', rarity: 'prismatic', stageWinRate: 0.528, stageGames: 23295, lift: 0.099, reasons: ['7 级选它胜率 52.8%', '注意：选它的人大多走法强流，和你已经出的装备不是一个路子'], fitOk: true } as OfferCard,
      { id: 3131, name: '屹立不倒', rarity: 'gold', stageWinRate: 0.471, stageGames: 15094, lift: 0.042, reasons: ['契合你现在的装备：这些装备配它胜率 +2.7%'], fitOk: true } as OfferCard,
      { id: 1077, name: '灵魂虹吸', rarity: 'gold', stageWinRate: 0.47, stageGames: 11766, lift: 0.041, reasons: ['契合你现在的装备：这些装备配它胜率 +2.3%'], fitOk: true } as OfferCard,
    ],
  },
}
const g = computed(() => (preview.value ? DEMO : game.value))
const aram = computed(() => !!g.value?.inGame && g.value.mode === 'aram')
const offer = computed(() => (g.value?.offer?.active ? g.value.offer : null))
// Cards in screen order (left to right), so 1/2/3 match the hotkeys.
const cards = computed(() => {
  const o = offer.value
  if (!o?.offered) return []
  const by = new Map(o.offered.map((c) => [c.id, c]))
  const order = o.screen?.length ? o.screen : o.offered.map((c) => c.id)
  return order.map((id) => by.get(id)).filter(Boolean) as OfferCard[]
})
const bestId = computed(() => offer.value?.offered?.[0]?.id)
const picks = computed(() => g.value?.picks ?? [])
const lastPick = computed(() => (picks.value.length ? picks.value[picks.value.length - 1] : null))
// A pick is due (level 1/7/11/15 reached) and not read yet.
const due = computed(() => {
  const o = g.value?.offer
  return o?.pending && !o.active ? o : null
})
const problem = computed(() => {
  const o = g.value?.ocr ?? ''
  if (!g.value?.inGame || o === 'available' || o === '' || o === 'disabled') return ''
  return o === 'unavailable' ? '这台电脑用不了屏幕识别，强化只能在主窗口里看' : o
})

// ---- Build: starting items early, then the core and the next item ----
const owned = computed(() => g.value?.me?.items ?? [])
const ownedSet = computed(() => new Set(owned.value))
const showStart = computed(() => (g.value?.gameTime ?? 0) < 150 && !owned.value.some((id) => (plan.value?.plan.cores ?? []).some((c) => c.items.some((it) => it.id === id))))
const starting = computed<ItemRef[]>(() => build.value?.starting?.[0]?.items ?? [])
const core = computed(() => {
  const c = plan.value?.plan.cores?.[0]
  if (c) return { items: c.items, boots: c.boots, style: STYLE[c.style] ?? c.style, winRate: c.winRate }
  const b = build.value
  if (!b?.core?.length) return null
  const s = b.core[0]
  return { items: s.items, boots: b.boots?.[0]?.items?.[0], style: '', winRate: s.games ? s.wins / s.games : 0 }
})
const next = computed(() => (plan.value?.plan.next ?? []).slice(0, 3))
const conflict = computed(() => (plan.value?.plan.direction?.conflict ? plan.value.plan.direction.note : ''))

// Build for this champion (start items; core when there are no aramkit routes).
watch(
  () => (aram.value && !preview.value ? g.value?.me?.champion.key ?? '' : ''),
  async (key) => {
    build.value = null
    if (!key) return
    try {
      build.value = await api.build(key)
    } catch {
      build.value = null
    }
  },
)
// Next item and core for the augments taken so far and the items you own.
let planSeq = 0
watch(
  () => (aram.value && !preview.value ? [g.value?.me?.champion.id, picks.value.map((p) => p.augmentId).join(','), [...owned.value].sort().join(',')].join('|') : ''),
  async (k) => {
    const my = ++planSeq
    const gm = g.value
    if (!k || !gm?.me?.champion.id) {
      plan.value = null
      return
    }
    try {
      const r = await api.augmentBuild(gm.me.champion.id, picks.value.map((p) => p.augmentId), owned.value)
      if (my === planSeq) plan.value = r
    } catch {
      if (my === planSeq) plan.value = null
    }
  },
)

const want = computed<'offer' | 'game' | 'info' | 'pinned' | 'none'>(() => {
  if (preview.value || offer.value) return 'offer'
  if (aram.value) return 'game'
  if (g.value?.inGame && (flash.value || Date.now() < infoUntil.value)) return 'info'
  if (!g.value?.inGame && flash.value) return 'info'
  return mode.value === 'pinned' ? 'pinned' : 'none'
})

function report() {
  const h = Math.ceil((root.value?.getBoundingClientRect().height ?? 0) + 2)
  window.ezlol?.overlay?.want(want.value, h)
}
watch(want, async () => {
  await nextTick()
  report()
})

let timer: number | undefined
let tick: number | undefined
// One poll at a time; a refresh asked for mid-poll runs right after it.
let polling = false
let again = false
async function poll() {
  if (polling) {
    again = true
    return
  }
  polling = true
  clearTimeout(timer)
  try {
    game.value = await api.game()
  } catch {
    game.value = null
  } finally {
    polling = false
  }
  if (again) {
    again = false
    void poll()
    return
  }
  timer = window.setTimeout(poll, game.value?.inGame ? 1000 : 3000)
}
let previewTimer: number | undefined
let flashTimer: number | undefined
onMounted(() => {
  document.documentElement.classList.add('overlay')
  void poll()
  // Re-evaluate time-based visibility and size.
  tick = window.setInterval(report, 1000)
  window.ezlol?.overlay?.onMode((m) => (mode.value = m))
  window.ezlol?.overlay?.onHotkeys((h) => (hotkeys.value = h))
  window.ezlol?.overlay?.onFlash((text) => {
    flash.value = text
    infoUntil.value = Date.now() + 6000
    clearTimeout(flashTimer)
    flashTimer = window.setTimeout(() => (flash.value = ''), 6000)
  })
  window.ezlol?.overlay?.onRefresh(() => void poll())
  window.ezlol?.overlay?.onPreview(() => {
    preview.value = true
    clearTimeout(previewTimer)
    previewTimer = window.setTimeout(() => (preview.value = false), 10000)
  })
  new ResizeObserver(report).observe(root.value!)
})
onUnmounted(() => {
  clearTimeout(timer)
  clearInterval(tick)
})
</script>

<template>
  <div ref="root" class="ov" :class="{ empty: want === 'none' }">
    <div v-if="flash" class="flash">{{ flash }}</div>
    <template v-if="offer">
      <div class="top">
        <span class="take">选「{{ offer.best }}」</span>
        <span class="muted">{{ STAGE[offer.stage] }}强化<template v-if="preview"> · 预览</template></span>
      </div>
      <div v-if="offer.note" class="note">{{ offer.note }}</div>
      <div v-if="offer.ai && offer.ai.status !== 'error'" class="ai" :class="{ agree: offer.ai.agree }">
        <template v-if="offer.ai.status === 'pending'">AI 复核中…</template>
        <template v-else><b>{{ offer.ai.agree ? 'AI 同意' : `AI 建议「${offer.ai.pick}」` }}</b>：{{ offer.ai.reason }}</template>
      </div>
      <div v-if="offer.via === 'ai'" class="note">这组是 AI 根据文字推断的，可能不准</div>
      <div v-for="(c, i) in cards" :key="c.id" class="row" :class="[c.rarity, { best: c.id === bestId, chosen: offer.chosen === i + 1 }]">
        <span class="slot">{{ i + 1 }}</span>
        <div class="body">
          <div class="name">
            {{ c.name }}<span v-if="c.id === bestId" class="rec">推荐</span><span v-if="offer.ai?.status === 'done' && !offer.ai.agree && offer.ai.pick === c.name" class="mark">AI</span><span v-if="offer.chosen === i + 1" class="mark">已标记</span>
            <span class="wr">{{ c.stageGames ? `${pct(c.stageWinRate)} ${signed(c.lift)}` : '数据少' }}</span>
          </div>
          <div v-for="(r, j) in c.reasons.slice(1, 3)" :key="j" class="why" :class="{ warn: r.startsWith('注意') || r.startsWith('和你现在的装备不太搭') }">{{ r }}</div>
        </div>
      </div>
      <div class="foot">选完按 {{ pickKeys }} 记下选的第几个（不按就记为推荐）· {{ pretty(hotkeys.scan) }} 重新识别 · {{ pretty(hotkeys.toggle) }} 隐藏</div>
    </template>

    <template v-else-if="aram && g?.me">
      <div class="top">
        <span class="champ">{{ g.me.champion.name }}</span>
        <span class="muted">{{ g.me.level }} 级<template v-if="core?.style"> · {{ core.style }}</template></span>
      </div>
      <div v-if="due" class="due">
        {{ STAGE[due.stage] }}强化待选：打开选择界面后按 <b>{{ pretty(hotkeys.scan) }}</b> 识别
      </div>
      <div v-if="problem" class="problem">{{ problem }}</div>
      <div v-if="conflict" class="problem">注意：{{ conflict }}</div>

      <div v-if="showStart && starting.length" class="line">
        <span class="lab">出门</span>
        <span v-for="(it, i) in starting" :key="i" class="it"><img v-if="it.image" :src="it.image" :alt="it.name" />{{ it.name }}</span>
      </div>
      <div v-if="next.length" class="line">
        <span class="lab">下一件</span>
        <span v-for="x in next" :key="x.item.id" class="it"><img v-if="x.item.image" :src="x.item.image" :alt="x.item.name" />{{ x.item.name }} <i>{{ pct(x.winRate) }}</i></span>
      </div>
      <div v-if="core" class="line">
        <span class="lab">核心</span>
        <span v-for="(it, i) in core.items" :key="i" class="it" :class="{ own: ownedSet.has(it.id) }"><span v-if="i" class="arrow">›</span><img v-if="it.image" :src="it.image" :alt="it.name" :title="it.name" /></span>
        <span v-if="core.boots" class="it" :class="{ own: ownedSet.has(core.boots.id) }"><span class="arrow">+</span><img v-if="core.boots.image" :src="core.boots.image" :alt="core.boots.name" :title="core.boots.name" /></span>
        <i v-if="core.winRate" class="wr2">胜率 {{ pct(core.winRate) }}</i>
      </div>
      <div v-if="!core && !next.length" class="muted">出装数据加载中…</div>

      <div v-if="picks.length" class="line picks">
        <span class="lab">强化</span>
        <span v-for="p in picks" :key="p.stage" class="aug" :class="[p.rarity, { assumed: p.assumed }]">{{ p.name }}<template v-if="p.assumed">?</template></span>
      </div>
      <div v-if="lastPick?.assumed" class="muted small">「{{ lastPick.name }}」是按推荐记的；不对就按 {{ pickKeys }}</div>
      <div class="foot">{{ pretty(hotkeys.scan) }} 识别强化 · {{ pretty(hotkeys.toggle) }} 隐藏小窗</div>
    </template>

    <template v-else-if="!flash">
      <div class="muted">ezlol 在后台；进入海克斯乱斗后这里显示出装，选强化时按 {{ pretty(hotkeys.scan) }} 识别。</div>
    </template>
  </div>
</template>

<style scoped>
.ov { width: 400px; box-sizing: border-box; padding: 10px 12px; color: #f0e6d2; font-size: 13px; line-height: 1.4;
  background: rgba(1, 10, 19, 0.86); border: 1px solid rgba(200, 170, 110, 0.7); border-radius: 6px; font-family: 'Microsoft YaHei UI', 'Microsoft YaHei', 'PingFang SC', sans-serif; }
.ov.empty { visibility: hidden; }
.muted { color: #a09b8c; font-size: 12px; }
.small { font-size: 11px; }
.top { display: flex; align-items: baseline; gap: 10px; margin-bottom: 2px; }
.take { font-size: 18px; font-weight: 700; color: #0ac8b9; }
.champ { font-size: 15px; font-weight: 700; color: #f0d48a; }
.note { font-size: 12px; color: #f0d48a; margin-bottom: 6px; }
.ai { font-size: 12px; margin-bottom: 4px; padding-left: 6px; border-left: 2px solid #f0d48a; color: #e6dcc4; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
.ai.agree { border-left-color: #0ac8b9; }
.row { display: flex; gap: 8px; padding: 5px 6px; margin-top: 4px; border: 1px solid rgba(120, 90, 40, 0.8); border-left-width: 3px; background: rgba(30, 35, 40, 0.6); }
.row.prismatic { border-left-color: #3fb4d8; } .row.gold { border-left-color: #c8aa6e; } .row.silver { border-left-color: #8b9bb0; }
.row.best { border-color: #0ac8b9; }
.row.chosen { outline: 2px solid #f0d48a; }
.slot { font-weight: 700; font-size: 16px; width: 18px; text-align: center; color: #c8aa6e; }
.body { flex: 1; min-width: 0; }
.name { font-weight: 700; display: flex; align-items: center; gap: 6px; }
.wr { margin-left: auto; font-weight: 400; font-size: 12px; color: #cdbe91; white-space: nowrap; }
.rec, .mark { font-size: 11px; font-weight: 400; padding: 0 4px; border: 1px solid #0ac8b9; color: #0ac8b9; }
.mark { border-color: #f0d48a; color: #f0d48a; }
.why { font-size: 11px; color: #a09b8c; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.why.warn { color: #e84057; }
.foot { margin-top: 6px; font-size: 11px; color: #7f7a6e; }
.flash { color: #f0d48a; font-weight: 700; margin-bottom: 4px; }
.problem { color: #e84057; margin-bottom: 4px; font-size: 12px; }
.due { color: #0ac8b9; margin-bottom: 4px; }
.due b { color: #f0d48a; }
.line { display: flex; flex-wrap: wrap; gap: 4px 8px; align-items: center; margin-top: 4px; }
.lab { flex: none; width: 3.2em; color: #a09b8c; font-size: 12px; }
.it { display: inline-flex; align-items: center; gap: 4px; }
.it img { width: 24px; height: 24px; border: 1px solid #785a28; }
.it.own img { opacity: 0.45; }
.it i, .wr2 { font-style: normal; color: #cdbe91; font-size: 11px; }
.wr2 { margin-left: auto; }
.arrow { color: #785a28; }
.aug { font-size: 12px; padding: 0 5px; border: 1px solid #785a28; }
.aug.prismatic { border-color: #3fb4d8; } .aug.gold { border-color: #c8aa6e; } .aug.silver { border-color: #8b9bb0; }
.aug.assumed { border-style: dashed; }
</style>

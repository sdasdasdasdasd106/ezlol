<script setup lang="ts">
// Always-on-top mini window shown over the game (Electron only). It cannot be
// clicked (clicks pass through to the game), so everything it needs is shown
// at a glance and corrections go through the global hotkeys.
// It tells the main process when it has something to show ("want"):
//   offer  an augment choice is on screen
//   info   just after a pick / a scan problem, for a few seconds
//   pinned shown on demand (hotkey) with the current state
//   none   hide
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { api } from '../api'
import type { AugBuildResponse, Game, OfferCard } from '../types'

const game = ref<Game | null>(null)
const plan = ref<AugBuildResponse | null>(null)
const mode = ref<'auto' | 'pinned' | 'hidden'>('auto')
const hotkeys = ref({ pick1: 'Ctrl+Shift+1', pick2: 'Ctrl+Shift+2', pick3: 'Ctrl+Shift+3', toggle: 'Ctrl+Shift+H' })
const flash = ref('') // hotkey feedback
const preview = ref(false)
const infoUntil = ref(0)
const root = ref<HTMLElement | null>(null)

const STAGE = ['', '开局', '7 级', '11 级', '15 级']
const pct = (x: number) => `${(x * 100).toFixed(1)}%`
const signed = (x: number) => `${x >= 0 ? '+' : ''}${(x * 100).toFixed(1)}%`
const pretty = (k: string) => k.replace('CommandOrControl', 'Ctrl').replace('Control', 'Ctrl')

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
const lastPick = computed(() => {
  const p = g.value?.picks ?? []
  return p.length ? p[p.length - 1] : null
})
const problem = computed(() => {
  const o = g.value?.ocr ?? ''
  if (!g.value?.inGame || o === 'available' || o === '' || o === 'disabled') return ''
  if (o.startsWith('文字识别正在启动')) return ''
  return o === 'unavailable' ? '这台电脑用不了屏幕识别' : o
})

const want = computed<'offer' | 'info' | 'pinned' | 'none'>(() => {
  if (preview.value || offer.value) return 'offer'
  if (!g.value?.inGame) return mode.value === 'pinned' ? 'pinned' : 'none'
  if (Date.now() < infoUntil.value || problem.value) return 'info'
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

// After a pick, show it (and the next item) for a few seconds.
watch(
  () => (g.value?.picks ?? []).map((p) => `${p.stage}:${p.augmentId}`).join(','),
  (now, before) => {
    if (now && now !== before) infoUntil.value = Date.now() + 15000
  },
)
// Next item for the augments taken so far.
watch(
  () => [g.value?.me?.champion.id, (g.value?.picks ?? []).map((p) => p.augmentId).join(','), (g.value?.me?.items ?? []).join(',')].join('|'),
  async () => {
    const gm = g.value
    if (!gm?.me || !gm.picks?.length || preview.value) {
      plan.value = null
      return
    }
    try {
      plan.value = await api.augmentBuild(gm.me.champion.id, gm.picks.map((p) => p.augmentId), gm.me.items ?? [])
    } catch {
      plan.value = null
    }
  },
)

let timer: number | undefined
let tick: number | undefined
async function poll() {
  try {
    game.value = await api.game()
  } catch {
    game.value = null
  }
  timer = window.setTimeout(poll, game.value?.inGame ? 1000 : 3000)
}
let previewTimer: number | undefined
onMounted(() => {
  document.documentElement.classList.add('overlay')
  void poll()
  // Re-evaluate time-based visibility (the info window ends) and size.
  tick = window.setInterval(report, 1000)
  window.ezlol?.overlay?.onMode((m) => (mode.value = m))
  window.ezlol?.overlay?.onHotkeys((h) => (hotkeys.value = h))
  window.ezlol?.overlay?.onFlash((text) => {
    flash.value = text
    infoUntil.value = Date.now() + 6000
    setTimeout(() => (flash.value = ''), 6000)
  })
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
    <template v-if="offer">
      <div class="top">
        <span class="take">选「{{ offer.best }}」</span>
        <span class="muted">{{ STAGE[offer.stage] }}强化<template v-if="preview"> · 预览</template></span>
      </div>
      <div v-if="offer.note" class="note">{{ offer.note }}</div>
      <div v-for="(c, i) in cards" :key="c.id" class="row" :class="[c.rarity, { best: c.id === bestId, chosen: offer.chosen === i + 1 }]">
        <span class="slot">{{ i + 1 }}</span>
        <div class="body">
          <div class="name">
            {{ c.name }}<span v-if="c.id === bestId" class="rec">推荐</span><span v-if="offer.chosen === i + 1" class="mark">已标记</span>
            <span class="wr">{{ c.stageGames ? `${pct(c.stageWinRate)} ${signed(c.lift)}` : '数据少' }}</span>
          </div>
          <div v-for="(r, j) in c.reasons.slice(1, 3)" :key="j" class="why" :class="{ warn: r.startsWith('注意') || r.startsWith('和你现在的装备不太搭') }">{{ r }}</div>
        </div>
      </div>
      <div class="foot">选完按 {{ pretty(hotkeys.pick1) }} / 2 / 3 标记实际选的第几个（不按默认记为推荐）· {{ pretty(hotkeys.toggle) }} 隐藏</div>
    </template>
    <template v-else>
      <div v-if="flash" class="flash">{{ flash }}</div>
      <div v-if="problem" class="problem">{{ problem }}</div>
      <div v-if="lastPick" class="picked">
        已记：{{ STAGE[lastPick.stage] }}「{{ lastPick.name }}」<span v-if="lastPick.assumed" class="muted">（按推荐；不对就按 {{ pretty(hotkeys.pick1) }}/2/3）</span>
      </div>
      <div v-if="plan?.plan.direction?.conflict" class="problem">注意：{{ plan.plan.direction.note }}</div>
      <div v-if="plan?.plan.next?.length" class="next">
        <span class="muted">下一件：</span>
        <span v-for="x in plan.plan.next.slice(0, 3)" :key="x.item.id" class="it">
          <img v-if="x.item.image" :src="x.item.image" :alt="x.item.name" />{{ x.item.name }} <i>{{ pct(x.winRate) }}</i>
        </span>
      </div>
      <div v-if="!lastPick && !problem && !flash" class="muted">ezlol 在后台；出现强化选择时会自动显示。</div>
    </template>
  </div>
</template>

<style scoped>
.ov { width: 400px; box-sizing: border-box; padding: 10px 12px; color: #f0e6d2; font-size: 13px; line-height: 1.4;
  background: rgba(1, 10, 19, 0.86); border: 1px solid rgba(200, 170, 110, 0.7); border-radius: 6px; font-family: 'Microsoft YaHei UI', 'Microsoft YaHei', 'PingFang SC', sans-serif; }
.ov.empty { visibility: hidden; }
.muted { color: #a09b8c; font-size: 12px; }
.top { display: flex; align-items: baseline; gap: 10px; margin-bottom: 2px; }
.take { font-size: 18px; font-weight: 700; color: #0ac8b9; }
.note { font-size: 12px; color: #f0d48a; margin-bottom: 6px; }
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
.problem { color: #e84057; margin-bottom: 4px; }
.picked { margin-bottom: 4px; }
.next { display: flex; flex-wrap: wrap; gap: 4px 10px; align-items: center; }
.it { display: inline-flex; align-items: center; gap: 4px; }
.it img { width: 22px; height: 22px; border: 1px solid #785a28; }
.it i { font-style: normal; color: #cdbe91; font-size: 11px; }
</style>

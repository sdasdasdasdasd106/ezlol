<script setup lang="ts">
// The side panel (Electron, ?side=1): a small always-on-top window at the side
// of the screen during an ARAM Mayhem game. It cannot be clicked (clicks go
// through to the game); hotkeys do everything. It shows the recommendation
// after a scan (cards left to right = hotkeys 1/2/3), then the augments taken
// and the next item for them.
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { api, DEFAULT_HOTKEYS, prettyHotkey, subscribe } from '../api'
import type { HotkeyEvent } from '../api'
import type { AugBuildResponse, Game, OfferCard } from '../types'
import { place } from '../voice'

const game = ref<Game | null>(null)
const plan = ref<AugBuildResponse | null>(null)
const keys = ref({ ...DEFAULT_HOTKEYS })
const flash = ref<{ text: string; ok: boolean } | null>(null)
const preview = ref(false)
const root = ref<HTMLElement | null>(null)

const STAGE = ['', '开局', '7 级', '11 级', '15 级']
const PIT: Record<string, string> = { mark: '坑', data: '可能是坑', ai: 'AI：别拿' }
const pct = (x: number) => `${(x * 100).toFixed(1)}%`
const pretty = prettyHotkey
const pickKeys = computed(() => {
  const [a, b, c] = [keys.value.pick1, keys.value.pick2, keys.value.pick3].map(pretty)
  return a.endsWith('1') && b === a.slice(0, -1) + '2' && c === a.slice(0, -1) + '3' ? `${a}/2/3` : `${a} / ${b} / ${c}`
})

// Preview data so the position can be checked from Settings.
const DEMO: Game = {
  inGame: true, gameTime: 420, mapId: 12, mode: 'aram', patch: '', ocr: 'available', ocrLang: 'zh-Hans-CN',
  me: { name: '', champion: { id: 22, key: 'Ashe', name: '寒冰射手', title: '', tags: [], image: '', attack: 0, magic: 0 }, team: '', isDead: false, level: 7, items: [] },
  picks: [],
  offer: {
    active: true, pending: false, level: 7, stage: 2, best: '大法师', note: '「大法师」明显更好，比第二名高 4.0%。', screen: [3, 1, 2],
    offered: [
      { id: 1, name: '大法师', rarity: 'prismatic', stageWinRate: 0.528, stageGames: 23295, lift: 0.099, reasons: [], fitOk: true } as unknown as OfferCard,
      { id: 2, name: '屹立不倒', rarity: 'gold', stageWinRate: 0.471, stageGames: 15094, lift: 0.042, reasons: [], fitOk: true, pitfall: 'data' } as unknown as OfferCard,
      { id: 3, name: '灵魂虹吸', rarity: 'gold', stageWinRate: 0.47, stageGames: 11766, lift: 0.041, reasons: [], fitOk: true } as unknown as OfferCard,
    ],
  },
}
const g = computed(() => (preview.value ? DEMO : game.value))
const aram = computed(() => !!g.value?.inGame && g.value.mode === 'aram')
const offer = computed(() => (g.value?.offer?.active && g.value.offer.offered?.length ? g.value.offer : null))
const cards = computed<OfferCard[]>(() => {
  const o = offer.value
  if (!o?.offered) return []
  const by = new Map(o.offered.map((c) => [c.id, c]))
  return (o.screen?.length ? o.screen : o.offered.map((c) => c.id)).map((id) => by.get(id)).filter(Boolean) as OfferCard[]
})
const best = computed(() => offer.value?.offered?.[0])
const bestPlace = computed(() => place(cards.value.findIndex((c) => c.id === best.value?.id), cards.value.length))
const picks = computed(() => g.value?.picks ?? [])
const due = computed(() => (g.value?.offer?.pending && !offer.value ? g.value.offer : null))
const next = computed(() => (plan.value?.plan.next ?? []).slice(0, 2))

// The next item for the augments taken.
watch(
  () => (aram.value && !preview.value && picks.value.length ? [g.value?.me?.champion.id, picks.value.map((p) => p.augmentId).join(','), [...(g.value?.me?.items ?? [])].sort().join(',')].join('|') : ''),
  async (k) => {
    const gm = g.value
    if (!k || !gm?.me) {
      plan.value = null
      return
    }
    try {
      plan.value = await api.augmentBuild(gm.me.champion.id, picks.value.map((p) => p.augmentId), gm.me.items ?? [])
    } catch {
      plan.value = null
    }
  },
)

// Tell the shell whether to show it, and how tall it is.
function report() {
  const h = Math.ceil((root.value?.getBoundingClientRect().height ?? 0) + 2)
  window.ezlol?.side?.want(aram.value || !!flash.value, h)
}

let timer: number | undefined
let tick: number | undefined
let flashTimer: number | undefined
let unsub: (() => void) | undefined
let polling = false
async function poll() {
  if (polling) return
  polling = true
  clearTimeout(timer)
  try {
    game.value = await api.game()
  } catch {
    game.value = null
  } finally {
    polling = false
  }
  timer = window.setTimeout(poll, game.value?.inGame ? 1000 : 3000)
}
function onHotkey(h: HotkeyEvent) {
  flash.value = { text: h.text, ok: h.ok }
  clearTimeout(flashTimer)
  flashTimer = window.setTimeout(() => (flash.value = null), 8000)
  void poll()
}
onMounted(() => {
  document.documentElement.classList.add('side')
  void poll()
  unsub = subscribe(() => {}, onHotkey)
  window.ezlol?.getAppSettings?.().then((a) => (keys.value = a.hotkeys)).catch(() => {})
  window.ezlol?.onAppSettings?.((a) => (keys.value = a.hotkeys))
  window.ezlol?.side?.onPreview(() => {
    preview.value = true
    setTimeout(() => (preview.value = false), 8000)
  })
  tick = window.setInterval(report, 1000)
  new ResizeObserver(report).observe(root.value!)
})
watch([aram, flash], async () => {
  await nextTick()
  report()
})
onUnmounted(() => {
  clearTimeout(timer)
  clearInterval(tick)
  unsub?.()
})
</script>

<template>
  <div ref="root" class="side">
    <div v-if="flash" class="flash" :class="{ err: !flash.ok }">{{ flash.text }}</div>

    <template v-if="offer">
      <div class="kick">{{ STAGE[offer.stage] }}强化<template v-if="preview"> · 预览</template></div>
      <div class="pick">选{{ bestPlace }}的 <b>{{ offer.best }}</b></div>
      <div v-if="offer.ai?.status === 'done' && !offer.ai.agree" class="ai">AI 建议改选「{{ offer.ai.pick }}」</div>
      <div v-for="(c, i) in cards" :key="c.id" class="row" :class="[c.rarity, { best: c.id === best?.id, pit: c.pitfall === 'mark' }]">
        <span class="slot">{{ i + 1 }}</span>
        <span class="name">{{ c.name }}</span>
        <span v-if="c.pitfall" class="tag pitt" :class="c.pitfall">{{ PIT[c.pitfall] }}</span>
        <span v-else-if="c.id === best?.id" class="tag">推荐</span>
        <span class="wr">{{ c.stageGames ? pct(c.stageWinRate) : '—' }}</span>
      </div>
      <div v-if="best?.core" class="build">
        <span class="lab">选它后出装</span>
        <img v-for="it in best.core.items" :key="it.id" :src="it.image" :title="it.name" :alt="it.name" />
        <img v-if="best.core.boots?.image" class="boots" :src="best.core.boots.image" :alt="best.core.boots.name" />
      </div>
      <div class="foot">选完按 {{ pickKeys }} 记下 · {{ pretty(keys.scan) }} 重新识别</div>
    </template>

    <template v-else-if="aram">
      <div v-if="due" class="due">{{ STAGE[due.stage] }}强化：打开后按 <b>{{ pretty(keys.scan) }}</b></div>
      <div v-if="picks.length" class="picks">
        <span v-for="p in picks" :key="p.stage" class="chip" :class="[p.rarity, { assumed: p.assumed }]">{{ p.name }}</span>
      </div>
      <div v-if="next.length" class="next">
        <span class="lab">下一件</span>
        <span v-for="x in next" :key="x.item.id" class="it"><img :src="x.item.image" :alt="x.item.name" />{{ x.item.name }}</span>
      </div>
      <div v-if="!due && !picks.length" class="muted">选强化时按 <b>{{ pretty(keys.scan) }}</b> 识别</div>
      <div v-if="g?.needAdmin" class="warn">英雄联盟以管理员身份运行：ezlol 也要以管理员身份运行，快捷键才有用</div>
    </template>
  </div>
</template>

<style scoped>
.side { box-sizing: border-box; width: 320px; padding: 10px 12px; font-size: 13px; line-height: 1.45; color: var(--text); background: var(--panel); border: 1px solid var(--border); border-radius: 10px; }
.flash { font-size: 12px; color: var(--accent); margin-bottom: 6px; }
.flash.err { color: var(--amber); }
.kick { font-size: 11px; color: var(--muted); }
.pick { font-size: 17px; font-weight: 600; margin-bottom: 6px; }
.pick b { color: var(--accent); }
.ai { font-size: 12px; color: var(--amber); margin-bottom: 4px; }
.row { display: flex; align-items: center; gap: 8px; padding: 5px 8px; margin-top: 4px; border-radius: 6px; background: var(--surface-raised); border: 1px solid var(--border); }
.row.best { border-color: var(--accent); background: var(--accent-soft); }
.row.pit { opacity: .6; }
.slot { flex: none; width: 18px; height: 18px; display: grid; place-items: center; border-radius: 50%; font-size: 11px; color: var(--muted); border: 1px solid var(--border); }
.name { flex: 1; min-width: 0; font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.row.prismatic .name { color: var(--rarity-prismatic); } .row.gold .name { color: var(--rarity-gold); }
.tag { flex: none; font-size: 10px; font-weight: 600; padding: 0 6px; border-radius: 999px; color: var(--btn-primary-text); background: var(--accent); }
.tag.pitt { color: var(--red); background: transparent; box-shadow: inset 0 0 0 1px var(--red); }
.tag.pitt.mark { color: #fff; background: var(--red); box-shadow: none; }
.wr { flex: none; font-size: 12px; color: var(--muted); font-variant-numeric: tabular-nums; }
.build { display: flex; align-items: center; gap: 3px; margin-top: 8px; }
.build img { width: 24px; height: 24px; border-radius: 4px; }
.build img.boots { margin-left: 6px; }
.lab { font-size: 11px; color: var(--muted); margin-right: 4px; }
.foot { margin-top: 8px; font-size: 11px; color: var(--dim); }
.due { color: var(--accent); }
.due b, .muted b { color: var(--text); }
.muted { color: var(--muted); font-size: 12px; }
.picks { display: flex; flex-wrap: wrap; gap: 4px; margin: 4px 0; }
.chip { font-size: 12px; padding: 1px 8px; border-radius: 999px; border: 1px solid var(--border); }
.chip.prismatic { border-color: var(--rarity-prismatic); } .chip.gold { border-color: var(--rarity-gold); }
.chip.assumed { border-style: dashed; }
.next { display: flex; flex-wrap: wrap; align-items: center; gap: 4px 10px; margin-top: 4px; font-size: 12px; }
.it { display: inline-flex; align-items: center; gap: 4px; }
.it img { width: 22px; height: 22px; border-radius: 4px; }
.warn { margin-top: 6px; font-size: 11px; color: var(--amber); }
</style>

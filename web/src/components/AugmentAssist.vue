<script setup lang="ts">
// In-game augment recommendation. The game runs in exclusive fullscreen, so
// the player presses the scan hotkey, switches out (Alt+Tab) and reads it here:
// the cards in screen order (left to right = hotkeys 1/2/3), the recommended
// one marked, and for each card the build that goes with it. Below, the
// augments taken so far (an assumed one can be corrected with a click).
import { computed, ref, watch } from 'vue'
import Widget from './Widget.vue'
import * as sound from '../sound'
import { api, DEFAULT_HOTKEYS, prettyHotkey } from '../api'
import { toast } from '../toast'
import { place } from '../voice'
import type { AugPick, Game, OfferCard } from '../types'

const props = defineProps<{ game: Game | null }>()
const emit = defineEmits<{ picks: [p: AugPick[]]; refresh: [] }>()

const offer = computed(() => (props.game?.offer?.active && props.game.offer.offered?.length ? props.game.offer : null))
const mode = computed(() => props.game?.mode ?? '')
const picks = computed(() => props.game?.picks ?? [])
const STAGE = ['', '开局', '7 级', '11 级', '15 级']
const pct = (x: number) => `${(x * 100).toFixed(1)}%`
const signed = (x: number) => `${x >= 0 ? '+' : ''}${(x * 100).toFixed(1)}%`

// Cards in screen order, so the 1/2/3 hotkeys and "left / middle / right" match.
const cards = computed<OfferCard[]>(() => {
  const o = offer.value
  if (!o?.offered) return []
  const by = new Map(o.offered.map((c) => [c.id, c]))
  const order = o.screen?.length ? o.screen : o.offered.map((c) => c.id)
  return order.map((id) => by.get(id)).filter(Boolean) as OfferCard[]
})
const bestId = computed(() => offer.value?.offered?.[0]?.id)
const bestPlace = computed(() => place(cards.value.findIndex((c) => c.id === bestId.value), cards.value.length))
const RARITY: Record<string, string> = { prismatic: '棱彩', gold: '金色', silver: '银色' }
const warn = (r: string) => r.startsWith('注意') || r.startsWith('和你现在的装备不太搭')
// The win-rate line is shown as numbers; the build line is shown as icons.
const reasonsOf = (c: OfferCard) => c.reasons.slice(1).filter((r) => !r.startsWith('推荐核心')).slice(0, 2)
const aiPick = (name: string) => offer.value?.ai?.status === 'done' && !offer.value.ai.agree && offer.value.ai.pick === name

// Hotkeys, for the hints.
const keys = ref({ ...DEFAULT_HOTKEYS })
window.ezlol?.getAppSettings?.().then((a) => (keys.value = a.hotkeys)).catch(() => {})
window.ezlol?.onAppSettings?.((a) => (keys.value = a.hotkeys))
const scanKey = computed(() => prettyHotkey(keys.value.scan))
const pickKeys = computed(() => {
  const [a, b, c] = [keys.value.pick1, keys.value.pick2, keys.value.pick3].map(prettyHotkey)
  return a.endsWith('1') && b === a.slice(0, -1) + '2' && c === a.slice(0, -1) + '3' ? `${a}/2/3` : `${a} / ${b} / ${c}`
})

// The same as the scan hotkey, for a windowed game or a second screen.
const scanning = ref(false)
const scanMsg = ref('')
async function scanNow() {
  scanning.value = true
  scanMsg.value = ''
  try {
    const r = await api.scanNow()
    scanMsg.value = r.status === 'ai' ? (r.message ?? 'AI 正在推断…') : ''
  } catch (e) {
    scanMsg.value = (e as Error).message
  } finally {
    scanning.value = false
    emit('refresh')
  }
}
const canScan = computed(() => !!props.game?.inGame && mode.value === 'aram' && !props.game?.offer?.waiting && !['unavailable', 'disabled'].includes(props.game?.ocr ?? ''))

const scanNote = computed(() => {
  const g = props.game
  if (!g?.inGame) return ''
  if (g.ocr === 'disabled') return '屏幕识别已在设置里关闭。'
  if (g.ocr === 'unavailable') return '这台电脑上用不了屏幕识别。'
  const l = g.ocrLang ?? ''
  return l && !l.startsWith('zh') ? `识别语言是 ${l}，认不出中文强化名：请在 Windows 设置里安装「中文(简体)」的光学字符识别，然后重启 ezlol。` : ''
})

// Correcting an assumed pick.
const fixing = ref<number | null>(null)
async function correct(p: AugPick, id: number) {
  fixing.value = null
  if (id === p.augmentId) return
  if (id === 0 && !confirm(`删除「${STAGE[p.stage]} ${p.name}」这条记录？`)) return
  try {
    emit('picks', await api.correctPick(p.stage, id))
  } catch (e) {
    toast({ key: 'pick', kind: 'error', title: '更正失败', body: (e as Error).message })
  }
}

// A chime when a recommendation comes in.
watch(
  () => (offer.value ? `${offer.value.scannedAt ?? ''}|${offer.value.screen?.join(',') ?? ''}` : ''),
  (k) => {
    if (k) sound.alert()
  },
)
</script>

<template>
  <Widget id="augment-assist" :w="12">
    <h2 class="hd">
      强化推荐<span v-if="game?.me" class="sub">{{ game.me.champion.name }} · {{ game.me.level }} 级</span>
      <button v-if="canScan" class="scan" :disabled="scanning" :title="`截屏识别现在屏幕上的强化（游戏里按 ${scanKey}）`" @click="scanNow">
        {{ scanning ? '识别中…' : '立即识别' }}<kbd>{{ scanKey }}</kbd>
      </button>
    </h2>
    <div v-if="scanMsg" class="msg">{{ scanMsg }}</div>

    <div v-if="!game?.inGame" class="empty">进入海克斯乱斗后，选强化时按 <kbd>{{ scanKey }}</kbd>，切出来在这里看推荐。</div>
    <div v-else-if="mode !== 'aram'" class="empty">这不是海克斯乱斗对局，没有强化可选。</div>
    <template v-else>
      <template v-if="offer">
        <div class="hero">
          <div class="kicker">{{ STAGE[offer.stage] }}强化 · 推荐</div>
          <div class="pick">选{{ bestPlace }}的<b>{{ offer.best }}</b></div>
          <div v-if="offer.note" class="sub2">{{ offer.note }}</div>
          <div v-if="offer.ai && offer.ai.status !== 'error'" class="ai" :class="{ agree: offer.ai.agree, pending: offer.ai.status === 'pending' }">
            <template v-if="offer.ai.status === 'pending'">AI 正在复核…</template>
            <template v-else><b>{{ offer.ai.agree ? 'AI 同意' : `AI 建议改选「${offer.ai.pick}」` }}</b>{{ offer.ai.reason }}</template>
          </div>
          <div v-if="offer.via === 'ai'" class="sub2 warnc">这组强化是 AI 根据识别出的文字推断的，可能不准。</div>
        </div>

        <div class="cards">
          <article v-for="(a, i) in cards" :key="a.id" class="card" :class="[a.rarity, { best: a.id === bestId, chosen: offer.chosen === i + 1 }]" :title="a.desc">
            <header>
              <span class="slot">{{ i + 1 }}</span>
              <img v-if="a.icon" class="ico" :src="a.icon" :alt="a.name" /><span v-else class="ico" />
              <div class="title">
                <div class="name">{{ a.name }}</div>
                <div class="rar">{{ RARITY[a.rarity] ?? '' }}</div>
              </div>
              <span v-if="a.id === bestId" class="tag">推荐</span>
              <span v-else-if="aiPick(a.name)" class="tag ai">AI 推荐</span>
            </header>
            <div class="wr">
              <template v-if="a.stageGames">
                <b>{{ pct(a.stageWinRate) }}</b><span :class="a.lift >= 0 ? 'up' : 'down'">{{ signed(a.lift) }}</span>
              </template>
              <span v-else class="na">数据少</span>
            </div>
            <ul v-if="reasonsOf(a).length" class="reasons">
              <li v-for="(r, j) in reasonsOf(a)" :key="j" :class="{ warn: warn(r) }">{{ r }}</li>
            </ul>
            <footer v-if="a.core" class="build">
              <span class="lab">选它后出装</span>
              <div class="items">
                <template v-for="(it, j) in a.core.items" :key="it.id">
                  <span v-if="j" class="arrow">›</span><img v-if="it.image" :src="it.image" :title="it.name" :alt="it.name" /><span v-else class="noimg">{{ it.name || it.id }}</span>
                </template>
                <img v-if="a.core.boots?.image" :src="a.core.boots.image" :title="a.core.boots.name" class="boots" />
              </div>
            </footer>
          </article>
        </div>
        <p class="hint">选完按 <kbd>{{ pickKeys }}</kbd>（左 / 中 / 右）记下选的是哪个，下面的出装按它推荐；不按就记为推荐的那个。</p>
      </template>
      <div v-else-if="game?.offer?.waiting" class="empty">还在加载画面。</div>
      <div v-else-if="game?.offer?.pending" class="empty due">{{ STAGE[game.offer.stage] }}强化待选：选择界面打开后按 <kbd>{{ scanKey }}</kbd>，再切出来看。</div>
      <div v-else class="empty">开局和 7 / 11 / 15 级选强化时，按 <kbd>{{ scanKey }}</kbd> 识别。</div>

      <div v-if="picks.length" class="picks">
        <span class="lab">已选</span>
        <span v-for="p in picks" :key="p.stage" class="chip" :class="[p.rarity, { assumed: p.assumed }]">
          <img v-if="p.icon" :src="p.icon" alt="" />
          <span class="st">{{ STAGE[p.stage] }}</span>{{ p.name }}
          <button class="fixbtn" :title="p.assumed ? '这是按推荐记的；选的不是它就点这里改' : '改成其他选项'" @click="fixing = fixing === p.stage ? null : p.stage">{{ p.assumed ? '不对？' : '改' }}</button>
          <span v-if="fixing === p.stage" class="fix">
            <button v-for="o in p.offered" :key="o.id" class="fixbtn" :class="{ active: o.id === p.augmentId }" @click="correct(p, o.id)">{{ o.name }}</button>
            <button class="fixbtn danger" title="这条记录是错的（其实没选过）：删掉它" @click="correct(p, 0)">删除</button>
          </span>
        </span>
      </div>
      <div v-if="scanNote" class="msg">{{ scanNote }}</div>
    </template>
  </Widget>
</template>

<style scoped>
.hd { display: flex; align-items: center; gap: 8px; }
.sub { font-weight: 400; font-size: 12px; color: var(--muted); text-transform: none; letter-spacing: 0; }
.scan { margin-left: auto; display: inline-flex; align-items: center; gap: 8px; padding: 5px 6px 5px 12px; font-size: 12px; text-transform: none; letter-spacing: 0; }
kbd { font-family: inherit; font-size: 11px; padding: 1px 6px; border-radius: 5px; border: 1px solid var(--border, var(--gold-deep)); background: var(--surface-input); color: var(--muted); white-space: nowrap; }
.empty { color: var(--muted); font-size: 13px; padding: 6px 0; }
.empty.due { color: var(--text); padding: 10px 12px; border-radius: var(--radius-sm); background: var(--accent-soft); }
.msg { font-size: 12px; color: var(--amber); margin: 4px 0 8px; }

.hero { margin: 2px 0 12px; }
.kicker { font-size: 12px; color: var(--muted); }
.pick { font-size: 22px; font-weight: 600; color: var(--text); margin-top: 2px; }
.pick b { color: var(--accent); margin-left: 6px; font-weight: 700; }
.sub2 { font-size: 12px; color: var(--muted); margin-top: 4px; }
.warnc { color: var(--amber); }
.ai { margin-top: 8px; font-size: 12px; line-height: 1.5; padding: 7px 10px; border-radius: var(--radius-sm); background: var(--surface-raised); color: var(--text); border-left: 3px solid var(--amber); }
.ai.agree { border-left-color: var(--accent); }
.ai.pending { color: var(--muted); border-left-color: var(--border, var(--gold-deep)); }
.ai b { margin-right: 6px; }

.cards { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 10px; }
@media (max-width: 900px) { .cards { grid-template-columns: 1fr; } }
.card { display: flex; flex-direction: column; gap: 8px; padding: 12px; min-width: 0; border-radius: var(--radius); border: 1px solid var(--border, var(--gold-deep)); background: var(--surface-raised); }
.card.best { border-color: var(--accent); background: linear-gradient(180deg, var(--accent-soft), transparent 70%), var(--surface-raised); }
.card.chosen { box-shadow: 0 0 0 2px var(--amber); }
.card header { display: flex; align-items: center; gap: 10px; }
.slot { flex: none; width: 20px; height: 20px; display: grid; place-items: center; border-radius: 50%; font-size: 11px; color: var(--muted); background: var(--surface-input); border: 1px solid var(--border, var(--gold-deep)); }
.ico { flex: none; width: 40px; height: 40px; border-radius: 8px; background: #000; border: 2px solid var(--border, var(--gold-deep)); }
.card.prismatic .ico { border-color: var(--rarity-prismatic); }
.card.gold .ico { border-color: var(--rarity-gold); }
.card.silver .ico { border-color: var(--rarity-silver); }
.title { min-width: 0; flex: 1; }
.name { font-size: 15px; font-weight: 600; color: var(--text); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.rar { font-size: 11px; color: var(--muted); }
.card.prismatic .rar { color: var(--rarity-prismatic); } .card.gold .rar { color: var(--rarity-gold); } .card.silver .rar { color: var(--rarity-silver); }
.tag { flex: none; align-self: flex-start; font-size: 11px; font-weight: 600; padding: 2px 8px; border-radius: 999px; color: var(--btn-primary-text); background: var(--accent); }
.tag.ai { background: var(--amber); color: #1a1405; }
.wr { display: flex; align-items: baseline; gap: 8px; }
.wr b { font-size: 20px; font-weight: 600; color: var(--text); font-variant-numeric: tabular-nums; }
.wr .up { font-size: 12px; color: var(--green); } .wr .down { font-size: 12px; color: var(--red); }
.wr .na { font-size: 12px; color: var(--muted); }
.reasons { margin: 0; padding: 0; list-style: none; display: grid; gap: 3px; font-size: 12px; line-height: 1.45; color: var(--muted); }
.reasons li.warn { color: var(--red); }
.build { margin-top: auto; padding-top: 8px; border-top: 1px solid var(--line); display: grid; gap: 6px; }
.build .lab { font-size: 11px; color: var(--muted); }
.items { display: flex; align-items: center; gap: 4px; flex-wrap: wrap; }
.items img { width: 30px; height: 30px; border-radius: 6px; background: #000; }
.items img.boots { margin-left: 8px; }
.items .arrow { color: var(--dim); }
.noimg { font-size: 11px; padding: 0 4px; border: 1px solid var(--border, var(--gold-deep)); border-radius: 4px; }
.hint { margin: 10px 0 0; font-size: 12px; color: var(--muted); }

.picks { margin-top: 12px; display: flex; flex-wrap: wrap; gap: 6px; align-items: center; font-size: 12px; }
.picks .lab { color: var(--muted); margin-right: 2px; }
.chip { display: inline-flex; align-items: center; gap: 6px; padding: 3px 4px 3px 3px; border-radius: 999px; border: 1px solid var(--border, var(--gold-deep)); background: var(--surface-raised); color: var(--text); }
.chip.assumed { border-style: dashed; }
.chip img { width: 22px; height: 22px; border-radius: 50%; }
.chip .st { color: var(--muted); }
.fix { display: inline-flex; gap: 3px; }
.fixbtn { padding: 1px 8px; font-size: 11px; min-height: 0; border-radius: 999px; text-transform: none; letter-spacing: 0; }
</style>

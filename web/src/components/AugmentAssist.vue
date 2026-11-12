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
    <h2>
      强化推荐 <span v-if="game?.me" class="muted sub">{{ game.me.champion.name }} · {{ game.me.level }} 级</span>
      <button v-if="canScan" class="sm scan" :disabled="scanning" :title="`截屏识别现在屏幕上的强化（游戏里按 ${scanKey}）`" @click="scanNow">{{ scanning ? '识别中…' : '立即识别' }}</button>
    </h2>
    <div v-if="scanMsg" class="note">{{ scanMsg }}</div>

    <div v-if="!game?.inGame" class="muted">进入海克斯乱斗后，选强化时按 <b>{{ scanKey }}</b>，切出来在这里看推荐。</div>
    <div v-else-if="mode !== 'aram'" class="muted">这不是海克斯乱斗对局，没有强化可选。</div>
    <template v-else>
      <template v-if="offer">
        <div class="take">选{{ bestPlace }}的「{{ offer.best }}」<span class="why">{{ STAGE[offer.stage] }}强化</span></div>
        <div v-if="offer.note" class="gap">{{ offer.note }}</div>
        <div v-if="offer.ai && offer.ai.status !== 'error'" class="ai" :class="[offer.ai.status, { agree: offer.ai.agree }]">
          <template v-if="offer.ai.status === 'pending'">AI 正在复核…</template>
          <template v-else><b>{{ offer.ai.agree ? 'AI 同意' : `AI 建议改选「${offer.ai.pick}」` }}</b>：{{ offer.ai.reason }}</template>
        </div>
        <div v-if="offer.via === 'ai'" class="gap">这组强化是 AI 根据识别出的文字推断的，可能不准。</div>
        <div class="cards">
          <div v-for="(a, i) in cards" :key="a.id" class="card" :class="[a.rarity, { best: a.id === bestId, chosen: offer.chosen === i + 1 }]" :title="a.desc">
            <div class="head">
              <img v-if="a.icon" :src="a.icon" :alt="a.name" /><span v-else class="noicon" />
              <div class="body">
                <div class="n">
                  <span class="slot">{{ i + 1 }}</span>{{ a.name }}
                  <span v-if="a.id === bestId" class="rec">推荐</span><span v-if="aiPick(a.name)" class="rec aipick">AI</span>
                </div>
                <div class="s">{{ a.stageGames ? `胜率 ${pct(a.stageWinRate)}（比平均 ${signed(a.lift)}）` : '数据少' }}</div>
              </div>
            </div>
            <ul v-if="reasonsOf(a).length" class="reasons">
              <li v-for="(r, j) in reasonsOf(a)" :key="j" :class="{ warn: warn(r) }">{{ r }}</li>
            </ul>
            <div v-if="a.core" class="core">
              <span class="lab">选它后出装</span>
              <template v-for="(it, j) in a.core.items" :key="it.id">
                <span v-if="j" class="arrow">›</span><img v-if="it.image" :src="it.image" :title="it.name" :alt="it.name" /><span v-else class="noimg">{{ it.name || it.id }}</span>
              </template>
              <img v-if="a.core.boots?.image" :src="a.core.boots.image" :title="a.core.boots.name" class="boots" />
            </div>
          </div>
        </div>
        <div class="hint muted">选完按 {{ pickKeys }}（左 / 中 / 右）记下选的是哪个，下面的出装会按它推荐；不按就记为推荐的那个。</div>
      </template>
      <div v-else-if="game?.offer?.waiting" class="muted">还在加载画面。</div>
      <div v-else-if="game?.offer?.pending" class="wait">{{ STAGE[game.offer.stage] }}强化待选：选择界面打开后按 <b>{{ scanKey }}</b>，再切出来看。</div>
      <div v-else class="muted">开局和 7 / 11 / 15 级选强化时，按 <b>{{ scanKey }}</b> 识别。</div>

      <div v-if="picks.length" class="picks">
        <span class="muted">已选：</span>
        <span v-for="p in picks" :key="p.stage" class="chip" :class="[p.rarity, { assumed: p.assumed }]">
          <img v-if="p.icon" :src="p.icon" alt="" />
          <span>{{ STAGE[p.stage] }} {{ p.name }}</span>
          <button class="sm" :title="p.assumed ? '这是按推荐记的；选的不是它就点这里改' : '改成其他选项'" @click="fixing = fixing === p.stage ? null : p.stage">{{ p.assumed ? '不对？' : '改' }}</button>
          <span v-if="fixing === p.stage" class="fix">
            <button v-for="o in p.offered" :key="o.id" class="sm" :class="{ active: o.id === p.augmentId }" @click="correct(p, o.id)">{{ o.name }}</button>
            <button class="sm danger" title="这条记录是错的（其实没选过）：删掉它" @click="correct(p, 0)">删除</button>
          </span>
        </span>
      </div>
      <div v-if="scanNote" class="note">{{ scanNote }}</div>
    </template>
  </Widget>
</template>

<style scoped>
.sub { text-transform: none; letter-spacing: 0; margin-left: 8px; }
h2 .scan { margin-left: auto; float: right; text-transform: none; letter-spacing: 0; }
.take { font-family: var(--display); font-size: 18px; font-weight: 700; color: var(--hextech-2); margin-bottom: 4px; }
.why { font-family: inherit; font-size: 12px; font-weight: 400; color: var(--muted); margin-left: 10px; }
.gap { font-size: 12px; color: var(--gold-bright); margin-bottom: 8px; }
.ai { font-size: 12px; margin-bottom: 8px; padding: 4px 8px; border-left: 3px solid var(--gold-deep); background: var(--surface-raised); line-height: 1.5; }
.ai.pending { color: var(--muted); }
.ai.error { color: var(--muted); border-left-color: var(--red, #e84057); }
.ai.done { border-left-color: #f0d48a; }
.ai.done.agree { border-left-color: var(--hextech-2); }
.ai .conf { color: var(--muted); margin-left: 6px; }
.rec.aipick { margin-left: 4px; color: #f0d48a; border-color: #f0d48a; }
.cards { display: grid; grid-template-columns: repeat(3, 1fr); gap: 8px; }
@media (max-width: 900px) { .cards { grid-template-columns: 1fr; } }
.card { padding: 8px; border: 1px solid var(--gold-deep); background: var(--surface-raised); min-width: 0; display: flex; flex-direction: column; gap: 6px; }
.card.best { border-color: var(--hextech-2); box-shadow: inset 0 0 0 1px var(--accent-glow), 0 0 14px var(--accent-glow); }
.card.prismatic { border-left: 3px solid var(--rarity-prismatic, #3fb4d8); }
.card.gold { border-left: 3px solid var(--rarity-gold, var(--gold)); }
.card.silver { border-left: 3px solid var(--rarity-silver, #8b9bb0); }
.head { display: grid; grid-template-columns: 40px 1fr; gap: 8px; align-items: center; }
.head img, .noicon { width: 40px; height: 40px; border: 1px solid var(--gold-deep); background: #000; }
.body { min-width: 0; }
.n { font-weight: 700; display: flex; align-items: center; gap: 6px; }
.rec { margin-left: auto; font-size: 11px; color: var(--hextech-2); border: 1px solid var(--hextech-dim); padding: 0 5px; }
.s { font-size: 12px; color: var(--muted); }
.reasons { margin: 0; padding-left: 16px; font-size: 12px; line-height: 1.45; }
.reasons li.warn { color: var(--red, #e84057); }
.core { display: flex; align-items: center; gap: 3px; flex-wrap: wrap; }
.core img { width: 26px; height: 26px; border: 1px solid var(--gold-deep); background: #000; }
.core img.boots { margin-left: 6px; }
.core .arrow { color: var(--muted); }
.core .lab { font-size: 11px; color: var(--muted); margin-right: 4px; }
.slot { font-family: var(--display); font-size: 12px; width: 18px; height: 18px; display: inline-grid; place-items: center; border: 1px solid var(--gold-deep); color: var(--gold); }
.card.chosen { outline: 2px solid #f0d48a; }
.hint { margin-top: 8px; font-size: 12px; }
.d { font-size: 11px; color: var(--muted); display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
.tp { font-family: var(--display); font-size: 10px; width: 16px; height: 16px; display: grid; place-items: center; border: 1px solid var(--gold-deep); color: var(--muted); }
.tp.t1 { color: #ff8a3d; border-color: #ff8a3d; } .tp.t2 { color: var(--gold); border-color: var(--gold); } .tp.t3 { color: var(--hextech-2); border-color: var(--hextech-dim); }
.wait { font-size: 13px; border: 1px dashed var(--gold-deep); padding: 6px 10px; color: var(--gold-bright); }
.picks { margin-top: 10px; display: flex; flex-wrap: wrap; gap: 6px; align-items: center; font-size: 12px; }
.chip { display: inline-flex; align-items: center; gap: 5px; padding: 2px 4px 2px 2px; border: 1px solid var(--gold-deep); background: var(--surface-raised); }
.chip.assumed { border-style: dashed; }
.chip img { width: 20px; height: 20px; }
.fix { display: inline-flex; gap: 3px; }
button.sm { padding: 2px 6px; font-size: 11px; min-height: 0; }
.note { margin-top: 8px; font-size: 12px; }
.noimg { font-size: 11px; padding: 0 4px; border: 1px solid var(--gold-deep); }
</style>

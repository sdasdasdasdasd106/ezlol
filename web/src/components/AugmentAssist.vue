<script setup lang="ts">
// In-game augment assistant: the augments on screen (read by OCR), best first,
// each with the numbers behind it; the augments taken so far (the recommended
// one is assumed, one click corrects it). Pops a toast and chimes on an offer.
import { computed, ref, watch } from 'vue'
import Widget from './Widget.vue'
import * as sound from '../sound'
import { api } from '../api'
import { dismissKey, toast } from '../toast'
import type { AugPick, Game, OfferCard } from '../types'

const props = defineProps<{ game: Game | null }>()
const emit = defineEmits<{ picks: [p: AugPick[]] }>()

const offer = computed(() => props.game?.offer)
const mode = computed(() => props.game?.mode ?? '')
const picks = computed(() => props.game?.picks ?? [])
const STAGE = ['', '开局', '7 级', '11 级', '15 级']
const STYLE: Record<string, string> = { crit: '暴击流', on_hit: '攻速特效流', ap: '法强流', hybrid: '混伤流', lethality: '穿甲流', tank: '坦克流', bruiser: '战士流', ad: '物理流' }
const pct = (x: number) => `${(x * 100).toFixed(1)}%`
const signed = (x: number) => `${x >= 0 ? '+' : ''}${(x * 100).toFixed(1)}%`
const tierLabel = (t?: number) => (t ? ['', 'S', 'A', 'B', 'C', 'D'][t] ?? '?' : '')
const statLine = (c: OfferCard) =>
  c.stageGames ? `胜率 ${pct(c.stageWinRate)} · 比平均 ${signed(c.lift)} · ${c.stageGames >= 10000 ? (c.stageGames / 10000).toFixed(1) + ' 万' : c.stageGames} 场` : '暂无数据'

// What the scanner is doing, in words.
const scanNote = computed(() => {
  const g = props.game
  if (!g?.inGame) return ''
  switch (g.ocr) {
    case 'available':
    case '':
      return ''
    case 'disabled':
      return '屏幕识别已在设置里关闭。'
    case 'unavailable':
      return '这台电脑上用不了屏幕识别，只能看下面按胜率排好的强化列表。'
    default:
      return `屏幕识别：${g.ocr}`
  }
})
const langWarn = computed(() => {
  const l = props.game?.ocrLang ?? ''
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

// The AI's pick, marked on its card when it differs from the statistics' pick.
const aiPick = (name: string) => {
  const r = offer.value?.ai
  return r?.status === 'done' && !r.agree && r.pick === name
}

let pinged = false
let announced = 0
watch(offer, (off) => {
  if (off?.active && off.offered?.length) {
    if (!pinged) {
      pinged = true
      sound.alert()
    }
    toast({ key: 'offer', kind: 'pick', sticky: true, title: `选强化 · 推荐「${off.best}」`, body: [off.why, off.note].filter(Boolean).join(' '), cards: off.offered ?? [] })
  } else if (pinged) {
    pinged = false
    dismissKey('offer')
  }
})
// After an offer closes, say what was recorded so a wrong guess gets noticed.
watch(
  () => picks.value.length,
  (n) => {
    const p = picks.value[n - 1]
    if (!p || n <= announced) return
    announced = n
    if (p.assumed) toast({ key: 'pick', kind: 'info', title: `已记为选了「${p.name}」`, body: '如果你选的是别的，在「强化推荐」面板里点它改掉，出装会跟着变。', ttl: 9000 })
  },
)
watch(
  () => props.game?.inGame,
  (v) => {
    if (!v) announced = 0
  },
)
</script>

<template>
  <Widget id="augment-assist" :w="6">
    <h2>强化推荐 <span v-if="game?.me" class="muted sub">{{ game.me.champion.name }} · {{ game.me.level }} 级</span></h2>

    <div v-if="!game?.inGame" class="muted">
      进入海克斯乱斗后，这里会自动识别屏幕上的强化选项并告诉你选哪个。
    </div>
    <div v-else-if="mode !== 'aram'" class="muted">这不是海克斯乱斗对局，没有强化可选。</div>
    <template v-else>
      <div v-if="offer?.active && offer.offered?.length" class="pick">
        <div class="take">选「{{ offer.best }}」<span v-if="offer.stage" class="why">{{ STAGE[offer.stage] }}强化</span></div>
        <div v-if="offer.note" class="gap">{{ offer.note }}</div>
        <div v-if="offer.ai" class="ai" :class="[offer.ai.status, { agree: offer.ai.agree }]">
          <template v-if="offer.ai.status === 'pending'">AI 正在复核这次选择…</template>
          <template v-else-if="offer.ai.status === 'error'">AI 复核失败：{{ offer.ai.error }}</template>
          <template v-else>
            <b>{{ offer.ai.agree ? 'AI 同意' : `AI 建议改选「${offer.ai.pick}」` }}</b><span v-if="offer.ai.confidence" class="conf">把握{{ offer.ai.confidence }}</span>：{{ offer.ai.reason }}
          </template>
        </div>
        <div v-if="offer.via === 'ai'" class="gap">这组强化是 AI 根据识别出的文字推断的，可能不准；不对的话选完按快捷键标记。</div>
        <div class="cards">
          <div v-for="(a, i) in offer.offered" :key="a.id" class="card" :class="[a.rarity, { best: i === 0 }]" :title="a.desc">
            <div class="head">
              <img v-if="a.icon" :src="a.icon" :alt="a.name" /><span v-else class="noicon" />
              <div class="body">
                <div class="n"><span v-if="tierLabel(a.tier)" class="tp" :class="'t' + a.tier">{{ tierLabel(a.tier) }}</span>{{ a.name }}<span v-if="i === 0" class="rec">推荐</span><span v-if="aiPick(a.name)" class="rec aipick">AI</span></div>
                <div class="s">{{ statLine(a) }}</div>
              </div>
            </div>
            <ul class="reasons">
              <li v-for="(r, j) in a.reasons" :key="j" :class="{ warn: r.startsWith('注意') || r.startsWith('和你现在的装备不太搭') }">{{ r }}</li>
            </ul>
            <div v-if="a.core" class="core" :title="`选它之后最常见的核心出装（${STYLE[a.core.style] ?? a.core.style}）`">
              <template v-for="(it, j) in a.core.items" :key="it.id">
                <span v-if="j" class="arrow">›</span><img v-if="it.image" :src="it.image" :title="it.name" :alt="it.name" /><span v-else class="noimg">{{ it.name || it.id }}</span>
              </template>
              <img v-if="a.core.boots?.image" :src="a.core.boots.image" :title="a.core.boots.name" class="boots" />
            </div>
            <div class="d">{{ a.desc }}</div>
          </div>
        </div>
      </div>
      <div v-else-if="offer?.waiting" class="muted">还在加载画面，进入游戏后开始识别。</div>
      <div v-else-if="offer?.pending" class="wait">
        {{ offer.level <= 1 ? '开局' : `${offer.level} 级` }}的强化待选——打开选择界面后会自动识别。
      </div>
      <div v-else class="muted">
        开局和 7 / 11 / 15 级时会出现强化选择，到时会自动识别。
      </div>

      <!-- Augments taken so far; an assumed one can be corrected -->
      <div v-if="picks.length" class="picks">
        <span class="muted">已选：</span>
        <span v-for="p in picks" :key="p.stage" class="chip" :class="[p.rarity, { assumed: p.assumed }]">
          <img v-if="p.icon" :src="p.icon" alt="" />
          <span>{{ STAGE[p.stage] }} {{ p.name }}</span>
          <button class="sm" :title="p.assumed ? '这是按推荐自动记录的；选的不是它就点这里改' : '改成其他选项'" @click="fixing = fixing === p.stage ? null : p.stage">{{ p.assumed ? '不对？' : '改' }}</button>
          <span v-if="fixing === p.stage" class="fix">
            <button v-for="o in p.offered" :key="o.id" class="sm" :class="{ active: o.id === p.augmentId }" @click="correct(p, o.id)">{{ o.name }}</button>
            <button class="sm danger" title="这条记录是错的（其实没选过）：删掉它" @click="correct(p, 0)">删除</button>
          </span>
        </span>
      </div>
      <div v-if="scanNote" class="note">{{ scanNote }}</div>
      <div v-if="langWarn" class="note">{{ langWarn }}</div>
    </template>
  </Widget>
</template>

<style scoped>
.sub { text-transform: none; letter-spacing: 0; margin-left: 8px; }
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

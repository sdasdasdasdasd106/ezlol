<script setup lang="ts">
// In-game augment assistant: shows the three augments on screen (read by OCR),
// best first, with why. Also pops a toast and chimes when an offer appears.
import { computed, watch } from 'vue'
import Widget from './Widget.vue'
import * as sound from '../sound'
import { dismissKey, toast } from '../toast'
import type { Augment, Game } from '../types'

const props = defineProps<{ game: Game | null }>()

const offer = computed(() => props.game?.offer)
const mode = computed(() => props.game?.mode ?? '')
const pctf = (x: number) => `${Math.round(x * 100)}%`
const tierLabel = (t?: number) => (t ? ['', 'S', 'A', 'B', 'C', 'D'][t] ?? '?' : '?')
const statLine = (a: Augment) =>
  a.avgPlace ? `平均名次 ${a.avgPlace.toFixed(2)} · ${a.games} 场` : a.games ? `胜率 ${pctf(a.winRate)} · ${a.games} 场 · 选取 ${pctf(a.pickRate)}` : '暂无数据'

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

let pinged = false
watch(offer, (off) => {
  if (off?.active && off.offered?.length) {
    if (!pinged) {
      pinged = true
      sound.alert()
    }
    toast({ key: 'offer', kind: 'pick', sticky: true, title: `选强化 · 推荐「${off.best}」`, body: off.why, cards: off.offered ?? [] })
  } else if (pinged) {
    pinged = false
    dismissKey('offer')
  }
})
</script>

<template>
  <Widget id="augment-assist" :w="6">
    <h2>强化推荐 <span v-if="game?.me" class="muted sub">{{ game.me.champion.name }} · {{ game.me.level }} 级</span></h2>

    <div v-if="!game?.inGame" class="muted">
      进入大乱斗（海克斯乱斗）或斗魂竞技场后，这里会自动识别屏幕上的强化选项并告诉你选哪个。
    </div>
    <div v-else-if="mode !== 'aram' && mode !== 'arena'" class="muted">召唤师峡谷没有强化，看下面的出装就好。</div>
    <template v-else>
      <div v-if="offer?.active && offer.offered?.length" class="pick">
        <div class="take">选「{{ offer.best }}」<span class="why">{{ offer.why }}</span></div>
        <div class="cards">
          <div v-for="(a, i) in offer.offered" :key="a.id" class="card" :class="[a.rarity, { best: i === 0 }]" :title="a.desc">
            <img v-if="a.icon" :src="a.icon" :alt="a.name" /><span v-else class="noicon" />
            <div class="body">
              <div class="n"><span class="tp" :class="'t' + a.tier">{{ tierLabel(a.tier) }}</span>{{ a.name }}<span v-if="i === 0" class="rec">推荐</span></div>
              <div class="s">{{ statLine(a) }}</div>
              <div class="d">{{ a.desc }}</div>
            </div>
          </div>
        </div>
      </div>
      <div v-else-if="offer?.pending" class="wait">
        {{ offer.level <= 1 ? '开局' : `${offer.level} 级` }}的强化待选——打开选择界面后会自动识别。
      </div>
      <div v-else class="muted">
        {{ mode === 'arena' ? '每轮之间出现强化选择时会自动识别。' : '开局和 7 / 11 / 15 级时会出现强化选择，到时会自动识别。' }}
      </div>
      <div v-if="scanNote" class="note">{{ scanNote }}</div>
      <div v-if="langWarn" class="note">{{ langWarn }}</div>
    </template>
  </Widget>
</template>

<style scoped>
.sub { text-transform: none; letter-spacing: 0; margin-left: 8px; }
.take { font-family: var(--display); font-size: 18px; font-weight: 700; color: var(--hextech-2); margin-bottom: 8px; }
.why { font-family: inherit; font-size: 12px; font-weight: 400; color: var(--muted); margin-left: 10px; }
.cards { display: grid; grid-template-columns: repeat(3, 1fr); gap: 8px; }
@media (max-width: 900px) { .cards { grid-template-columns: 1fr; } }
.card { display: grid; grid-template-columns: 44px 1fr; gap: 8px; padding: 8px; border: 1px solid var(--gold-deep); background: var(--surface-raised); min-width: 0; }
.card.best { border-color: var(--hextech-2); box-shadow: inset 0 0 0 1px var(--accent-glow), 0 0 14px var(--accent-glow); }
.card.prismatic { border-left: 3px solid var(--rarity-prismatic, #3fb4d8); }
.card.gold { border-left: 3px solid var(--rarity-gold, var(--gold)); }
.card.silver { border-left: 3px solid var(--rarity-silver, #8b9bb0); }
.card img { width: 44px; height: 44px; border: 1px solid var(--gold-deep); background: #000; }
.body { min-width: 0; }
.n { font-weight: 700; display: flex; align-items: center; gap: 6px; }
.rec { margin-left: auto; font-size: 11px; color: var(--hextech-2); border: 1px solid var(--hextech-dim); padding: 0 5px; }
.s { font-size: 12px; color: var(--muted); }
.d { font-size: 11px; color: var(--muted); margin-top: 3px; display: -webkit-box; -webkit-line-clamp: 3; -webkit-box-orient: vertical; overflow: hidden; }
.tp { font-family: var(--display); font-size: 10px; width: 16px; height: 16px; display: grid; place-items: center; border: 1px solid var(--gold-deep); color: var(--muted); }
.tp.t1 { color: #ff8a3d; border-color: #ff8a3d; } .tp.t2 { color: var(--gold); border-color: var(--gold); } .tp.t3 { color: var(--hextech-2); border-color: var(--hextech-dim); }
.wait { font-size: 13px; border: 1px dashed var(--gold-deep); padding: 6px 10px; color: var(--gold-bright); }
.note { margin-top: 8px; font-size: 12px; }
.noicon { width: 44px; height: 44px; border: 1px solid var(--gold-deep); background: #000; }
</style>

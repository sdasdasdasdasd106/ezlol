<script setup lang="ts">
// AI strategy card (DeepSeek searches the web and reads the statistics once
// per champion and patch). In game it comes with /api/game and starts by
// itself; in the lobby only a cached card is shown and the user starts the
// research with a click, so browsing champions costs nothing.
import { computed, onUnmounted, ref, watch } from 'vue'
import Widget from './Widget.vue'
import { api } from '../api'
import type { Champion, Game, Research } from '../types'

const props = defineProps<{ game: Game | null; champion: Champion | null }>()

const lobby = ref<Research | { status: 'off' } | null>(null)
const starting = ref(false)
const inGame = computed(() => !!props.game?.inGame && props.game.mode === 'aram')
const champId = computed(() => (inGame.value ? props.game?.me?.champion.id : props.champion?.id) ?? 0)
const r = computed<Research | { status: 'off' } | null>(() => (inGame.value ? (props.game?.research ?? null) : lobby.value))
const sg = computed(() => (r.value && 'strategy' in r.value ? r.value.strategy : undefined))
const when = computed(() => (sg.value?.at ? new Date(sg.value.at).toLocaleDateString('zh-CN', { month: 'numeric', day: 'numeric' }) : ''))
const host = (u: string) => {
  try {
    return new URL(u).hostname.replace(/^www\./, '')
  } catch {
    return u
  }
}

let timer: number | undefined
async function load(start = false) {
  clearTimeout(timer)
  const id = champId.value
  if (!id || inGame.value) return
  try {
    const v = await api.aiResearch(id, start)
    if (id !== champId.value) return
    lobby.value = v
    if (v.status === 'pending') timer = window.setTimeout(() => load(), 3000)
  } catch {
    lobby.value = null
  }
}
async function startResearch() {
  starting.value = true
  try {
    await load(true)
  } finally {
    starting.value = false
  }
}
watch(champId, () => {
  lobby.value = null
  void load()
}, { immediate: true })
onUnmounted(() => clearTimeout(timer))
</script>

<template>
  <Widget v-if="champId && r && r.status !== 'off'" id="ai-strategy" :w="6">
    <h2>AI 攻略 <span class="muted sub">DeepSeek 联网整理 · 仅供参考</span></h2>
    <div v-if="r.status === 'none'" class="row">
      <span class="muted">让 AI 搜索这个英雄的海克斯乱斗玩法，结合胜率数据整理一份攻略（每个英雄每个版本一次，花费不到 1 美分）。</span>
      <button type="button" class="primary sm" :disabled="starting" @click="startResearch">{{ starting ? '开始中…' : '生成攻略' }}</button>
    </div>
    <div v-else-if="r.status === 'pending'" class="muted">AI 正在联网搜索并整理攻略，大约需要半分钟到一分钟…</div>
    <div v-else-if="r.status === 'error'" class="err">
      攻略生成失败：{{ (r as Research).error }}<span class="muted">（两分钟后会自动重试）</span>
    </div>
    <template v-else-if="sg">
      <p class="sum">{{ sg.summary }}</p>
      <div v-if="sg.prefer?.length" class="grp">
        <span class="lbl good">优先拿</span>
        <span v-for="a in sg.prefer" :key="a.name" class="tag good" :title="a.why">{{ a.name }}<i>{{ a.why }}</i></span>
      </div>
      <div v-if="sg.avoid?.length" class="grp">
        <span class="lbl bad">尽量别拿</span>
        <span v-for="a in sg.avoid" :key="a.name" class="tag bad" :title="a.why">{{ a.name }}<i>{{ a.why }}</i></span>
      </div>
      <ul v-if="sg.build?.length" class="build">
        <li v-for="(b, i) in sg.build" :key="i">{{ b }}</li>
      </ul>
      <div class="src muted">
        <template v-if="sg.sources?.length">来源：<a v-for="u in sg.sources.slice(0, 4)" :key="u" :href="u" target="_blank" rel="noreferrer">{{ host(u) }}</a></template>
        <span v-if="when"> · {{ when }}整理</span>
      </div>
    </template>
  </Widget>
</template>

<style scoped>
.sub { text-transform: none; letter-spacing: 0; margin-left: 8px; }
.row { display: flex; gap: 10px; align-items: center; justify-content: space-between; }
.sum { margin: 0 0 8px; line-height: 1.55; }
.grp { display: flex; flex-wrap: wrap; gap: 4px 6px; align-items: baseline; margin-bottom: 6px; font-size: 12px; }
.lbl { font-weight: 700; margin-right: 2px; }
.lbl.good { color: var(--hextech-2); } .lbl.bad { color: var(--red, #e84057); }
.tag { padding: 1px 6px; border: 1px solid var(--gold-deep); background: var(--surface-raised); }
.tag.good { border-color: var(--hextech-dim); } .tag.bad { border-color: rgba(232, 64, 87, 0.5); }
.tag i { font-style: normal; color: var(--muted); margin-left: 6px; }
.build { margin: 4px 0 6px; padding-left: 18px; font-size: 12px; line-height: 1.5; }
.src { font-size: 11px; }
.src a { margin-right: 8px; }
.err { font-size: 12px; color: var(--red, #e84057); }
button.sm { padding: 3px 10px; font-size: 12px; min-height: 0; white-space: nowrap; }
</style>

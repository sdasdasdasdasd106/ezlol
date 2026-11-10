<script setup lang="ts">
// Fixed champion bar above the widget grid: portrait, ARAM Mayhem tier,
// source and tier, your mastery and record, the Follow switch. Clicking the
// portrait / name opens the champion picker as a dropdown card. It drives the
// shared build state that the build widgets read; it is never part of the grid.
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import ChampionPicker from './ChampionPicker.vue'
import { fmtPoints, winrate } from '../api'
import { buildState as s, loadBuild } from '../build'
import type { Champion, Status } from '../types'

const props = defineProps<{ champion: Champion | null; champions: Champion[]; status: Status | null; screen: 'idle' | 'select' | 'game'; compact?: boolean }>()
const emit = defineEmits<{ select: [c: Champion] }>()
const follow = defineModel<boolean>('follow', { default: true })

const build = computed(() => s.build)
const info = computed(() => s.info)
const me = computed(() => s.me)
const DMG: Record<string, string> = { kMagic: '法术', kPhysical: '物理', kMixed: '混合' }
const ATK: Record<string, string> = { kRanged: '远程', kMelee: '近战' }
const PIP_LABEL: Record<string, string> = { damage: '伤害', durability: '坦度', crowdControl: '控制', mobility: '机动', utility: '功能' }
const PIPS = ['damage', 'durability', 'crowdControl', 'mobility', 'utility'] as const

// Champion picker dropdown
const pickerOpen = ref(false)
const root = ref<HTMLElement | null>(null)
function choose(c: Champion) {
  pickerOpen.value = false
  // A manual pick while the client has you on another champion would be
  // undone by the next status tick; the user clearly wants to browse.
  if (props.status?.pickedChampion && props.status.pickedChampion !== c.id) follow.value = false
  emit('select', c)
}
function onDoc(e: MouseEvent) {
  if (pickerOpen.value && root.value && !root.value.contains(e.target as Node)) pickerOpen.value = false
}
function onKey(e: KeyboardEvent) {
  if (pickerOpen.value && e.key === 'Escape') pickerOpen.value = false
}
onMounted(() => {
  document.addEventListener('mousedown', onDoc)
  document.addEventListener('keydown', onKey)
})
onUnmounted(() => {
  document.removeEventListener('mousedown', onDoc)
  document.removeEventListener('keydown', onKey)
})

watch(
  () => props.champion?.id,
  () => void loadBuild(props.champion),
  { immediate: true },
)
watch(
  () => props.status?.connected,
  (c, prev) => {
    if (c && !prev) void loadBuild()
  },
)

const pctf = (x: number) => `${Math.round(x * 100)}%`
const n = (g: number) => (g >= 10000 ? `${(g / 10000).toFixed(1)} 万` : String(g))
</script>

<template>
  <!-- Loading modal: fixed overlay, never affects the layout -->
  <Teleport to="body">
    <div v-if="s.loading && s.loadingChamp" class="load-backdrop" aria-live="polite" aria-busy="true">
      <div class="load-card">
        <img :src="s.loadingChamp.image" :alt="s.loadingChamp.name" />
        <div class="load-name">{{ s.loadingChamp.name }}</div>
        <div class="load-sub">正在加载出装…</div>
        <div class="spinner" />
      </div>
    </div>
  </Teleport>

  <section ref="root" class="panel champbar" :class="{ compact, empty: !build }" aria-label="英雄">
    <template v-if="build">
      <button type="button" class="who-btn" :class="{ open: pickerOpen }" :aria-expanded="pickerOpen" aria-haspopup="dialog" title="换一个英雄" @click="pickerOpen = !pickerOpen">
        <img class="portrait" :src="build.champion.image" alt="" />
        <span class="who">
          <span class="name">{{ build.champion.name }}<i class="chev" :class="{ up: pickerOpen }" /></span>
          <span class="title">
            {{ build.champion.title }} · 版本 {{ build.patch }} · 海克斯乱斗
          </span>
        </span>
      </button>
      <div class="rows">
        <div class="row1">
          <div class="facts">
            <span class="badge" :class="build.source">
              {{ build.source === 'aramkit' ? 'aramkit 海克斯乱斗数据' : build.source === 'opgg' ? 'op.gg 普通大乱斗数据' : '暂无数据' }}
            </span>
            <span v-if="build.tier" class="badge tier" :class="'t' + build.tier">{{ build.tier }} 级 · 第 {{ build.rank }} 名<template v-if="build.pickRate"> · 选取率 {{ pctf(build.pickRate) }}</template></span>
            <span v-if="build.total.games" class="muted">{{ n(build.total.games) }} 场 · 胜率 {{ winrate(build.total) }}</span>
          </div>
        </div>
        <div class="row2">
          <div class="facts">
            <span v-if="me?.mastery" class="badge lcu" :title="`最高评分 ${me.mastery.highestGrade}`">你的熟练度：{{ me.mastery.championLevel }} 级 · {{ fmtPoints(me.mastery.championPoints) }}</span>
            <span v-if="me?.record" class="badge riot">
              最近大乱斗：{{ me.record.wins }}胜 {{ me.record.games - me.record.wins }}负 ·
              KDA {{ ((me.record.kills + me.record.assists) / Math.max(1, me.record.deaths)).toFixed(1) }}
            </span>
          </div>
          <div v-if="info && !compact" class="playstyle">
            <span class="badge" :class="info.tacticalInfo.damageType === 'kMagic' ? 'ap' : info.tacticalInfo.damageType === 'kPhysical' ? 'ad' : ''">
              {{ DMG[info.tacticalInfo.damageType] ?? '?' }} · {{ ATK[info.tacticalInfo.attackType] ?? info.tacticalInfo.attackType }} · {{ (info.roles ?? []).join(', ') }}
            </span>
            <div v-for="k in PIPS" :key="k" class="ps">
              <span class="lbl">{{ PIP_LABEL[k] }}</span>
              <span class="pips"><i v-for="n in 3" :key="n" :class="{ on: n <= info.playstyleInfo[k] }" /></span>
            </div>
          </div>
        </div>
        <div v-if="build.notes?.length" class="notes">
          <span v-for="n in build.notes" :key="n" class="note">{{ n }}</span>
        </div>
      </div>
      <button type="button" role="switch" class="toggle follow" :class="{ on: follow }" :aria-checked="follow" title="选英雄锁定后或进游戏后，自动切到你在玩的英雄" @click="follow = !follow">
        <span class="track" aria-hidden="true" /><span class="muted">自动跟随</span>
      </button>
    </template>
    <template v-else-if="s.loading"><div class="muted">正在加载出装…</div></template>
    <template v-else-if="champion">
      <button type="button" class="who-btn" :aria-expanded="pickerOpen" aria-haspopup="dialog" @click="pickerOpen = !pickerOpen">
        <img class="portrait" :src="champion.image" alt="" />
        <span class="who">
          <span class="name">{{ champion.name }}<i class="chev" :class="{ up: pickerOpen }" /></span>
          <span class="title">{{ s.error || '暂无出装数据' }}</span>
        </span>
      </button>
      <button @click="loadBuild(champion)">重试</button>
    </template>
    <template v-else>
      <button type="button" class="who-btn" :aria-expanded="pickerOpen" aria-haspopup="dialog" @click="pickerOpen = !pickerOpen">
        <span class="who">
          <span class="name">选择英雄<i class="chev" :class="{ up: pickerOpen }" /></span>
          <span class="title">点这里搜索；或者在选英雄时锁定、进游戏后，出装会自动切过去。</span>
        </span>
      </button>
    </template>
    <ChampionPicker :champions="champions" :selected="champion" :open="pickerOpen" @select="choose" @close="pickerOpen = false" />
  </section>
</template>

<style scoped>
.champbar { display: flex; align-items: center; gap: 14px; padding: 6px 130px 6px 8px; min-height: 76px; box-sizing: border-box; z-index: 5; }
.champbar.empty { min-height: 48px; padding-right: 8px; }
/* Pinned top-right so it never decides where the rest of the bar wraps */
.follow { position: absolute; right: 10px; top: 50%; transform: translateY(-50%); padding: 0 8px; min-height: 0; background: none; box-shadow: none; border-color: transparent; }
.follow.on { color: var(--gold-bright); }
/* Portrait + name is the picker trigger: a button wearing the card's own look */
.who-btn { display: flex; align-items: center; gap: 12px; padding: 4px 10px 4px 4px; min-height: 0; background: none; box-shadow: none; border: 1px solid transparent; border-radius: var(--radius-sm); text-align: left; text-transform: none; letter-spacing: 0; font: inherit; color: inherit; cursor: pointer; }
.who-btn:hover, .who-btn.open { border-color: var(--gold-deep); background: var(--gold-soft); box-shadow: none; }
.portrait { width: 56px; height: 56px; flex: none; border: 2px solid var(--gold); border-radius: var(--radius-sm); box-shadow: var(--shadow-inset), 0 0 16px var(--gold-glow); }
.who { display: flex; flex-direction: column; min-width: 0; }
.name { font-family: var(--display); font-size: 22px; font-weight: 700; letter-spacing: .04em; line-height: 1.1; color: var(--gold-bright); white-space: nowrap; display: inline-flex; align-items: center; gap: 10px; }
.chev { width: 0; height: 0; border-left: 5px solid transparent; border-right: 5px solid transparent; border-top: 6px solid var(--muted); transition: transform .15s; }
.chev.up { transform: rotate(180deg); }
.title { color: var(--muted); font-style: italic; font-size: 12px; white-space: nowrap; }
/* Row 1: mode / lane buttons + source. Row 2: your record + playstyle. Each wraps on its own. */
.rows { display: flex; flex-direction: column; gap: 6px; min-width: 0; flex: 1; }
.row1, .row2 { display: flex; align-items: center; gap: 8px 18px; flex-wrap: wrap; }
.roles { justify-content: flex-start; align-items: center; }
.roles .sep { width: 1px; height: 22px; background: var(--gold-deep); margin: 0 4px; }
.facts { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
.playstyle { display: flex; gap: 12px; align-items: center; flex-wrap: wrap; }
.ps { display: inline-flex; align-items: center; gap: 6px; font-size: 11px; color: var(--muted); text-transform: capitalize; }
.pips { display: inline-flex; gap: 2px; }
.pips i { width: 10px; height: 10px; border: 1px solid var(--gold-deep); background: var(--surface-input); }
.pips i.on { background: var(--gold); box-shadow: 0 0 6px var(--gold-glow); }
.notes { display: flex; gap: 8px; flex-wrap: wrap; }
.notes .note { margin: 0; padding: 4px 10px; font-size: 12px; }
/* In game: one line when it fits, the rows flow next to each other */
.compact { min-height: 56px; }
.compact .rows { flex-direction: row; flex-wrap: wrap; gap: 4px 18px; align-items: center; }
.compact .portrait { width: 40px; height: 40px; }
.compact .name { font-size: 17px; }
button.sm { padding: 4px 8px; font-size: 10px; }
.badge.opgg { color: var(--hextech-2); border-color: var(--hextech-dim); }
.badge.tier { color: var(--gold-bright); }
.badge.tier.tS { color: #ff8a3d; border-color: #ff8a3d; }
.badge.tier.tA { color: var(--gold); border-color: var(--gold); }
.badge.ap { color: #7fb3ff; border-color: #2a4a7a; }
.badge.ad { color: #e8a33d; border-color: #7a5a2a; }
.load-backdrop { position: fixed; inset: 0; z-index: 150; background: rgba(0, 0, 0, 0.55); backdrop-filter: blur(3px); display: grid; place-items: center; animation: fadein .15s ease; }
@keyframes fadein { from { opacity: 0; } to { opacity: 1; } }
.load-card { display: grid; justify-items: center; gap: 8px; padding: 22px 34px; background: var(--panel-2); border: 1px solid var(--gold-dark); border-radius: var(--radius); box-shadow: var(--shadow-panel); }
.load-card img { width: 96px; height: 96px; border: 2px solid var(--gold); border-radius: var(--radius-sm); box-shadow: 0 0 20px var(--gold-glow); }
.load-name { font-family: var(--display); font-size: 20px; font-weight: 700; color: var(--gold-bright); letter-spacing: .04em; }
.load-sub { color: var(--muted); font-size: 13px; }
.spinner { width: 28px; height: 28px; border: 3px solid var(--gold-deep); border-top-color: var(--accent); border-radius: 50%; animation: spin .8s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
</style>

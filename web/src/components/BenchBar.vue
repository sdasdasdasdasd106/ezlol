<script setup lang="ts">
// Champion select bench (ARAM / ARAM Mayhem): your champion and the bench,
// best ARAM Mayhem win rate first, with one line saying whether to swap, keep
// or reroll. Advice only: the player swaps or rerolls in the client. Clicking a
// champion previews its build below; the app goes back to following your real
// champion when it changes. Refetched only when the status event says the
// bench, your champion or the rerolls changed (no timer of its own).
import { computed, ref, watch } from 'vue'
import { api, fmtPoints } from '../api'
import type { BenchAdvice, BenchRow, Status } from '../types'

const props = defineProps<{ status: Status | null; selectedId?: number }>()
const emit = defineEmits<{ preview: [id: number] }>()

const advice = ref<BenchAdvice | null>(null)
const failed = ref(false)
let seq = 0

const key = computed(() => {
  const s = props.status
  const cs = s?.champSelect
  if (!s?.connected || s.phase !== 'ChampSelect' || !cs?.benchEnabled) return ''
  return [s.pickedChampion, (cs.bench ?? []).join(','), cs.rerolls].join('|')
})

async function load() {
  const my = ++seq
  if (!key.value) {
    advice.value = null
    return
  }
  try {
    const a = await api.bench()
    if (my !== seq) return
    advice.value = a
    failed.value = false
  } catch {
    // Keep the last list; a failed refresh is not worth a toast here.
    if (my === seq) failed.value = true
  }
}
watch(key, load, { immediate: true })

const rows = computed(() => advice.value?.rows ?? [])
const anyData = computed(() => rows.value.some((r) => r.hasData))
const benchEmpty = computed(() => !!advice.value && !rows.value.some((r) => !r.current))
const pct = (x: number) => `${(x * 100).toFixed(1)}%`
const n = (g: number) => (g >= 10000 ? `${(g / 10000).toFixed(1)} 万场` : `${g} 场`)
const record = (r: BenchRow) => (r.record?.games ? `${r.record.wins}胜${r.record.games - r.record.wins}负` : '')
function title(r: BenchRow): string {
  const parts = [r.champion.name]
  if (r.hasData) parts.push(`胜率 ${pct(r.winRate)}（${n(r.games)}）`)
  if (r.mastery) parts.push(`熟练度 ${r.mastery.championLevel} 级 · ${fmtPoints(r.mastery.championPoints)}`)
  if (r.record?.games) parts.push(`最近大乱斗 ${record(r)}`)
  parts.push(r.current ? '点一下看出装' : '点一下预览出装（不会替你换英雄）')
  return parts.join('\n')
}
</script>

<template>
  <section v-if="key" class="panel benchbar" aria-label="板凳席">
    <div class="head">
      <span class="label">板凳席</span>
      <span v-if="advice?.verdict" class="verdict" :class="advice.kind">{{ advice.verdict }}</span>
      <span v-else-if="!advice" class="muted">正在读取板凳席…</span>
      <span v-else-if="rows.length && !anyData" class="muted">胜率数据暂时拿不到</span>
      <span v-if="benchEmpty && !advice?.verdict.startsWith('板凳席')" class="muted">板凳席还是空的</span>
      <span v-if="advice?.note" class="hint">{{ advice.note }}</span>
      <span class="grow" />
      <span v-if="advice?.source === 'opgg'" class="badge opgg" title="海克斯乱斗数据暂时拿不到">op.gg 普通大乱斗数据</span>
      <span v-else-if="advice?.source === 'aramkit'" class="badge">aramkit 海克斯乱斗<template v-if="advice.patch"> · {{ advice.patch }}</template></span>
      <span v-if="advice && advice.rerolls >= 0" class="muted">重骰 {{ advice.rerolls }} 次</span>
    </div>
    <div v-if="rows.length" class="list">
      <button
        v-for="r in rows"
        :key="r.champion.id"
        type="button"
        class="champ"
        :class="{ current: r.current, target: advice?.target === r.champion.id, viewing: selectedId === r.champion.id }"
        :title="title(r)"
        @click="emit('preview', r.champion.id)"
      >
        <img :src="r.champion.image" alt="" />
        <span class="info">
          <span class="line1">
            <span class="name">{{ r.champion.name }}</span>
            <span v-if="r.current" class="tag cur">当前</span>
            <span v-else-if="advice?.target === r.champion.id" class="tag best">推荐</span>
          </span>
          <span class="line2">
            <span v-if="r.hasData" class="wr">{{ pct(r.winRate) }}</span>
            <span v-else class="wr none">暂无数据</span>
            <span v-if="r.hasData && r.lowSample" class="tag low">数据少</span>
          </span>
          <span class="line3">
            <template v-if="r.mastery">{{ r.mastery.championLevel }} 级 · {{ fmtPoints(r.mastery.championPoints) }}</template>
            <template v-else>没玩过</template>
            <template v-if="record(r)"> · {{ record(r) }}</template>
          </span>
        </span>
      </button>
    </div>
    <div v-if="failed" class="muted">刷新失败，显示的是上一次的结果</div>
  </section>
</template>

<style scoped>
.benchbar { display: flex; flex-direction: column; gap: 8px; padding: 8px 10px; }
.head { display: flex; align-items: center; gap: 8px 12px; flex-wrap: wrap; min-width: 0; }
.label { font-family: var(--display); font-size: 12px; letter-spacing: var(--label-spacing); text-transform: var(--label-transform); color: var(--muted); }
.verdict { font-size: 14px; font-weight: 600; color: var(--text); }
.verdict.swap { color: var(--green); }
.verdict.reroll { color: var(--amber); }
.verdict.unsure { color: var(--muted); font-weight: 400; }
.hint { font-size: 12px; color: var(--amber); }
.grow { flex: 1; }
.badge.opgg { color: var(--hextech-2); border-color: var(--hextech-dim); }
.list { display: flex; gap: 6px; flex-wrap: wrap; }
.champ { display: flex; align-items: center; gap: 8px; min-height: 0; padding: 4px 10px 4px 4px; background: var(--surface-raised); border: 1px solid var(--border); border-radius: var(--radius-sm); box-shadow: none; text-align: left; text-transform: none; letter-spacing: 0; font-family: var(--serif); color: var(--text); cursor: pointer; }
.champ:hover:not(:disabled) { border-color: var(--gold-dark); background: var(--gold-soft); box-shadow: none; color: var(--text); }
.champ.current { border-color: var(--gold-dark); }
.champ.target { border-color: var(--green); }
.champ.viewing { background: var(--accent-soft); border-color: var(--accent); }
.champ img { width: 40px; height: 40px; flex: none; border-radius: var(--radius-sm); }
.info { display: flex; flex-direction: column; gap: 1px; min-width: 0; }
.line1, .line2 { display: flex; align-items: center; gap: 6px; }
.name { font-size: 13px; font-weight: 600; white-space: nowrap; }
.wr { font-size: 13px; color: var(--gold-bright); font-variant-numeric: tabular-nums; }
.wr.none { color: var(--dim); font-size: 12px; }
.line3 { font-size: 11px; color: var(--muted); white-space: nowrap; }
.tag { font-size: 10px; padding: 0 5px; border: 1px solid var(--border); border-radius: var(--radius-sm); color: var(--muted); line-height: 16px; }
.tag.cur { color: var(--gold-bright); border-color: var(--gold-dark); }
.tag.best { color: var(--green); border-color: var(--green); }
.tag.low { color: var(--amber); border-color: var(--amber); }
</style>

<script setup lang="ts">
// Champion search card. Lives in the champion bar's dropdown: it stays mounted
// (v-show) so mastery loads once and the sort choice survives closing.
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { api, fmtPoints } from '../api'
import type { Champion, Mastery } from '../types'

const props = defineProps<{ champions: Champion[]; selected: Champion | null; open: boolean }>()
const emit = defineEmits<{ select: [c: Champion]; close: [] }>()
const q = ref('')
const input = ref<HTMLInputElement | null>(null)
const sortBy = ref<'name' | 'mastery'>((localStorage.getItem('ezlol.sort') as 'name' | 'mastery') || 'name')
const mastery = ref<Record<string, Mastery>>({})
onMounted(async () => {
  try {
    mastery.value = await api.mastery()
  } catch {
    /* client not running */
  }
})
watch(
  () => props.open,
  async (o) => {
    if (!o) return
    q.value = ''
    await nextTick()
    input.value?.focus()
  },
)
function setSort(v: 'name' | 'mastery') {
  sortBy.value = v
  localStorage.setItem('ezlol.sort', v)
}
const m = (c: Champion) => mastery.value[String(c.id)]

const filtered = computed(() => {
  const s = q.value.trim().toLowerCase()
  let list = s ? props.champions.filter((c) => c.name.toLowerCase().includes(s) || c.title.toLowerCase().includes(s) || c.key.toLowerCase().includes(s)) : [...props.champions]
  if (sortBy.value === 'mastery') list = list.sort((a, b) => (m(b)?.championPoints ?? 0) - (m(a)?.championPoints ?? 0))
  return list
})

function onEnter() {
  if (filtered.value.length) emit('select', filtered.value[0])
}
</script>

<template>
  <div v-show="open" class="picker" role="dialog" aria-label="选择英雄">
    <div class="row">
      <input ref="input" v-model="q" placeholder="搜索英雄（中文名、称号或英文名）…" @keydown.enter="onEnter" @keydown.esc="emit('close')" style="flex: 1; width: auto" />
      <button class="sm" :class="{ active: sortBy === 'name' }" @click="setSort('name')" title="按名字排序">名字</button>
      <button class="sm" :class="{ active: sortBy === 'mastery' }" @click="setSort('mastery')" title="按你的熟练度排序">熟练度</button>
    </div>
    <div class="champ-grid">
      <div v-for="c in filtered" :key="c.id" class="cell" :class="{ active: selected?.id === c.id }" :title="`${c.name} ${c.title}${m(c) ? ` · 熟练度 ${m(c).championLevel} 级 · ${fmtPoints(m(c).championPoints)}` : ''}`" @click="emit('select', c)">
        <img :src="c.image" :alt="c.name" loading="lazy" />
        <span v-if="m(c)?.championLevel" class="m">{{ m(c).championLevel }}</span>
      </div>
      <div v-if="!filtered.length" class="muted" style="grid-column: 1 / -1">没有找到“{{ q }}”。</div>
    </div>
  </div>
</template>

<style scoped>
.picker { position: absolute; top: calc(100% + 6px); left: 10px; z-index: 300; width: min(600px, calc(100vw - 40px)); max-height: min(70vh, 680px); display: flex; flex-direction: column; gap: 10px; padding: 10px;
  background: var(--panel-2); border: 1px solid var(--gold-deep); border-radius: var(--radius-sm); box-shadow: var(--shadow-panel); }
.row { flex-wrap: nowrap; }
button.sm { padding: 6px 8px; font-size: 10px; }
.champ-grid { grid-template-columns: repeat(9, 1fr); overflow-y: auto; min-height: 0; padding-right: 4px; }
.cell { position: relative; cursor: pointer; }
.cell img { width: 100%; aspect-ratio: 1; border: 1px solid var(--gold-deep); display: block; filter: saturate(.85); transition: all .12s; }
.cell:hover img { border-color: var(--gold); filter: saturate(1.1); transform: scale(1.06); }
.cell.active img { border-color: var(--hextech-2); box-shadow: 0 0 10px var(--accent-glow); filter: saturate(1.1); }
.m { position: absolute; right: 1px; bottom: 1px; font-size: 9px; font-family: var(--display); color: var(--gold); background: var(--pill-bg); padding: 0 3px; border: 1px solid var(--gold-deep); }
</style>

<script setup lang="ts">
// Items that go with augments (ARAM Mayhem), from games where players took
// them: what to build next given what you own, and the best core sets.
// In game it follows the augments you took; outside a game, click an augment
// in the augment lists to preview it.
import { computed, ref, watch } from 'vue'
import Widget from './Widget.vue'
import { api } from '../api'
import { buildState as s } from '../build'
import type { AugBuildResponse, Game } from '../types'

const props = defineProps<{ game: Game | null }>()

const STYLE: Record<string, string> = { crit: '暴击流', on_hit: '攻速特效流', ap: '法强流', hybrid: '混伤流', lethality: '穿甲流', tank: '坦克流', bruiser: '战士流', ad: '物理流' }
const inGame = computed(() => !!props.game?.inGame && props.game.mode === 'aram')
const champId = computed(() => (inGame.value ? props.game?.me?.champion.id : s.build?.champion.id) ?? 0)
const augs = computed(() => (inGame.value && props.game?.picks?.length ? props.game.picks.map((p) => p.augmentId) : s.augPreview))
const owned = computed(() => (inGame.value ? props.game?.me?.items ?? [] : []))
const show = computed(() => inGame.value || s.build?.mode === 'aram')

const data = ref<AugBuildResponse | null>(null)
const err = ref('')
const loading = ref(false)
let seq = 0
watch(
  () => `${champId.value}|${augs.value.join(',')}|${[...owned.value].sort().join(',')}`,
  async () => {
    const my = ++seq
    err.value = ''
    if (!champId.value || !augs.value.length) {
      data.value = null
      return
    }
    loading.value = true
    try {
      const r = await api.augmentBuild(champId.value, augs.value, owned.value)
      if (my === seq) data.value = r
    } catch (e) {
      if (my === seq) {
        data.value = null
        err.value = (e as Error).message
      }
    } finally {
      if (my === seq) loading.value = false
    }
  },
  { immediate: true },
)
const pct = (x: number) => `${(x * 100).toFixed(1)}%`
const signed = (x: number) => `${x >= 0 ? '+' : ''}${(x * 100).toFixed(1)}%`
const n = (g: number) => (g >= 10000 ? `${(g / 10000).toFixed(1)} 万` : String(g))
const ownedSet = computed(() => new Set(owned.value))
// Previewing from the augment lists: bring this box into view.
watch(
  () => s.augPreview.length,
  (n, prev) => {
    if (!inGame.value && n > (prev ?? 0)) setTimeout(() => document.querySelector('[gs-id="aug-build"]')?.scrollIntoView({ behavior: 'smooth', block: 'nearest' }), 50)
  },
)
</script>

<template>
  <Widget v-if="show" id="aug-build" :w="6">
    <h2>
      强化出装
      <span class="muted sub">
        <template v-if="data?.augments?.length">根据：{{ data.augments.map((a) => a.name).join(' + ') }}<template v-if="data.plan.games"> · {{ n(data.plan.games) }} 场</template></template>
      </span>
      <button v-if="!inGame && s.augPreview.length" class="sm clear" @click="s.augPreview = []">清除</button>
    </h2>

    <div v-if="!augs.length" class="muted">
      {{ inGame ? '选了强化之后，这里会按你选的强化和已有装备推荐下一件出什么。' : '点上面强化列表里的任意一个强化，看选了它之后大家怎么出装。' }}
    </div>
    <div v-else-if="loading && !data" class="muted">正在加载强化出装数据…</div>
    <div v-else-if="err" class="note">{{ err }}</div>
    <template v-else-if="data">
      <div class="cols">
        <div>
          <h3>{{ inGame ? `下一件（第 ${data.plan.slot} 件）` : '第 1 件选什么' }}</h3>
          <div v-if="!data.plan.next?.length" class="muted">样本不够，没有可靠推荐。</div>
          <div v-for="(x, i) in data.plan.next ?? []" :key="x.item.id" class="row" :class="{ top: i === 0 }">
            <img :src="x.item.image" :alt="x.item.name" />
            <span class="name">{{ x.item.name }}</span>
            <span class="stat"><b>{{ pct(x.winRate) }}</b><i :class="x.lift >= 0 ? 'up' : 'down'">{{ signed(x.lift) }}</i> · {{ n(x.games) }} 场</span>
          </div>
        </div>
        <div>
          <h3>核心出装</h3>
          <div v-if="!data.plan.cores?.length" class="muted">样本不够。</div>
          <div v-for="(c, i) in data.plan.cores ?? []" :key="i" class="core">
            <div class="ch">
              <span class="badge">{{ STYLE[c.style] ?? c.style }}</span>
              <span class="stat"><b>{{ pct(c.winRate) }}</b>{{ n(c.games) }} 场</span>
              <span v-if="inGame && c.owned" class="muted">已有 {{ c.owned }} 件</span>
            </div>
            <div class="items">
              <template v-for="(it, j) in c.items" :key="it.id">
                <span v-if="j" class="arrow">›</span>
                <img v-if="it.image" :src="it.image" :title="it.name" :alt="it.name" :class="{ have: ownedSet.has(it.id) }" /><span v-else class="noimg">{{ it.name || it.id }}</span>
              </template>
              <img v-if="c.boots?.image" :src="c.boots.image" :title="c.boots.name" class="boots" :class="{ have: ownedSet.has(c.boots.id) }" />
            </div>
          </div>
        </div>
      </div>
      <div class="foot muted">下一件的 +/− 对比的是这个英雄第 {{ data.plan.slot }} 件装备的平均胜率（{{ pct(data.plan.slotBase) }}）。数据：aramkit · {{ data.version }}</div>
    </template>
  </Widget>
</template>

<style scoped>
.sub { text-transform: none; letter-spacing: 0; margin-left: 8px; }
h2 { display: flex; align-items: center; }
.clear { margin-left: auto; }
button.sm { padding: 2px 8px; font-size: 11px; min-height: 0; }
.cols { display: grid; grid-template-columns: 1fr 1fr; gap: 14px; }
@media (max-width: 900px) { .cols { grid-template-columns: 1fr; } }
h3 { margin-top: 4px; }
.row { display: grid; grid-template-columns: 30px 1fr auto; gap: 8px; align-items: center; padding: 4px 6px; border: 1px solid var(--line); background: var(--surface-raised); margin-bottom: 4px; }
.row.top { border-color: var(--hextech-2); }
.row img { width: 30px; height: 30px; border: 1px solid var(--gold-deep); background: #000; }
.name { font-size: 13px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.stat { font-size: 12px; color: var(--muted); white-space: nowrap; }
.stat b { color: var(--gold-bright); }
.stat i { font-style: normal; margin-left: 4px; }
.up { color: #4ec98a; } .down { color: var(--red, #e84057); }
.core { padding: 6px; border: 1px solid var(--line); background: var(--surface-raised); margin-bottom: 6px; }
.ch { display: flex; gap: 8px; align-items: center; margin-bottom: 4px; font-size: 12px; }
.items { display: flex; align-items: center; gap: 3px; flex-wrap: wrap; }
.items img { width: 30px; height: 30px; border: 1px solid var(--gold-deep); background: #000; }
.items img.have { border-color: var(--hextech-2); box-shadow: 0 0 6px var(--accent-glow); }
.items img.boots { margin-left: 8px; }
.arrow { color: var(--muted); }
.foot { font-size: 11px; margin-top: 6px; }
.note { font-size: 12px; }
.noimg { font-size: 11px; padding: 0 4px; border: 1px solid var(--gold-deep); }
</style>
